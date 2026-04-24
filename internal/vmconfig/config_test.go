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
