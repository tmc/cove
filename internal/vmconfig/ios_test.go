package vmconfig

import (
	"os"
	"path/filepath"
	"testing"

	iosbundle "github.com/tmc/cove/internal/ios/bundle"
)

func TestIOSConfigPersistence(t *testing.T) {
	dir := t.TempDir()
	ios := iosbundle.DefaultConfig()
	if err := Save(dir, &Config{CPU: 8, MemoryGB: 8, IOS: &ios}); err != nil {
		t.Fatal(err)
	}
	if _, err := SetHardware(dir, Hardware{CPU: 4, MemoryGB: 16}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.IOS == nil || *got.IOS != ios || got.CPU != 4 || got.MemoryGB != 16 {
		t.Fatalf("configuration lost on hardware edit: %+v", got)
	}
}

func TestIOSDetectionPrecedesMacMarker(t *testing.T) {
	tests := []struct{ name, config, want string }{
		{"ios", `{"ios":{"schemaVersion":1,"profile":"vresearch101","variant":"regular","network":"nat","display":{"width":1290,"height":2796,"ppi":460,"scale":3}}}`, "iOS"},
		{"unknown ios schema", `{"ios":{"schemaVersion":2}}`, "unknown"},
		{"malformed config", `{"ios":`, "unknown"},
		{"mac config", `{"cpu":8}`, "macOS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string]string{"hw.model": "model", "config.json": tt.config} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := DetectOSType(dir); got != tt.want {
				t.Fatalf("DetectOSType = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRejectInvalidIOSSave(t *testing.T) {
	dir := t.TempDir()
	ios := iosbundle.DefaultConfig()
	cfg := &Config{CPU: 8, IOS: &ios}
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.IOS.SchemaVersion = 2
	if err := Save(dir, cfg); err == nil {
		t.Fatal("saved unknown ios schema")
	}
	after, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed save changed authoritative configuration")
	}
}

func TestRecipesPreserveInvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	before := []byte(`{"ios":{"schemaVersion":99},"cpu":8}`)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := SetPostInstallRecipes(dir, "test"); err == nil {
		t.Fatal("invalid config overwritten")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed mutation changed config")
	}
}
