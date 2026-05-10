package main

import (
	"io"
	"os"
	"strings"
	"testing"
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
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		if strings.Contains(line, "[os: darwin]") {
			t.Errorf("linux filter leaked darwin entry: %q", line)
		}
	}
}
