package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// withTempHome points $HOME at a fresh temp dir BEFORE any code can call
// vmconfig.BaseDir() / RunsDir(). The vz-macos memory file flags this as
// the v0.1 smoke-test blocker — never touch ~/.vz/ from tests.
func withTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	return tmp
}

func TestRunBundle_LazyCreate(t *testing.T) {
	tmp := withTempHome(t)
	runsRoot := filepath.Join(tmp, ".vz", "runs")

	b, err := NewRunBundle(runsRoot, "vm-eph-1", "base-vm")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	if b.ID() == "" || len(b.ID()) != 8 {
		t.Fatalf("run id %q: want 8 hex chars", b.ID())
	}

	// No events yet — directory must NOT exist.
	if _, err := os.Stat(b.Dir()); !os.IsNotExist(err) {
		t.Fatalf("bundle dir exists before any event (err=%v); want NotExist", err)
	}

	// Finalize without writing — still no dir, no manifest.
	if err := b.Finalize(nil); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if _, err := os.Stat(b.Dir()); !os.IsNotExist(err) {
		t.Fatalf("bundle dir exists after empty finalize; want NotExist")
	}
}

func TestRunBundle_ManifestRoundTrip(t *testing.T) {
	tmp := withTempHome(t)
	runsRoot := filepath.Join(tmp, ".vz", "runs")

	b, err := NewRunBundle(runsRoot, "vm-x", "image:foo@sha256:abc")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	if err := b.AppendEvent(map[string]any{"event": "hello"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := b.Finalize(errors.New("boot timeout")); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	manifestPath := filepath.Join(b.Dir(), "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var got runManifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RunID != b.ID() {
		t.Errorf("RunID = %q, want %q", got.RunID, b.ID())
	}
	if got.VMName != "vm-x" {
		t.Errorf("VMName = %q, want vm-x", got.VMName)
	}
	if got.ForkFrom != "image:foo@sha256:abc" {
		t.Errorf("ForkFrom = %q, want image:foo@sha256:abc", got.ForkFrom)
	}
	if got.ExitStatus != "boot timeout" {
		t.Errorf("ExitStatus = %q, want boot timeout", got.ExitStatus)
	}
	if got.StartedAt == "" || got.EndedAt == "" {
		t.Errorf("timestamps empty: started=%q ended=%q", got.StartedAt, got.EndedAt)
	}
}

func TestRunBundle_AtomicWrite(t *testing.T) {
	tmp := withTempHome(t)
	runsRoot := filepath.Join(tmp, ".vz", "runs")
	b, err := NewRunBundle(runsRoot, "vm-x", "base")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	if err := b.AppendEvent(map[string]any{"event": "trigger-mkdir"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := b.Finalize(nil); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	entries, err := os.ReadDir(b.Dir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file leaked into bundle dir: %s", e.Name())
		}
	}

	// Direct atomicWriteFile call: ensure no partial-write window.
	target := filepath.Join(b.Dir(), "atomic.txt")
	if _, err := atomicWriteFile(target, []byte("payload"), 0644); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("contents = %q, want payload", string(got))
	}
}

func TestRunBundle_ScreenshotRecord(t *testing.T) {
	tmp := withTempHome(t)
	runsRoot := filepath.Join(tmp, ".vz", "runs")
	b, err := NewRunBundle(runsRoot, "vm-x", "base")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	if err := b.RecordScreenshot("step_01", []byte{0x89, 0x50, 0x4E, 0x47}); err != nil {
		t.Fatalf("RecordScreenshot: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(b.ScreenshotsDir(), "step_01.png"))
	if err != nil {
		t.Fatalf("read screenshot: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("len = %d, want 4", len(got))
	}
}

// TestRunBundle_OnlyForkFrom verifies that runVMWithConfig only initializes
// a bundle for fork-from runs. We swap the runs hook to a temp dir and
// stub the run hooks so the call returns immediately without booting.
func TestRunBundle_OnlyForkFrom(t *testing.T) {
	withTempHome(t)
	runsRoot := t.TempDir()
	prev := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() { runsDirHook = prev })

	// Stub the runtime hooks so runVMWithConfig short-circuits without
	// touching the Virtualization framework or grabbing a real run.lock.
	stubAcquireRunLockHook(t)
	prevMac := runMacOSVMHook
	t.Cleanup(func() { runMacOSVMHook = prevMac })
	runMacOSVMHook = func() error { return nil }

	// Plain run with no fork-from must not activate the run bundle; it may
	// still create a metrics-only run directory.
	cfg := RunConfig{VM: vmSelection{Name: "vm-plain", Directory: t.TempDir()}}
	if err := runVMWithConfig(cfg); err != nil {
		t.Fatalf("plain run: %v", err)
	}
	if ActiveRunBundle() != nil {
		t.Fatalf("plain run left an active bundle behind")
	}
}

// TestRunsDir_FromHome confirms vmconfig.RunsDir composes from $HOME.
func TestRunsDir_FromHome(t *testing.T) {
	t.Setenv("HOME", "/tmp/fake-home")
	want := filepath.Join("/tmp/fake-home", ".vz", "runs")
	if got := vmconfig.RunsDir(); got != want {
		t.Errorf("RunsDir() = %q, want %q", got, want)
	}
}
