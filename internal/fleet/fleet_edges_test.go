package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPathMissingReturnsEmpty(t *testing.T) {
	cfg, err := LoadPath(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("LoadPath missing: %v", err)
	}
	if cfg == nil || cfg.Remotes == nil {
		t.Fatalf("expected non-nil empty config, got %#v", cfg)
	}
	if len(cfg.Remotes) != 0 {
		t.Fatalf("expected empty Remotes, got %d", len(cfg.Remotes))
	}
}

func TestLoadPathInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := LoadPath(path)
	if err == nil || !strings.Contains(err.Error(), "parse fleet config") {
		t.Fatalf("LoadPath bad json err = %v", err)
	}
}

func TestSavePathNilCreatesEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "fleet.json")
	if err := SavePath(path, nil); err != nil {
		t.Fatalf("SavePath nil: %v", err)
	}
	got, err := LoadPath(path)
	if err != nil {
		t.Fatalf("LoadPath: %v", err)
	}
	if len(got.Remotes) != 0 {
		t.Fatalf("expected empty, got %#v", got.Remotes)
	}
}

func TestConfigAddValidation(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		remote Remote
		want   string
	}{
		{"empty name", "", Remote{Host: "h"}, "name required"},
		{"slash in name", "a/b", Remote{Host: "h"}, "invalid fleet name"},
		{"empty host", "a", Remote{Host: "  "}, "host required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{}
			err := cfg.Add(tt.key, tt.remote)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Add err = %v, want contains %q", err, tt.want)
			}
		})
	}
}

func TestConfigRemoveAndGetNil(t *testing.T) {
	var nilCfg *Config
	if _, ok := nilCfg.Get("x"); ok {
		t.Fatal("Get on nil config returned ok")
	}
	if err := nilCfg.Remove("x"); err == nil {
		t.Fatal("Remove on nil config returned nil err")
	}

	cfg := &Config{}
	if err := cfg.Remove("missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Remove missing err = %v", err)
	}
	if _, ok := cfg.Get("missing"); ok {
		t.Fatal("Get missing returned ok")
	}
}

func TestConfigListEmpty(t *testing.T) {
	if got := (&Config{}).List(); got != nil {
		t.Fatalf("List on empty = %#v, want nil", got)
	}
	var nilCfg *Config
	if got := nilCfg.List(); got != nil {
		t.Fatalf("List on nil = %#v, want nil", got)
	}
}

func TestParseTargetErrors(t *testing.T) {
	for _, in := range []string{"", "   "} {
		if _, err := ParseTarget(in); err == nil {
			t.Fatalf("ParseTarget(%q) returned nil err", in)
		}
	}
	if _, err := ParseTarget("user@   "); err == nil {
		t.Fatal("ParseTarget(user@<spaces>) returned nil err")
	}
}
