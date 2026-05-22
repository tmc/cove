package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

type supportBundleOptions struct {
	VM  string
	Out string
}

type supportBundleManifest struct {
	CreatedAt string `json:"created_at"`
	Version   string `json:"version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	VM        string `json:"vm,omitempty"`
	Redacted  bool   `json:"redacted"`
}

type supportCommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

var supportRunCommand = runSupportCommand

func runSupportCommand(ctx context.Context, args ...string) supportCommandResult {
	exe, err := os.Executable()
	if err != nil {
		return supportCommandResult{ExitCode: -1, Err: err}
	}
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		code = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
	}
	return supportCommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code, Err: err}
}

func handleSupportCommand(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printSupportUsage(os.Stdout)
		if len(args) == 0 {
			return fmt.Errorf("support: command required")
		}
		return nil
	}
	switch args[0] {
	case "bundle":
		return runSupportBundle(args[1:], os.Stdout)
	default:
		return fmt.Errorf("unknown support command: %s", args[0])
	}
}

func printSupportUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove support <command>

Commands:
  bundle [-vm NAME] [-out PATH]   Create a redacted diagnostics bundle`)
}

func runSupportBundle(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("support bundle", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printSupportBundleUsage(fs.Output()) }
	vm := fs.String("vm", "", "VM name to include VM-specific diagnostics")
	out := fs.String("out", "", "output .tar.gz path")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove support bundle [-vm NAME] [-out PATH]")
	}
	opts := supportBundleOptions{VM: *vm, Out: *out}
	path, err := createSupportBundle(opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Support bundle written to %s\n", path)
	fmt.Fprintln(stdout, "Redacted diagnostics include host readiness, command inventory, trace/log discovery, and recent run metadata.")
	return nil
}

func printSupportBundleUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove support bundle [-vm NAME] [-out PATH]

Create a redacted diagnostics archive for support.

The bundle includes host readiness, version/signing data, helper and daemon
status, storage census, recent run and recording metadata, and optional
VM-specific doctor/control diagnostics when -vm is set.

