package sckit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseBackend(t *testing.T) {
	tests := []struct {
		in   string
		want Backend
	}{
		{"sckit", BackendSCKit},
		{"SCKIT", BackendSCKit},
		{"  sckit\n", BackendSCKit},
		{"cgwindow", BackendCGWindow},
		{"auto", BackendCGWindow}, // Slice 3: auto resolves to cgwindow
		{"", BackendCGWindow},
		{"bogus", BackendCGWindow},
	}
	for _, tt := range tests {
		if got := ParseBackend(tt.in); got != tt.want {
			t.Errorf("ParseBackend(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBackendForVMDirEnvOnly(t *testing.T) {
	t.Setenv("COVE_CAPTURE_BACKEND", "sckit")
	if got := BackendForVMDir(""); got != BackendSCKit {
		t.Errorf("BackendForVMDir(\"\") = %q, want sckit", got)
	}
	t.Setenv("COVE_CAPTURE_BACKEND", "")
	if got := BackendForVMDir(""); got != BackendCGWindow {
		t.Errorf("BackendForVMDir(\"\") with empty env = %q, want cgwindow", got)
	}
}

func TestBackendForVMDirPerVMFileWinsOverEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "capture-backend"), []byte("sckit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COVE_CAPTURE_BACKEND", "cgwindow")
	if got := BackendForVMDir(dir); got != BackendSCKit {
		t.Errorf("BackendForVMDir = %q, want sckit (per-VM file wins)", got)
	}
}

func TestBackendForVMDirUnknownFileFallsThroughToEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "capture-backend"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COVE_CAPTURE_BACKEND", "sckit")
	if got := BackendForVMDir(dir); got != BackendSCKit {
		t.Errorf("BackendForVMDir = %q, want sckit (unknown file -> env)", got)
	}
}

func TestBackendForVMDirMissingFileFallsThroughToEnv(t *testing.T) {
	dir := t.TempDir() // no capture-backend file
	t.Setenv("COVE_CAPTURE_BACKEND", "sckit")
	if got := BackendForVMDir(dir); got != BackendSCKit {
		t.Errorf("BackendForVMDir = %q, want sckit (missing file -> env)", got)
	}
}

func TestBackendForVMDirEnvGarbageFallsToDefault(t *testing.T) {
	t.Setenv("COVE_CAPTURE_BACKEND", "totally-unknown")
	if got := BackendForVMDir(""); got != BackendCGWindow {
		t.Errorf("BackendForVMDir = %q, want cgwindow (garbage env -> default)", got)
	}
}

func TestBackendForVMDirEnvUppercaseAndWhitespace(t *testing.T) {
	tests := []struct {
		env  string
		want Backend
	}{
		{"  SCKit\n", BackendSCKit},
		{"\tSCKIT\t", BackendSCKit},
		{"  CGWINDOW ", BackendCGWindow},
		{"  AUTO ", BackendCGWindow},
	}
	for _, tt := range tests {
		t.Setenv("COVE_CAPTURE_BACKEND", tt.env)
		if got := BackendForVMDir(""); got != tt.want {
			t.Errorf("env=%q BackendForVMDir = %q, want %q", tt.env, got, tt.want)
		}
	}
}

func TestBackendForVMDirEmptyPerVMFileFallsThroughToEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "capture-backend"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COVE_CAPTURE_BACKEND", "sckit")
	if got := BackendForVMDir(dir); got != BackendSCKit {
		t.Errorf("BackendForVMDir = %q, want sckit (empty file -> env)", got)
	}
}

func TestBackendForVMDirPerVMFileUppercaseWithNewline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "capture-backend"), []byte("SCKIT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COVE_CAPTURE_BACKEND", "")
	if got := BackendForVMDir(dir); got != BackendSCKit {
		t.Errorf("BackendForVMDir = %q, want sckit (uppercase per-VM file)", got)
	}
}

func TestBackendForVMDirPerVMCGWindowWinsOverSCKitEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "capture-backend"), []byte("cgwindow"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COVE_CAPTURE_BACKEND", "sckit")
	if got := BackendForVMDir(dir); got != BackendCGWindow {
		t.Errorf("BackendForVMDir = %q, want cgwindow (per-VM cgwindow overrides env sckit)", got)
	}
}
