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
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

type esloggerTraceConfig struct {
	VMName    string `json:"vm_name"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at"`
}

type esloggerTraceSession struct {
	ID        string `json:"id"`
	VMName    string `json:"vm_name"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
	StoppedAt string `json:"stopped_at,omitempty"`
	LogPath   string `json:"log_path"`
	Note      string `json:"note,omitempty"`
}

var traceNow = time.Now

func handleTraceCommand(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printTraceUsage(os.Stdout)
		return nil
	}
	switch args[0] {
	case "enable":
		return runTraceEnable(args[1:])
	case "start":
		return runTraceStart(args[1:])
	case "stop":
		return runTraceStop(args[1:])
	case "export":
		return runTraceExport(args[1:])
	case "status":
		return runTraceStatus(args[1:])
	default:
		printTraceUsage(os.Stderr)
		return fmt.Errorf("unknown trace subcommand: %s", args[0])
	}
}

func printTraceUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove trace <subcommand> [options]

Subcommands:
  enable <vm>                  Record that eslogger tracing is desired
  start <vm> [--id ID]         Create an eslogger trace session artifact
  stop <vm> [--id ID]          Mark an eslogger trace session stopped
  status <vm>                  Show trace configuration and latest session
  export <vm> [--id ID] --out PATH

eslogger tracing is supported for macOS guests. Linux/Windows guests return a
clear unsupported diagnostic. Start records artifact paths immediately; if the
guest-side eslogger capture is not wired in yet, the session is marked
unsupported instead of hiding the failure.`)
}

func runTraceEnable(args []string) error {
	vm, err := oneTraceVMArg("trace enable", args)
	if err != nil {
		return err
	}
	dir, err := requireTraceMacOSVM(vm)
	if err != nil {
		return err
	}
	cfg := esloggerTraceConfig{VMName: vm, Enabled: true, UpdatedAt: traceNow().UTC().Format(time.RFC3339Nano)}
	if err := writeJSONFile(traceConfigPath(dir), cfg); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "enabled eslogger tracing for %s; config: %s\n", vm, traceConfigPath(dir))
	return nil
}

func runTraceStart(args []string) error {
	fs := flag.NewFlagSet("trace start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	id := fs.String("id", "", "trace session id")
	if err := fs.Parse(moveKnownFlagsFirst(args, map[string]bool{"id": true})); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cove trace start <vm> [--id ID]")
	}
	vm := fs.Arg(0)
	dir, err := requireTraceMacOSVM(vm)
	if err != nil {
		return err
	}
	sessionID := strings.TrimSpace(*id)
	if sessionID == "" {
		sessionID = traceNow().UTC().Format("20060102-150405")
	}
	sessionDir := traceSessionDir(dir, sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return fmt.Errorf("trace start: create session: %w", err)
	}
	session := esloggerTraceSession{
		ID:        sessionID,
		VMName:    vm,
		Status:    "unsupported",
		StartedAt: traceNow().UTC().Format(time.RFC3339Nano),
		LogPath:   filepath.Join(sessionDir, "eslogger.jsonl"),
		Note:      "guest-side eslogger capture is not available from this host build; install eslogger in the macOS guest and place JSONL at log_path before export",
	}
	if err := writeJSONFile(traceSessionPath(dir, sessionID), session); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "trace session %s prepared at %s\n", sessionID, sessionDir)
	fmt.Fprintf(os.Stdout, "unsupported: %s\n", session.Note)
	return nil
}

func runTraceStop(args []string) error {
	fs := flag.NewFlagSet("trace stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	id := fs.String("id", "", "trace session id")
	if err := fs.Parse(moveKnownFlagsFirst(args, map[string]bool{"id": true})); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cove trace stop <vm> [--id ID]")
	}
	vm := fs.Arg(0)
	dir, err := requireTraceMacOSVM(vm)
	if err != nil {
		return err
	}
	sessionID, err := resolveTraceSessionID(dir, *id)
	if err != nil {
		return err
	}
	session, err := loadTraceSession(dir, sessionID)
	if err != nil {
		return err
	}
	session.Status = "stopped"
	session.StoppedAt = traceNow().UTC().Format(time.RFC3339Nano)
	if err := writeJSONFile(traceSessionPath(dir, sessionID), session); err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "stopped trace session %s\n", sessionID)
	return nil
}

