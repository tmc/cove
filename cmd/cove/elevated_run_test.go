package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestManualElevationGuidanceDoesNotRetainManifest(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	out := captureStderrString(t, func() { printManualElevationGuidance("Install the guest agent.") })
	for _, want := range []string{"rerun the full cove command", "recreates temporary files and mounts", "sudo cove helper install", "COVE_USE_HELPER=1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
	for _, stale := range []string{elevatedOpArg, "cove-elev-manifest-", "Advanced manual recovery"} {
		if strings.Contains(out, stale) {
			t.Fatalf("advertised stale recovery %q: %s", stale, out)
		}
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("guidance left temporary files: %v", entries)
	}
}

func TestRestrictedElevationLeavesNoRecoveryManifest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires unprivileged process")
	}
	t.Setenv(appleAppSandboxContainerEnv, "")
	t.Setenv("COVE_USE_HELPER", "")
	t.Setenv("COVE_FORCE_MANUAL_ELEVATION", "1")
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	old := runElevatedManifestNativeHook
	defer func() { runElevatedManifestNativeHook = old }()
	runElevatedManifestNativeHook = func(string, string, string) error { t.Fatal("restricted path called native authorization"); return nil }
	out := captureStderrString(t, func() {
		err := runElevatedOnCurrentThread(&elevatedManifest{MkdirAll: []string{"/Library/CoveTest"}}, "Install test files.")
		if !errors.Is(err, errRestrictedNoElevation) {
			t.Fatalf("error = %v", err)
		}
	})
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("restricted elevation retained files: %v", entries)
	}
	if strings.Contains(out, elevatedOpArg) {
		t.Fatalf("advertised low-level recovery: %s", out)
	}
	if !strings.Contains(out, "COVE_USE_HELPER=1") {
		t.Fatalf("missing helper opt-in: %s", out)
	}
}

func TestNativeElevationNoTTYCleansManifest(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires unprivileged process")
	}
	for _, name := range []string{appleAppSandboxContainerEnv, "COVE_USE_HELPER", "CLAUDECODE", "IS_SANDBOX", "COVE_FORCE_MANUAL_ELEVATION"} {
		t.Setenv(name, "")
	}
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	old := runElevatedManifestNativeHook
	defer func() { runElevatedManifestNativeHook = old }()
	var manifestPath string
	runElevatedManifestNativeHook = func(path, hash, prompt string) error {
		manifestPath = path
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == 0 || hash == "" {
			t.Fatal("native child missing authenticated manifest")
		}
		return ErrAuthorizationNoTTY
	}
	out := captureStderrString(t, func() {
		err := runElevatedOnCurrentThread(&elevatedManifest{MkdirAll: []string{"/Library/CoveTest"}}, "Install test files.")
		if !errors.Is(err, ErrAuthorizationNoTTY) {
			t.Fatalf("error = %v", err)
		}
	})
	if manifestPath == "" {
		t.Fatal("native path not called")
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("manifest retained: %v", err)
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed elevation retained files: %v", entries)
	}
	if strings.Contains(out, elevatedOpArg) || strings.Contains(out, manifestPath) {
		t.Fatalf("output advertises discarded manifest: %s", out)
	}
	if !strings.Contains(out, "rerun the full cove command") {
		t.Fatalf("missing supported recovery: %s", out)
	}
}

func TestRunElevatedRefusesAppleAppSandbox(t *testing.T) {
	t.Setenv(appleAppSandboxContainerEnv, "com.tmc.cove")
	t.Setenv("COVE_USE_HELPER", "1")

	oldHook := runElevatedManifestNativeHook
	t.Cleanup(func() { runElevatedManifestNativeHook = oldHook })
	runElevatedManifestNativeHook = func(string, string, string) error {
		t.Fatal("native elevation called from app sandbox")
		return nil
	}

	out := captureStderrString(t, func() {
		err := runElevatedOnCurrentThread(&elevatedManifest{MkdirAll: []string{"/Library/CoveTest"}}, "Need root.")
		if !errors.Is(err, errAppSandboxNoElevation) {
			t.Fatalf("runElevatedOnCurrentThread error = %v, want errAppSandboxNoElevation", err)
		}
	})
	for _, notWant := range []string{
		"sudo cove helper install",
		elevatedOpArg,
		"Advanced manual recovery",
	} {
		if strings.Contains(out, notWant) {
			t.Fatalf("app sandbox elevation wrote %q to stderr:\n%s", notWant, out)
		}
	}
}

func captureStderrString(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = old
	return <-done
}
