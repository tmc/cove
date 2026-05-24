package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tmc/cove/internal/imagestore"
)

type ImageHistory struct {
	Ref     string              `json:"ref"`
	Entries []ImageHistoryEntry `json:"entries"`
}

type ImageHistoryEntry struct {
	Ref           string              `json:"ref"`
	Timestamp     string              `json:"timestamp,omitempty"`
	Size          int64               `json:"size"`
	ParentRef     string              `json:"parent_ref,omitempty"`
	SourceCommand string              `json:"source_command,omitempty"`
	CoveCommit    string              `json:"cove_commit,omitempty"`
	AgentCommit   string              `json:"agent_commit,omitempty"`
	AgentFeatures []string            `json:"agent_features,omitempty"`
	Layers        []ImageHistoryLayer `json:"layers"`
}

type ImageHistoryLayer struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

func ImageHistoryFor(ref imagestore.Ref) (ImageHistory, error) {
	seen := map[string]bool{}
	var entries []ImageHistoryEntry
	cur := ref
	for {
		if seen[cur.String()] {
			break
		}
		seen[cur.String()] = true
		entry, parent, err := imageHistoryEntry(cur)
		if err != nil {
			if len(entries) == 0 {
				return ImageHistory{}, err
			}
			break
		}
		entries = append(entries, entry)
		if parent == "" {
			break
		}
		next, err := ParseImageRef(parent)
		if err != nil || !ImageExists(next) {
			break
		}
		cur = next
	}
	return ImageHistory{Ref: ref.String(), Entries: entries}, nil
}

func imageHistoryEntry(ref imagestore.Ref) (ImageHistoryEntry, string, error) {
	manifest, err := LoadImageManifest(ref)
	if err != nil {
		return ImageHistoryEntry{}, "", fmt.Errorf("image history: %w", err)
	}
	parent := strings.TrimSpace(manifest.SourceImage)
	if parent == "" {
		parent = strings.TrimSpace(manifest.BaseImage)
	}
	entry := ImageHistoryEntry{
		Ref:           ref.String(),
		Size:          manifest.DiskSize,
		ParentRef:     parent,
		SourceCommand: strings.TrimSpace(manifest.BuildRecipe),
		CoveCommit:    strings.TrimSpace(manifest.CoveCommit),
		AgentCommit:   strings.TrimSpace(manifest.AgentCommit),
		AgentFeatures: append([]string(nil), manifest.AgentFeatures...),
	}
	ts := manifest.BuiltAt
	if ts.IsZero() {
		ts = manifest.CreatedAt
	}
	if !ts.IsZero() {
		entry.Timestamp = ts.UTC().Format(time.RFC3339)
	}
	layers, err := imageHistoryLayers(ref)
	if err != nil {
		return ImageHistoryEntry{}, "", err
	}
	entry.Layers = layers
	return entry, parent, nil
}

func imageHistoryLayers(ref imagestore.Ref) ([]ImageHistoryLayer, error) {
	layers := make([]ImageHistoryLayer, 0, len(imageDataFiles))
	for _, name := range imageDataFiles {
		path := filepath.Join(ref.Path(), name)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("image history: stat %s: %w", name, err)
		}
		if info.IsDir() {
			continue
		}
		sum, size, err := sha256AndSize(path)
		if err != nil {
			return nil, fmt.Errorf("image history: digest %s: %w", name, err)
		}
		layers = append(layers, ImageHistoryLayer{Name: name, Digest: "sha256:" + sum, Size: size})
	}
	return layers, nil
}

func runImageHistory(args []string) error {
	fs := flag.NewFlagSet("image history", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cove image history <ref> [-json]")
	}
	ref, err := ParseImageRef(fs.Arg(0))
	if err != nil {
		return err
	}
	history, err := ImageHistoryFor(ref)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeImageHistoryJSON(os.Stdout, history)
	}
	return writeImageHistoryText(os.Stdout, history)
}

func writeImageHistoryJSON(w io.Writer, history ImageHistory) error {
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("encode image history: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func writeImageHistoryText(w io.Writer, history ImageHistory) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "REF\tTIMESTAMP\tSIZE\tPARENT\tSOURCE")
	for _, e := range history.Entries {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n", e.Ref, e.Timestamp, e.Size, e.ParentRef, e.SourceCommand)
		for _, layer := range e.Layers {
			fmt.Fprintf(tw, "  %s\t%s\t%d\t\t\n", layer.Name, layer.Digest, layer.Size)
		}
	}
	return tw.Flush()
}