func runTraceStatus(args []string) error {
	vm, err := oneTraceVMArg("trace status", args)
	if err != nil {
		return err
	}
	dir, err := requireExistingVMForControl(vm)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "trace config: %s\n", traceConfigPath(dir))
	if cfg, err := loadTraceConfig(dir); err == nil && cfg.Enabled {
		fmt.Fprintf(os.Stdout, "enabled: yes updated_at=%s\n", cfg.UpdatedAt)
	} else {
		fmt.Fprintln(os.Stdout, "enabled: no")
	}
	if id, err := latestTraceSessionID(dir); err == nil && id != "" {
		session, _ := loadTraceSession(dir, id)
		fmt.Fprintf(os.Stdout, "latest: %s status=%s log=%s\n", id, session.Status, session.LogPath)
	} else {
		fmt.Fprintln(os.Stdout, "latest: none")
	}
	return nil
}

func runTraceExport(args []string) error {
	fs := flag.NewFlagSet("trace export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	id := fs.String("id", "", "trace session id")
	out := fs.String("out", "", "output tar.gz path")
	if err := fs.Parse(moveKnownFlagsFirst(args, map[string]bool{"id": true, "out": true})); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 || strings.TrimSpace(*out) == "" {
		return fmt.Errorf("usage: cove trace export <vm> [--id ID] --out PATH")
	}
	vm := fs.Arg(0)
	dir, err := requireTraceMacOSVM(vm)
	if err != nil {
		return err
	}
	sessionID, err := resolveTraceSessionID(dir, *id)
	if err != nil {
		return err
	}
	sessionDir := traceSessionDir(dir, sessionID)
	if err := os.MkdirAll(filepath.Dir(*out), 0755); err != nil {
		return fmt.Errorf("trace export: create output dir: %w", err)
	}
	f, err := os.Create(*out)
	if err != nil {
		return fmt.Errorf("trace export: create %s: %w", *out, err)
	}
	if err := writeDirTarGz(f, sessionDir); err != nil {
		_ = f.Close()
		_ = os.Remove(*out)
		return fmt.Errorf("trace export: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("trace export: close %s: %w", *out, err)
	}
	fmt.Fprintf(os.Stdout, "exported trace %s for %s to %s\n", sessionID, vm, *out)
	return nil
}

func oneTraceVMArg(usage string, args []string) (string, error) {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return "", fmt.Errorf("usage: cove %s <vm>", usage)
	}
	return args[0], nil
}

func requireTraceMacOSVM(vm string) (string, error) {
	dir, err := requireExistingVMForControl(vm)
	if err != nil {
		return "", err
	}
	if osType := vmconfig.DetectOSType(dir); osType != "macOS" {
		return "", fmt.Errorf("trace: eslogger is supported for macOS guests; VM %q is %s", vm, osType)
	}
	return dir, nil
}

func traceRoot(dir string) string {
	return filepath.Join(dir, "traces", "eslogger")
}

func traceConfigPath(dir string) string {
	return filepath.Join(traceRoot(dir), "config.json")
}

func traceSessionDir(dir, id string) string {
	return filepath.Join(traceRoot(dir), "sessions", id)
}

func traceSessionPath(dir, id string) string {
	return filepath.Join(traceSessionDir(dir, id), "session.json")
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("write json: create dir: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("write json: marshal: %w", err)
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func loadTraceConfig(dir string) (esloggerTraceConfig, error) {
	var cfg esloggerTraceConfig
	data, err := os.ReadFile(traceConfigPath(dir))
	if err != nil {
		return cfg, err
	}
	return cfg, json.Unmarshal(data, &cfg)
}

func loadTraceSession(dir, id string) (esloggerTraceSession, error) {
	var session esloggerTraceSession
	data, err := os.ReadFile(traceSessionPath(dir, id))
	if err != nil {
		return session, err
	}
	return session, json.Unmarshal(data, &session)
}

func resolveTraceSessionID(dir, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id != "" {
		if _, err := os.Stat(traceSessionPath(dir, id)); err != nil {
			return "", fmt.Errorf("trace session %q not found", id)
		}
		return id, nil
	}
	latest, err := latestTraceSessionID(dir)
	if err != nil {
		return "", err
	}
	if latest == "" {
		return "", fmt.Errorf("trace: no eslogger sessions found")
	}
	return latest, nil
}

func latestTraceSessionID(dir string) (string, error) {
	root := filepath.Join(traceRoot(dir), "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "", nil
	}
	return ids[len(ids)-1], nil
}

func writeDirTarGz(w io.Writer, dir string) error {
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
		_, err = io.Copy(tw, f)
		return err
	}); err != nil {
		_ = tw.Close()
		_ = gz.Close()
		return err
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}
