package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/vz-macos/internal/ociimage"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestParseLumeMemory(t *testing.T) {
	tests := []struct {
		in   string
		want uint64
		ok   bool
	}{
		{"4G", 4, true},
		{"4GB", 4, true},
		{"8g", 8, true},
		{"16Gb", 16, true},
		{"4096M", 4, true},
		{"4096MB", 4, true},
		{"8192m", 8, true},
		{"1024K", 0, false},          // sub-GB rounds to zero, treat as not-ok
		{"1048576K", 1, true},        // 1 GiB exactly in KiB
		{"4294967296", 4, true},      // bare bytes — exactly 4 GiB
		{"4294967296B", 4, true},     // explicit byte suffix
		{"1T", 1024, true},           // 1 TiB
		{"", 0, false},
		{"0G", 0, false},
		{"abc", 0, false},
		{"4X", 0, false},             // unknown suffix
		{"4 GB", 4, true},            // trailing whitespace before suffix
		{" 4G ", 4, true},            // outer whitespace
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := parseLumeMemory(tc.in)
			if ok != tc.ok {
				t.Fatalf("parseLumeMemory(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("parseLumeMemory(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseLumeMemoryExactGiB(t *testing.T) {
	// 1048576 KiB == 1 GiB exactly. Confirm the bare-K path lands on 1.
	got, ok := parseLumeMemory("1048576K")
	if !ok || got != 1 {
		t.Fatalf("parseLumeMemory(1048576K) = %d, %v; want 1, true", got, ok)
	}
}

func TestLumeWriteCoveConfigMapsCPUAndMemory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lume-config.json"), []byte(`{
		"os": "macos",
		"cpu": 6,
		"memory": "8G",
		"machineIdentifier": "abc",
		"hardwareModel": "model",
		"macAddress": "aa:bb:cc:dd:ee:ff"
	}`), 0644); err != nil {
		t.Fatal(err)
	}
	plan := &pullPlan{VMDir: dir}
	if err := lumeWriteCoveConfig(plan); err != nil {
		t.Fatalf("lumeWriteCoveConfig: %v", err)
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		t.Fatalf("load cove config: %v", err)
	}
	if cfg.CPU != 6 {
		t.Errorf("cfg.CPU = %d, want 6", cfg.CPU)
	}
	if cfg.MemoryGB != 8 {
		t.Errorf("cfg.MemoryGB = %d, want 8", cfg.MemoryGB)
	}
}

func TestLumeWriteCoveConfigMissingFileIsNoOp(t *testing.T) {
	dir := t.TempDir()
	plan := &pullPlan{VMDir: dir}
	if err := lumeWriteCoveConfig(plan); err != nil {
		t.Fatalf("lumeWriteCoveConfig with no sidecar: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); !os.IsNotExist(err) {
		t.Errorf("expected no config.json, got err=%v", err)
	}
}

func TestLumeWriteCoveConfigPreservesUnrelatedFields(t *testing.T) {
	dir := t.TempDir()
	// Pre-existing cove config with a recipes string.
	if err := vmconfig.Save(dir, &vmconfig.Config{
		PostInstallRecipes: "homebrew",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lume-config.json"), []byte(`{
		"cpu": 4,
		"memory": "4G"
	}`), 0644); err != nil {
		t.Fatal(err)
	}
	plan := &pullPlan{VMDir: dir}
	if err := lumeWriteCoveConfig(plan); err != nil {
		t.Fatalf("lumeWriteCoveConfig: %v", err)
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		t.Fatalf("load cove config: %v", err)
	}
	if cfg.CPU != 4 || cfg.MemoryGB != 4 {
		t.Errorf("hardware not mapped: cpu=%d mem=%d", cfg.CPU, cfg.MemoryGB)
	}
	if cfg.PostInstallRecipes != "homebrew" {
		t.Errorf("PostInstallRecipes lost: got %q", cfg.PostInstallRecipes)
	}
}

func TestLumeWriteCoveConfigSkipsUnparseableMemory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lume-config.json"), []byte(`{
		"cpu": 2,
		"memory": "garbage"
	}`), 0644); err != nil {
		t.Fatal(err)
	}
	plan := &pullPlan{VMDir: dir}
	if err := lumeWriteCoveConfig(plan); err != nil {
		t.Fatalf("lumeWriteCoveConfig: %v", err)
	}
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		t.Fatalf("load cove config: %v", err)
	}
	if cfg.CPU != 2 {
		t.Errorf("cfg.CPU = %d, want 2", cfg.CPU)
	}
	if cfg.MemoryGB != 0 {
		t.Errorf("cfg.MemoryGB = %d, want 0 (unparseable input)", cfg.MemoryGB)
	}
}

func TestPullPlanDispatchesLumeFormat(t *testing.T) {
	// Sanity: handlePull's switch should route a FormatLume manifest into
	// lumePullDisk. Rather than re-running handlePull (which wants a
	// network), exercise the format constant identity that pull.go relies
	// on. If the enum drifts, this test breaks loudly.
	if ociimage.FormatLume == ociimage.FormatCove {
		t.Fatal("FormatLume must differ from FormatCove")
	}
}
