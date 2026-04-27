package vmconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissing(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got == nil {
		t.Fatal("Load() = nil, want empty config")
	}
	if got.CPU != 0 || got.MemoryGB != 0 || got.Agent != nil || len(got.Volumes) != 0 {
		t.Fatalf("Load() = %#v, want empty config", got)
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	want := &Config{
		CPU:      4,
		MemoryGB: 8,
		Volumes: []VolumeMount{
			{HostPath: "/tmp/share", Tag: "share", ReadOnly: true},
		},
		PostInstallRecipes: "base",
		ParentVM:           "base-vm",
		ParentSnapshot:     "clean",
		ForkedAt:           time.Date(2026, time.April, 24, 9, 30, 0, 0, time.UTC),
		Agent: &AgentConfig{
			Platform:   "linux",
			Requested:  true,
			Verified:   true,
			VerifiedAt: time.Date(2026, time.April, 23, 12, 0, 0, 0, time.UTC),
			Source:     "runtime",
		},
	}
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatalf("Stat(config.json) error = %v", err)
	}

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.CPU != want.CPU || got.MemoryGB != want.MemoryGB || got.PostInstallRecipes != want.PostInstallRecipes {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
	if got.ParentVM != want.ParentVM || got.ParentSnapshot != want.ParentSnapshot {
		t.Fatalf("Load() lineage = parent %q snapshot %q, want parent %q snapshot %q", got.ParentVM, got.ParentSnapshot, want.ParentVM, want.ParentSnapshot)
	}
	if !got.ForkedAt.Equal(want.ForkedAt) {
		t.Fatalf("Load().ForkedAt = %v, want %v", got.ForkedAt, want.ForkedAt)
	}
	if len(got.Volumes) != 1 || got.Volumes[0].HostPath != "/tmp/share" || got.Volumes[0].Tag != "share" || !got.Volumes[0].ReadOnly {
		t.Fatalf("Load().Volumes = %#v", got.Volumes)
	}
	if got.Agent == nil || got.Agent.Platform != "linux" || !got.Agent.Requested || !got.Agent.Verified || got.Agent.Source != "runtime" {
		t.Fatalf("Load().Agent = %#v", got.Agent)
	}
	if !got.Agent.VerifiedAt.Equal(want.Agent.VerifiedAt) {
		t.Fatalf("Load().Agent.VerifiedAt = %v, want %v", got.Agent.VerifiedAt, want.Agent.VerifiedAt)
	}
}

func TestApplyHardware(t *testing.T) {
	tests := []struct {
		name         string
		cfg          Config
		current      Hardware
		explicit     HardwareExplicit
		wantHardware Hardware
		wantConfig   Config
		wantChanged  bool
	}{
		{
			name:         "use saved values by default",
			cfg:          Config{CPU: 4, MemoryGB: 8},
			current:      Hardware{CPU: 2, MemoryGB: 4},
			wantHardware: Hardware{CPU: 4, MemoryGB: 8},
			wantConfig:   Config{CPU: 4, MemoryGB: 8},
		},
		{
			name:         "persist explicit values",
			cfg:          Config{CPU: 4, MemoryGB: 8},
			current:      Hardware{CPU: 6, MemoryGB: 12},
			explicit:     HardwareExplicit{CPU: true, MemoryGB: true},
			wantHardware: Hardware{CPU: 6, MemoryGB: 12},
			wantConfig:   Config{CPU: 6, MemoryGB: 12},
			wantChanged:  true,
		},
		{
			name:         "persist only explicit cpu",
			cfg:          Config{CPU: 4, MemoryGB: 8},
			current:      Hardware{CPU: 6, MemoryGB: 12},
			explicit:     HardwareExplicit{CPU: true},
			wantHardware: Hardware{CPU: 6, MemoryGB: 8},
			wantConfig:   Config{CPU: 6, MemoryGB: 8},
			wantChanged:  true,
		},
		{
			name:         "unchanged explicit values",
			cfg:          Config{CPU: 6, MemoryGB: 12},
			current:      Hardware{CPU: 6, MemoryGB: 12},
			explicit:     HardwareExplicit{CPU: true, MemoryGB: true},
			wantHardware: Hardware{CPU: 6, MemoryGB: 12},
			wantConfig:   Config{CPU: 6, MemoryGB: 12},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			got, changed := ApplyHardware(&cfg, tt.current, tt.explicit)
			if got != tt.wantHardware {
				t.Fatalf("ApplyHardware() hardware = %#v, want %#v", got, tt.wantHardware)
			}
			if changed != tt.wantChanged {
				t.Fatalf("ApplyHardware() changed = %v, want %v", changed, tt.wantChanged)
			}
			if cfg.CPU != tt.wantConfig.CPU || cfg.MemoryGB != tt.wantConfig.MemoryGB {
				t.Fatalf("config = %#v, want %#v", cfg, tt.wantConfig)
			}
		})
	}
}

func TestSetHardware(t *testing.T) {
	dir := t.TempDir()
	changed, err := SetHardware(dir, Hardware{CPU: 4, MemoryGB: 8})
	if err != nil {
		t.Fatalf("SetHardware() error = %v", err)
	}
	if !changed {
		t.Fatal("SetHardware() changed = false, want true")
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.CPU != 4 || got.MemoryGB != 8 {
		t.Fatalf("hardware = cpu %d memory %d, want cpu 4 memory 8", got.CPU, got.MemoryGB)
	}

	changed, err = SetHardware(dir, Hardware{CPU: 4, MemoryGB: 8})
	if err != nil {
		t.Fatalf("SetHardware() error = %v", err)
	}
	if changed {
		t.Fatal("SetHardware() changed = true, want false")
	}
}

func TestSetPostInstallRecipes(t *testing.T) {
	dir := t.TempDir()
	if err := SetPostInstallRecipes(dir, "base,tools"); err != nil {
		t.Fatalf("SetPostInstallRecipes() error = %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.PostInstallRecipes != "base,tools" {
		t.Fatalf("PostInstallRecipes = %q, want base,tools", got.PostInstallRecipes)
	}
}

func TestSetVolumes(t *testing.T) {
	dir := t.TempDir()
	mounts := []VolumeMount{
		{HostPath: "/tmp/share", Tag: "share", ReadOnly: true},
	}
	if err := SetVolumes(dir, mounts); err != nil {
		t.Fatalf("SetVolumes() error = %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got.Volumes) != 1 || got.Volumes[0].HostPath != "/tmp/share" || got.Volumes[0].Tag != "share" || !got.Volumes[0].ReadOnly {
		t.Fatalf("Volumes = %#v, want %#v", got.Volumes, mounts)
	}
}
