package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestPlatform(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  string
	}{
		{
			name: "detect macos",
			want: PlatformMacOS,
		},
		{
			name: "detect linux disk",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), nil, 0644); err != nil {
					t.Fatal(err)
				}
			},
			want: PlatformLinux,
		},
		{
			name: "persisted",
			setup: func(t *testing.T, dir string) {
				t.Helper()
				if err := vmconfig.Save(dir, &vmconfig.Config{Agent: &vmconfig.AgentConfig{Platform: "custom"}}); err != nil {
					t.Fatal(err)
				}
			},
			want: "custom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, dir)
			}
			if got := Platform(dir); got != tt.want {
				t.Fatalf("Platform() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetRequested(t *testing.T) {
	dir := t.TempDir()
	if err := SetRequested(dir, PlatformLinux, true, SourceInstall); err != nil {
		t.Fatalf("SetRequested() error = %v", err)
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Agent == nil || cfg.Agent.Platform != PlatformLinux || !cfg.Agent.Requested || cfg.Agent.Source != SourceInstall {
		t.Fatalf("Load().Agent = %#v", cfg.Agent)
	}
}

func TestSetRequestedClearsEmptyAgent(t *testing.T) {
	dir := t.TempDir()
	if err := SetRequested(dir, "", false, ""); err != nil {
		t.Fatalf("SetRequested() error = %v", err)
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Agent != nil {
		t.Fatalf("Load().Agent = %#v, want nil", cfg.Agent)
	}
}

func TestMarkVerified(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2026, 4, 23, 14, 0, 0, 0, time.FixedZone("test", -7*60*60))
	if err := MarkVerified(dir, PlatformMacOS, SourceRuntime, when); err != nil {
		t.Fatalf("MarkVerified() error = %v", err)
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Agent == nil || cfg.Agent.Platform != PlatformMacOS || !cfg.Agent.Verified || cfg.Agent.Source != SourceRuntime {
		t.Fatalf("Load().Agent = %#v", cfg.Agent)
	}
	if !cfg.Agent.VerifiedAt.Equal(when.UTC()) {
		t.Fatalf("VerifiedAt = %v, want %v", cfg.Agent.VerifiedAt, when.UTC())
	}
}
