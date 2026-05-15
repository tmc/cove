package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestCreateSupportBundleRedactsDiagnostics(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USER", "alice")
	oldRun := supportRunCommand
	oldHostRun := hostDoctorRunCommand
	t.Cleanup(func() {
		supportRunCommand = oldRun
		hostDoctorRunCommand = oldHostRun
	})
	supportRunCommand = func(ctx context.Context, args ...string) supportCommandResult {
		return supportCommandResult{
			Stdout:   "path=/Users/alice/project\nAuthorization: Bearer abc.def\npassword: swordfish\n",
			ExitCode: 0,
		}
	}
	hostDoctorRunCommand = func(name string, args ...string) ([]byte, error) {
		switch name {
		case "sw_vers":
			return []byte("15.5\n"), nil
		case "codesign":
			return []byte("<key>com.apple.security.virtualization</key><true/>"), nil
		case "xcode-select":
			return []byte("/Library/Developer/CommandLineTools\n"), nil
		default:
			return nil, nil
		}
	}
	if err := os.MkdirAll(filepath.Join(vmconfig.BaseDir(), "work"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "bundle.tar.gz")
	path, err := createSupportBundle(supportBundleOptions{VM: "work", Out: out})
	if err != nil {
		t.Fatalf("createSupportBundle: %v", err)
	}
	if path != out {
		t.Fatalf("path = %q, want %q", path, out)
	}
	files := readSupportBundleFiles(t, out)
	for _, name := range []string{
		"manifest.json",
		"doctor-host.json",
		"commands/commands.json",
		"commands/helper-status.txt",
		"commands/trace-capabilities.json",
		"commands/logs-help.txt",
		"vm/ctl-agent-status.txt",
	} {
		if _, ok := files[name]; !ok {
			t.Fatalf("bundle missing %s; files=%v", name, supportBundleMapKeys(files))
		}
	}
	all := strings.Join(mapValues(files), "\n")
	for _, forbidden := range []string{"Bearer abc.def", "swordfish", "/Users/alice"} {
		if strings.Contains(all, forbidden) {
			t.Fatalf("bundle was not redacted; found %q in:\n%s", forbidden, all)
		}
	}
	for _, want := range []string{"Bearer REDACTED", "password: REDACTED", "$HOME"} {
		if !strings.Contains(all, want) {
			t.Fatalf("bundle missing redacted marker %q in:\n%s", want, all)
		}
	}
}

func TestCreateSupportBundleMissingVMIsReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldRun := supportRunCommand
	oldHostRun := hostDoctorRunCommand
	t.Cleanup(func() {
		supportRunCommand = oldRun
		hostDoctorRunCommand = oldHostRun
	})

	var commands []string
	supportRunCommand = func(ctx context.Context, args ...string) supportCommandResult {
		commands = append(commands, strings.Join(args, " "))
		return supportCommandResult{ExitCode: 0}
	}
	hostDoctorRunCommand = func(name string, args ...string) ([]byte, error) {
		return nil, nil
	}

	out := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if _, err := createSupportBundle(supportBundleOptions{VM: "missing", Out: out}); err != nil {
		t.Fatalf("createSupportBundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing VM dir stat error = %v, want not exist", err)
	}
	files := readSupportBundleFiles(t, out)
	body, ok := files["vm/not-found.txt"]
	if !ok {
		t.Fatalf("bundle missing vm/not-found.txt; files=%v", supportBundleMapKeys(files))
	}
	for _, want := range []string{`no VM named "missing"`, "cove list", "cove up -user <name>"} {
		if !strings.Contains(body, want) {
			t.Fatalf("vm/not-found.txt missing %q:\n%s", want, body)
		}
	}
	if _, ok := files["vm/doctor.txt"]; ok {
		t.Fatalf("bundle included vm/doctor.txt for missing VM")
	}
	for _, cmd := range commands {
		if strings.Contains(cmd, " -vm missing") || strings.HasPrefix(cmd, "doctor -vm missing") || cmd == "trace status missing" {
			t.Fatalf("ran VM diagnostic for missing VM: %s", cmd)
		}
	}
}

func readSupportBundleFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		files[hdr.Name] = string(data)
	}
	return files
}

func supportBundleMapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mapValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
