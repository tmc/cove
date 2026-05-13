package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

type recordingArtifact struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Kind string `json:"kind"`
	Size int64  `json:"size,omitempty"`
}

type recordingEntry struct {
	RunID     string              `json:"run_id"`
	VMName    string              `json:"vm_name,omitempty"`
	StartedAt string              `json:"started_at,omitempty"`
	Path      string              `json:"path"`
	Artifacts []recordingArtifact `json:"artifacts"`
}

func handleRecordingCommand(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printRecordingUsage(os.Stdout)
		return nil
	}
	switch args[0] {
	case "list", "ls":
		return runRecordingList(args[1:])
	case "export":
		return runRecordingExport(args[1:])
	default:
		printRecordingUsage(os.Stderr)
		return fmt.Errorf("unknown recording subcommand: %s", args[0])
	}
}

func printRecordingUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove recording <subcommand> [options]

Subcommands:
  list [--json] [--limit N]       List run/session recording artifacts
  export <run-id-prefix> --out PATH

Recording export writes a gzip tarball containing the run manifest, metrics,
events, logs, screenshots, replay files, and trace artifacts when present.`)
}

func runRecordingList(args []string) error {
	fs := flag.NewFlagSet("recording list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "emit JSON")
	limit := fs.Int("limit", 25, "maximum recordings to show")
	fs.Usage = func() { printRecordingListUsage(fs.Output()) }
	if err := fs.Parse(moveKnownFlagsFirst(args, map[string]bool{"json": false, "limit": true})); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove recording list [--json] [--limit N]")
	}
	if *limit < 0 {
		return fmt.Errorf("recording list: --limit must be non-negative")
	}
	recordings, err := listRecordings(vmconfig.RunsDir(), *limit)
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(recordings)
	}
	return printRecordingTable(os.Stdout, recordings)
}

func printRecordingListUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove recording list [--json] [--limit N]

List run/session recording artifacts from ~/.vz/runs. A recording is any run
with manifest, events, metrics, logs, screenshots, replay, or trace artifacts.`)
}

func runRecordingExport(args []string) error {
	prefix, out, err := parseRecordingExportArgs(args)
	if err != nil {
		return err
	}
	if prefix == "" || out == "" {
		return fmt.Errorf("usage: cove recording export <run-id-prefix> --out PATH")
	}
	dir, err := matchRecordingDir(vmconfig.RunsDir(), prefix)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return fmt.Errorf("recording export: create output dir: %w", err)
	}
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("recording export: create %s: %w", out, err)
	}
	closeErr := func() error {
		if err := f.Close(); err != nil {
			return fmt.Errorf("recording export: close %s: %w", out, err)
		}
		return nil
	}
	if err := writeRecordingTarGz(f, dir); err != nil {
		_ = f.Close()
		_ = os.Remove(out)
		return err
	}
	if err := closeErr(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "exported recording %s to %s\n", filepath.Base(dir), out)
	return nil
}

func parseRecordingExportArgs(args []string) (prefix, out string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--out" || arg == "-out":
			i++
			if i >= len(args) || args[i] == "" {
				return "", "", fmt.Errorf("recording export: --out requires a path")
			}
			out = args[i]
		case strings.HasPrefix(arg, "--out="):
			out = strings.TrimPrefix(arg, "--out=")
		case strings.HasPrefix(arg, "-out="):
			out = strings.TrimPrefix(arg, "-out=")
		default:
			if strings.HasPrefix(arg, "-") {
				return "", "", fmt.Errorf("unknown recording export flag %q", arg)
			}
			if prefix != "" {
				return "", "", fmt.Errorf("usage: cove recording export <run-id-prefix> --out PATH")
			}
			prefix = arg
		}
	}
	return prefix, out, nil
}

func listRecordings(root string, limit int) ([]recordingEntry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("recording list: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	var out []recordingEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rec, ok := loadRecordingEntry(filepath.Join(root, entry.Name()))
		if !ok {
			continue
		}
		out = append(out, rec)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	if limit == 0 {
		return nil, nil
	}
	return out, nil
}

func loadRecordingEntry(dir string) (recordingEntry, bool) {
	artifacts := recordingArtifacts(dir)
	if len(artifacts) == 0 {
		return recordingEntry{}, false
	}
	rec := recordingEntry{
		RunID:     filepath.Base(dir),
		Path:      dir,
		Artifacts: artifacts,
	}
	var mf runManifest
	if data, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
		_ = json.Unmarshal(data, &mf)
		rec.VMName = mf.VMName
		rec.StartedAt = mf.StartedAt
	}
	return rec, true
}

func recordingArtifacts(dir string) []recordingArtifact {
	known := []struct {
		name string
		kind string
	}{
		{"manifest.json", "metadata"},
		{"metrics.jsonl", "events"},
		{"events.jsonl", "events"},
		{"stdout.log", "log"},
		{"stderr.log", "log"},
		{"screenshots", "screenshots"},
		{"replay", "replay"},
		{"traces", "trace"},
	}
	var artifacts []recordingArtifact
	for _, item := range known {
		path := filepath.Join(dir, item.name)
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		artifacts = append(artifacts, recordingArtifact{
			Name: item.name,
			Path: path,
			Kind: item.kind,
			Size: artifactSize(path, info),
		})
	}
	return artifacts
}

func artifactSize(path string, info os.FileInfo) int64 {
	if !info.IsDir() {
		return info.Size()
	}
	var total int64
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func printRecordingTable(w io.Writer, recordings []recordingEntry) error {
	if len(recordings) == 0 {
		fmt.Fprintln(w, "No recordings found. Run cove run -fork-from or cove agent-sandbox run to create run artifacts.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN ID\tVM\tARTIFACTS\tPATH")
	for _, rec := range recordings {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", rec.RunID, rec.VMName, artifactNames(rec.Artifacts), rec.Path)
	}
	return tw.Flush()
}

func artifactNames(artifacts []recordingArtifact) string {
	names := make([]string, len(artifacts))
	for i, artifact := range artifacts {
		names[i] = artifact.Name
	}
	return strings.Join(names, ",")
}

func matchRecordingDir(root, prefix string) (string, error) {
	recordings, err := listRecordings(root, -1)
	if err != nil {
		return "", err
	}
	var matches []recordingEntry
	for _, rec := range recordings {
		if strings.HasPrefix(rec.RunID, prefix) {
			matches = append(matches, rec)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("recording %q not found under %s", prefix, root)
	case 1:
		return matches[0].Path, nil
	default:
		return "", fmt.Errorf("recording prefix %q is ambiguous", prefix)
	}
}

func writeRecordingTarGz(w io.Writer, dir string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(dir), path)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		return nil
	}); err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return fmt.Errorf("recording export: %w", err)
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return fmt.Errorf("recording export: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("recording export: %w", err)
	}
	return nil
}
