package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeHardwareBounds(t *testing.T) {
	tests := []struct {
		name string
		in   hardwareBounds
		want hardwareBounds
	}{
		{
			name: "valid bounds unchanged",
			in:   hardwareBounds{MinCPU: 1, MaxCPU: 10, MinMemoryGB: 1, MaxMemoryGB: 64},
			want: hardwareBounds{MinCPU: 1, MaxCPU: 10, MinMemoryGB: 1, MaxMemoryGB: 64},
		},
		{
			name: "zero bounds fall back",
			in:   hardwareBounds{},
			want: fallbackHardwareBounds,
		},
		{
			name: "inverted max clamps to min",
			in:   hardwareBounds{MinCPU: 12, MaxCPU: 2, MinMemoryGB: 16, MaxMemoryGB: 4},
			want: hardwareBounds{MinCPU: 12, MaxCPU: 12, MinMemoryGB: 16, MaxMemoryGB: 16},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeHardwareBounds(tt.in); got != tt.want {
				t.Fatalf("normalizeHardwareBounds() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestHardwareBoundsClamp(t *testing.T) {
	b := hardwareBounds{MinCPU: 2, MaxCPU: 8, MinMemoryGB: 4, MaxMemoryGB: 32}
	cpuTests := []struct{ in, fallback, want uint }{
		{0, 6, 6},
		{1, 6, 2},
		{99, 6, 8},
		{4, 6, 4},
	}
	for _, tt := range cpuTests {
		if got := b.clampCPU(tt.in, tt.fallback); got != tt.want {
			t.Errorf("clampCPU(%d, %d) = %d, want %d", tt.in, tt.fallback, got, tt.want)
		}
	}
	memTests := []struct{ in, fallback, want uint64 }{
		{0, 16, 16},
		{1, 16, 4},
		{999, 16, 32},
		{8, 16, 8},
	}
	for _, tt := range memTests {
		if got := b.clampMemoryGB(tt.in, tt.fallback); got != tt.want {
			t.Errorf("clampMemoryGB(%d, %d) = %d, want %d", tt.in, tt.fallback, got, tt.want)
		}
	}
}

func TestLoadVMHardware(t *testing.T) {
	b := hardwareBounds{MinCPU: 1, MaxCPU: 8, MinMemoryGB: 1, MaxMemoryGB: 32}

	t.Run("saved values win", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"cpu":4,"memoryGB":16}`), 0o644); err != nil {
			t.Fatal(err)
		}
		hw, err := loadVMHardware(dir, b)
		if err != nil {
			t.Fatalf("loadVMHardware() error = %v", err)
		}
		if hw.CPU != 4 || hw.MemoryGB != 16 {
			t.Fatalf("loadVMHardware() = %+v, want cpu=4 memory=16", hw)
		}
	})

	t.Run("corrupt config reports error", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := loadVMHardware(dir, b); err == nil {
			t.Fatal("loadVMHardware() error = nil, want parse error")
		}
	})
}

func TestHardwareBoundsEditHint(t *testing.T) {
	b := hardwareBounds{MinCPU: 1, MaxCPU: 8, MinMemoryGB: 1, MaxMemoryGB: 32}

	dir := t.TempDir()
	if got, want := b.editHint(dir), b.hint(); got != want {
		t.Fatalf("editHint() = %q, want %q", got, want)
	}

	if err := os.WriteFile(suspendStatePathForVM(dir), []byte("state"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := b.editHint(dir); !strings.Contains(got, "discards the suspended session") {
		t.Fatalf("editHint() = %q, want suspend-state warning", got)
	}
}

// TestApplyVMConfigSwitchesVM covers the selector Run path: switching to
// another VM must adopt that VM's saved hardware, not keep the previous one's.
func TestApplyVMConfigSwitchesVM(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(first, "config.json"), []byte(`{"cpu":2,"memoryGB":4}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "config.json"), []byte(`{"cpu":6,"memoryGB":24}`), 0o644); err != nil {
		t.Fatal(err)
	}
	savedCPU, savedMem := cpuCount, memoryGB
	defer func() { cpuCount, memoryGB = savedCPU, savedMem }()

	applyVMConfig(first)
	if cpuCount != 2 || memoryGB != 4 {
		t.Fatalf("after first VM: cpu=%d memory=%d, want 2/4", cpuCount, memoryGB)
	}
	applyVMConfig(second)
	if cpuCount != 6 || memoryGB != 24 {
		t.Fatalf("after second VM: cpu=%d memory=%d, want 6/24", cpuCount, memoryGB)
	}
}
