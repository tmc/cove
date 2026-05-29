package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func captureVZScriptStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	done := make(chan struct{})
	var buf strings.Builder
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	runErr := fn()
	w.Close()
	<-done
	r.Close()
	return buf.String(), runErr
}

func TestVzscriptListByOSFilter(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no filter lists all", args: nil},
		{name: "darwin filter", args: []string{"-os", "darwin"}},
		{name: "linux filter", args: []string{"-os", "linux"}},
		{name: "macos alias", args: []string{"-os", "macos"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := captureVZScriptStdout(t, func() error { return vzscriptList(tt.args) })
			if err != nil {
				t.Fatalf("vzscriptList(%v) err = %v", tt.args, err)
			}
			if !strings.Contains(out, "Built-in recipes:") {
				t.Fatalf("output missing header: %q", out)
			}
		})
	}
}

func TestVzscriptListRejectsInvalidOS(t *testing.T) {
	err := vzscriptList([]string{"-os", "windows"})
	if err == nil || !strings.Contains(err.Error(), "invalid guest OS") {
		t.Fatalf("err = %v, want invalid guest OS", err)
	}
}

func TestVzscriptListVMFilterDoesNotCreateMissingVMDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missing := "missing-vzscript-list-vm"
	err := vzscriptList([]string{"-vm", missing})
	if err == nil {
		t.Fatal("vzscriptList missing VM succeeded; want error")
	}
	if !strings.Contains(err.Error(), `no VM named "missing-vzscript-list-vm"`) {
		t.Fatalf("err = %v, want missing VM diagnostic", err)
	}
	if _, statErr := os.Stat(filepath.Join(vmconfig.BaseDir(), missing)); !os.IsNotExist(statErr) {
		t.Fatalf("missing VM dir stat = %v, want not exist", statErr)
	}
}

func TestOpenVZScriptLogSkipsMissingVMDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	missingDir := filepath.Join(vmconfig.BaseDir(), "missing-vzscript-log")
	f, err := openVZScriptLog(filepath.Join(missingDir, "control.sock"))
	if err != nil {
		t.Fatalf("openVZScriptLog missing dir: %v", err)
	}
	if f != nil {
		t.Fatal("openVZScriptLog returned file for missing dir")
	}
	if _, statErr := os.Stat(missingDir); !os.IsNotExist(statErr) {
		t.Fatalf("missing log VM dir stat = %v, want not exist", statErr)
	}
}

func TestVzscriptListWithGuestOSEmptyFilter(t *testing.T) {
	out, err := captureVZScriptStdout(t, func() error { return vzscriptListWithGuestOS("") })
	if err != nil {
		t.Fatalf("vzscriptListWithGuestOS: %v", err)
	}
	if !strings.HasPrefix(out, "Built-in recipes:") {
		t.Fatalf("output missing header: %q", out)
	}
}

func TestVzscriptListWithGuestOSLinuxFilter(t *testing.T) {
	out, err := captureVZScriptStdout(t, func() error { return vzscriptListWithGuestOS("linux") })
	if err != nil {
		t.Fatalf("vzscriptListWithGuestOS: %v", err)
	}
	if !strings.Contains(out, "kvm-test") {
		t.Fatalf("linux filter missing kvm-test:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		if strings.Contains(line, "[os: darwin]") {
			t.Errorf("linux filter leaked darwin entry: %q", line)
		}
	}
}
