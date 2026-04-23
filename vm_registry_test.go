package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetVMPathPrefersExistingLegacyVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	legacyPath := filepath.Join(filepath.Dir(GetVMBaseDir()), "legacy")
	if err := os.MkdirAll(legacyPath, 0755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", legacyPath, err)
	}
	legacyPath = resolvePath(legacyPath)

	if got := GetVMPath("legacy"); got != legacyPath {
		t.Fatalf("GetVMPath(%q) = %q, want %q", "legacy", got, legacyPath)
	}
}

func TestEnsureVMDirCreatesAliasForLegacyVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	legacyPath := filepath.Join(filepath.Dir(GetVMBaseDir()), "legacy")
	if err := os.MkdirAll(legacyPath, 0755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", legacyPath, err)
	}
	legacyPath = resolvePath(legacyPath)

	got, err := EnsureVMDir("legacy")
	if err != nil {
		t.Fatalf("EnsureVMDir() error = %v", err)
	}
	if got != legacyPath {
		t.Fatalf("EnsureVMDir() = %q, want %q", got, legacyPath)
	}

	aliasPath := filepath.Join(GetVMBaseDir(), "legacy")
	link, err := os.Readlink(aliasPath)
	if err != nil {
		t.Fatalf("Readlink(%q) error = %v", aliasPath, err)
	}
	if link != legacyPath {
		t.Fatalf("vm alias target = %q, want %q", link, legacyPath)
	}
}

func TestEnsureVMDirCreatesRegistryDirForNewVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	got, err := EnsureVMDir("fresh")
	if err != nil {
		t.Fatalf("EnsureVMDir() error = %v", err)
	}
	want := resolvePath(filepath.Join(GetVMBaseDir(), "fresh"))
	if got != want {
		t.Fatalf("EnsureVMDir() = %q, want %q", got, want)
	}
	info, err := os.Stat(want)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", want, err)
	}
	if !info.IsDir() {
		t.Fatalf("Stat(%q).IsDir = false, want true", want)
	}
}

func TestGetVMInfoState(t *testing.T) {
	t.Run("stopped", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		info, err := GetVMInfo(vmPath)
		if err != nil {
			t.Fatalf("GetVMInfo() error = %v", err)
		}
		if info.State != "stopped" {
			t.Fatalf("GetVMInfo().State = %q, want %q", info.State, "stopped")
		}
	})

	t.Run("suspended", func(t *testing.T) {
		vmPath := makeTestVMDir(t)
		if err := os.WriteFile(filepath.Join(vmPath, "suspend.vmstate"), []byte("state"), 0644); err != nil {
			t.Fatalf("WriteFile(suspend.vmstate) error = %v", err)
		}
		info, err := GetVMInfo(vmPath)
		if err != nil {
			t.Fatalf("GetVMInfo() error = %v", err)
		}
		if info.State != "suspended" {
			t.Fatalf("GetVMInfo().State = %q, want %q", info.State, "suspended")
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
		info, err := GetVMInfo(vmPath)
		if err != nil {
			t.Fatalf("GetVMInfo() error = %v", err)
		}
		if info.State != "running" {
			t.Fatalf("GetVMInfo().State = %q, want %q", info.State, "running")
		}
	})
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
	want := &VMConfig{
		CPU:      4,
		MemoryGB: 8,
		Agent: &VMAgentConfig{
			Platform:   vmAgentPlatformLinux,
			Requested:  true,
			Verified:   true,
			VerifiedAt: time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC),
			Source:     vmAgentSourceRuntime,
		},
	}
	if err := SaveVMConfig(vmPath, want); err != nil {
		t.Fatalf("SaveVMConfig() error = %v", err)
	}
	got, err := LoadVMConfig(vmPath)
	if err != nil {
		t.Fatalf("LoadVMConfig() error = %v", err)
	}
	if got.Agent == nil {
		t.Fatal("LoadVMConfig().Agent = nil, want value")
	}
	if got.Agent.Platform != want.Agent.Platform || !got.Agent.Requested || !got.Agent.Verified || got.Agent.Source != want.Agent.Source {
		t.Fatalf("LoadVMConfig().Agent = %#v, want %#v", got.Agent, want.Agent)
	}
	if !got.Agent.VerifiedAt.Equal(want.Agent.VerifiedAt) {
		t.Fatalf("LoadVMConfig().Agent.VerifiedAt = %v, want %v", got.Agent.VerifiedAt, want.Agent.VerifiedAt)
	}
}
