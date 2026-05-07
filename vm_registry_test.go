package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentstate "github.com/tmc/vz-macos/internal/agent"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestGetVMPathPrefersExistingLegacyVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	legacyPath := filepath.Join(filepath.Dir(vmconfig.BaseDir()), "legacy")
	if err := os.MkdirAll(legacyPath, 0755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", legacyPath, err)
	}
	legacyPath = resolvePath(legacyPath)

	if got := vmconfig.Path("legacy"); got != legacyPath {
		t.Fatalf("GetVMPath(%q) = %q, want %q", "legacy", got, legacyPath)
	}
}

func TestVMConfigEnsureDirCreatesAliasForLegacyVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	legacyPath := filepath.Join(filepath.Dir(vmconfig.BaseDir()), "legacy")
	if err := os.MkdirAll(legacyPath, 0755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", legacyPath, err)
	}
	legacyPath = resolvePath(legacyPath)

	got, err := vmconfig.EnsureDir("legacy", vmDir)
	if err != nil {
		t.Fatalf("vmconfig.EnsureDir() error = %v", err)
	}
	if got != legacyPath {
		t.Fatalf("vmconfig.EnsureDir() = %q, want %q", got, legacyPath)
	}

	aliasPath := filepath.Join(vmconfig.BaseDir(), "legacy")
	link, err := os.Readlink(aliasPath)
	if err != nil {
		t.Fatalf("Readlink(%q) error = %v", aliasPath, err)
	}
	if link != legacyPath {
		t.Fatalf("vm alias target = %q, want %q", link, legacyPath)
	}
}

func TestVMConfigEnsureDirCreatesRegistryDirForNewVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	got, err := vmconfig.EnsureDir("fresh", vmDir)
	if err != nil {
		t.Fatalf("vmconfig.EnsureDir() error = %v", err)
	}
	want := resolvePath(filepath.Join(vmconfig.BaseDir(), "fresh"))
	if got != want {
		t.Fatalf("vmconfig.EnsureDir() = %q, want %q", got, want)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", want, err)
	}
	if !info.IsDir() {
		t.Fatalf("Stat(%q).IsDir = false, want true", want)
	}
}

func TestVMInfoState(t *testing.T) {
	t.Run("stopped", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		info, err := vmconfig.InfoFor(vmPath, detectVMState)
		if err != nil {
			t.Fatalf("vmconfig.InfoFor() error = %v", err)
		}
		if info.State != "stopped" {
			t.Fatalf("vmconfig.InfoFor().State = %q, want %q", info.State, "stopped")
		}
	})

	t.Run("suspended", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		if err := os.WriteFile(filepath.Join(vmPath, "suspend.vmstate"), []byte("state"), 0644); err != nil {
			t.Fatalf("WriteFile(suspend.vmstate) error = %v", err)
		}
		info, err := vmconfig.InfoFor(vmPath, detectVMState)
		if err != nil {
			t.Fatalf("vmconfig.InfoFor() error = %v", err)
		}
		if info.State != "suspended" {
			t.Fatalf("vmconfig.InfoFor().State = %q, want %q", info.State, "suspended")
		}
	})

	t.Run("running", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		sock := GetControlSocketPathForVM(vmPath)
		ln, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatalf("Listen(%s) error = %v", sock, err)
		}
		defer ln.Close()
		info, err := vmconfig.InfoFor(vmPath, detectVMState)
		if err != nil {
			t.Fatalf("vmconfig.InfoFor() error = %v", err)
		}
		if info.State != "running" {
			t.Fatalf("vmconfig.InfoFor().State = %q, want %q", info.State, "running")
		}
	})

	t.Run("starting while run lock held", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		lock, err := AcquireRunLock(vmPath)
		if err != nil {
			t.Fatalf("AcquireRunLock() error = %v", err)
		}
		defer lock.Release()
		if err := writeVMRuntimeState(vmPath, "starting"); err != nil {
			t.Fatalf("writeVMRuntimeState() error = %v", err)
		}
		info, err := vmconfig.InfoFor(vmPath, detectVMState)
		if err != nil {
			t.Fatalf("vmconfig.InfoFor() error = %v", err)
		}
		if info.State != "starting" {
			t.Fatalf("vmconfig.InfoFor().State = %q, want %q", info.State, "starting")
		}
	})

	t.Run("starting while run lock held before runtime state", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		lock, err := AcquireRunLock(vmPath)
		if err != nil {
			t.Fatalf("AcquireRunLock() error = %v", err)
		}
		defer lock.Release()
		info, err := vmconfig.InfoFor(vmPath, detectVMState)
		if err != nil {
			t.Fatalf("vmconfig.InfoFor() error = %v", err)
		}
		if info.State != "starting" {
			t.Fatalf("vmconfig.InfoFor().State = %q, want %q", info.State, "starting")
		}
	})

	t.Run("stale runtime state ignored without run lock", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		if err := writeVMRuntimeState(vmPath, "starting"); err != nil {
			t.Fatalf("writeVMRuntimeState() error = %v", err)
		}
		info, err := vmconfig.InfoFor(vmPath, detectVMState)
		if err != nil {
			t.Fatalf("vmconfig.InfoFor() error = %v", err)
		}
		if info.State != "stopped" {
			t.Fatalf("vmconfig.InfoFor().State = %q, want %q", info.State, "stopped")
		}
	})

	t.Run("dead runtime pid ignored", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		if err := writeVMRuntimeStateForTest(vmPath, vmRuntimeState{
			State:       "starting",
			Phase:       "configuring",
			PID:         999999,
			UpdatedAt:   time.Now().UTC(),
			LastPhaseAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("writeVMRuntimeStateForTest() error = %v", err)
		}
		info, err := vmconfig.InfoFor(vmPath, detectVMState)
		if err != nil {
			t.Fatalf("vmconfig.InfoFor() error = %v", err)
		}
		if info.State != "stopped" {
			t.Fatalf("vmconfig.InfoFor().State = %q, want %q", info.State, "stopped")
		}
	})
}

