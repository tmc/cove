package main

import "testing"

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