Flags:
  -vm NAME    include diagnostics for a VM
  -out PATH   output .tar.gz path`)
}

func createSupportBundle(opts supportBundleOptions) (string, error) {
	out := opts.Out
	if out == "" {
		out = filepath.Join(os.TempDir(), "cove-support-"+time.Now().UTC().Format("20060102-150405")+".tar.gz")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", fmt.Errorf("support bundle: create output directory: %w", err)
	}
	tmp := out + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return "", fmt.Errorf("support bundle: create: %w", err)
	}
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmp)
		}
	}()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := writeSupportBundle(tw, opts); err != nil {
		_ = tw.Close()
		_ = gz.Close()
		_ = f.Close()
		return "", err
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		_ = f.Close()
		return "", fmt.Errorf("support bundle: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("support bundle: close gzip: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("support bundle: close file: %w", err)
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", fmt.Errorf("support bundle: rename: %w", err)
	}
	removeTmp = false
	return out, nil
}

func writeSupportBundle(tw *tar.Writer, opts supportBundleOptions) error {
	manifest := supportBundleManifest{
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Version:   versionInfo(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		VM:        opts.VM,
		Redacted:  true,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := supportWriteFile(tw, "manifest.json", append(manifestJSON, '\n')); err != nil {
		return err
	}
	if err := supportWriteFile(tw, "doctor-host.json", supportHostDoctorJSON()); err != nil {
		return err
	}
	if err := supportWriteFile(tw, "version.txt", []byte(versionInfo()+"\n")); err != nil {
		return err
	}
	if err := supportWriteFile(tw, "host.txt", []byte(supportHostSummary())); err != nil {
		return err
	}
	vmMissing := false
	if opts.VM != "" {
		if _, ok := vmconfig.ExistingPath(opts.VM); !ok {
			vmMissing = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, spec := range supportCommandSpecs(opts.VM, !vmMissing) {
		res := supportRunCommand(ctx, spec.args...)
		body := supportCommandBody(spec.args, res)
		if err := supportWriteFile(tw, spec.path, []byte(redactSupportText(body))); err != nil {
			return err
		}
	}
	if vmMissing {
		body := fmt.Sprintf("no VM named %q under %s\n  list VMs: cove list\n  create a VM: cove up -user <name>\n", opts.VM, vmconfig.BaseDir())
		if err := supportWriteFile(tw, "vm/not-found.txt", []byte(redactSupportText(body))); err != nil {
			return err
		}
	}
	return nil
}

type supportCommandSpec struct {
	path string
	args []string
}

func supportCommandSpecs(vm string, includeVM bool) []supportCommandSpec {
	specs := []supportCommandSpec{
		{"commands/commands.json", []string{"commands", "--json"}},
		{"commands/helper-status.txt", []string{"helper", "status"}},
		{"commands/daemon-status.txt", []string{"daemon", "status"}},
		{"commands/daemon-metrics.txt", []string{"daemon", "metrics", "--json"}},
		{"commands/storage-census.txt", []string{"storage", "census", "-human"}},
		{"commands/runs-list.json", []string{"runs", "list", "--json", "--limit", "20"}},
		{"commands/recording-list.json", []string{"recording", "list", "--json", "--limit", "20"}},
		{"commands/trace-capabilities.json", []string{"trace", "capabilities", "--json"}},
		{"commands/logs-help.txt", []string{"logs", "-h"}},
	}
	if vm != "" && includeVM {
		specs = append(specs,
			supportCommandSpec{"vm/doctor.txt", []string{"doctor", "-vm", vm, "-v"}},
			supportCommandSpec{"vm/ctl-capabilities.txt", []string{"ctl", "-vm", vm, "capabilities"}},
			supportCommandSpec{"vm/ctl-gui-status.txt", []string{"ctl", "-vm", vm, "gui", "status"}},
			supportCommandSpec{"vm/ctl-vnc-status.txt", []string{"ctl", "-vm", vm, "vnc", "status"}},
			supportCommandSpec{"vm/ctl-agent-status.txt", []string{"ctl", "-vm", vm, "agent-status"}},
			supportCommandSpec{"vm/trace-status.txt", []string{"trace", "status", vm}},
		)
	}
	return specs
}

func supportHostDoctorJSON() []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	_ = enc.Encode(collectHostDoctorReport())
	return []byte(redactSupportText(buf.String()))
}

func supportHostSummary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "version: %s\n", versionInfo())
	fmt.Fprintf(&b, "goos: %s\n", runtime.GOOS)
	fmt.Fprintf(&b, "goarch: %s\n", runtime.GOARCH)
	if out, err := hostDoctorRunCommand("sw_vers"); err == nil {
		b.WriteString(redactSupportText(string(out)))
	}
	return b.String()
}

func supportCommandBody(args []string, res supportCommandResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "$ cove %s\n", strings.Join(args, " "))
	fmt.Fprintf(&b, "exit: %d\n", res.ExitCode)
	if res.Err != nil {
		fmt.Fprintf(&b, "error: %v\n", res.Err)
	}
	if res.Stdout != "" {
		b.WriteString("\nstdout:\n")
		b.WriteString(res.Stdout)
	}
	if res.Stderr != "" {
		b.WriteString("\nstderr:\n")
		b.WriteString(res.Stderr)
	}
	return b.String()
}

func supportWriteFile(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o600,
		Size:    int64(len(data)),
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("support bundle: write %s header: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("support bundle: write %s: %w", name, err)
	}
	return nil
}

var supportRedactors = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(password|token|secret|auth_token)(["'=:\s]+)([^\s,"']+)`),
	regexp.MustCompile(`/Users/[^/\s"']+(?:/[^\s"']*)?`),
}

func redactSupportText(s string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		s = strings.ReplaceAll(s, home, "$HOME")
	}
	if user := os.Getenv("USER"); user != "" {
		s = strings.ReplaceAll(s, user, "$USER")
	}
	s = supportRedactors[0].ReplaceAllString(s, "Bearer REDACTED")
	s = supportRedactors[1].ReplaceAllString(s, `${1}${2}REDACTED`)
	s = supportRedactors[2].ReplaceAllString(s, "$HOME")
	return s
}