func TestRuntimeListFieldsStarting(t *testing.T) {
	vmPath := makeTestVMDir(t)
	when := time.Now().Add(-23 * time.Second).UTC()
	if err := writeVMRuntimeStateForTest(vmPath, vmRuntimeState{
		State:       "starting",
		Phase:       "configuring",
		PID:         os.Getpid(),
		UpdatedAt:   when,
		LastPhaseAt: when,
	}); err != nil {
		t.Fatalf("writeVMRuntimeStateForTest() error = %v", err)
	}
	uptime, note := runtimeListFields(vmPath, "starting")
	if uptime == "-" {
		t.Fatal("uptime = -, want duration")
	}
	if note != fmt.Sprintf("configuring (pid=%d)", os.Getpid()) {
		t.Fatalf("note = %q, want configuring pid note", note)
	}
}

func writeVMRuntimeStateForTest(vmPath string, rt vmRuntimeState) error {
	data, err := json.Marshal(rt)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(vmPath, vmRuntimeStateFile), append(data, '\n'), 0644)
}

func makeTestVMDir(t *testing.T) string {
	t.Helper()

	vmPath := t.TempDir()
	for _, name := range []string{"disk.img", "aux.img"} {
		if err := os.WriteFile(filepath.Join(vmPath, name), []byte(name), 0644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	return vmPath
}

func TestVMConfigRoundTripAgentState(t *testing.T) {
	vmPath := makeTestVMDir(t)
	want := &vmconfig.Config{
		CPU:      4,
		MemoryGB: 8,
		Agent: &vmconfig.AgentConfig{
			Platform:   agentstate.PlatformLinux,
			Requested:  true,
			Verified:   true,
			VerifiedAt: time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC),
			Source:     agentstate.SourceRuntime,
		},
	}
	if err := vmconfig.Save(vmPath, want); err != nil {
		t.Fatalf("vmconfig.Save() error = %v", err)
	}
	got, err := vmconfig.Load(vmPath)
	if err != nil {
		t.Fatalf("vmconfig.Load() error = %v", err)
	}
	if got.Agent == nil {
		t.Fatal("vmconfig.Load().Agent = nil, want value")
	}
	if got.Agent.Platform != want.Agent.Platform || !got.Agent.Requested || !got.Agent.Verified || got.Agent.Source != want.Agent.Source {
		t.Fatalf("vmconfig.Load().Agent = %#v, want %#v", got.Agent, want.Agent)
	}
	if !got.Agent.VerifiedAt.Equal(want.Agent.VerifiedAt) {
		t.Fatalf("vmconfig.Load().Agent.VerifiedAt = %v, want %v", got.Agent.VerifiedAt, want.Agent.VerifiedAt)
	}
}
