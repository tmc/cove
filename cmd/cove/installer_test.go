package main

import "testing"

func TestClampInstallCPUCount(t *testing.T) {
	tests := []struct {
		name         string
		requested    uint
		frameworkMin uint
		frameworkMax uint
		restoreMin   uint
		want         uint
	}{
		{
			name:         "keeps requested when already valid",
			requested:    6,
			frameworkMin: 1,
			frameworkMax: 8,
			restoreMin:   4,
			want:         6,
		},
		{
			name:         "raises to restore minimum",
			requested:    2,
			frameworkMin: 1,
			frameworkMax: 8,
			restoreMin:   4,
			want:         4,
		},
		{
			name:         "raises to framework minimum",
			requested:    1,
			frameworkMin: 2,
			frameworkMax: 8,
			restoreMin:   1,
			want:         2,
		},
		{
			name:         "caps at framework maximum",
			requested:    16,
			frameworkMin: 1,
			frameworkMax: 8,
			restoreMin:   4,
			want:         8,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampInstallCPUCount(tt.requested, tt.frameworkMin, tt.frameworkMax, tt.restoreMin)
			if got != tt.want {
				t.Fatalf("clampInstallCPUCount(%d, %d, %d, %d) = %d, want %d",
					tt.requested, tt.frameworkMin, tt.frameworkMax, tt.restoreMin, got, tt.want)
			}
		})
	}
}

func TestClampInstallMemorySize(t *testing.T) {
	gb := uint64(1024 * 1024 * 1024)
	tests := []struct {
		name         string
		requested    uint64
		frameworkMin uint64
		frameworkMax uint64
		restoreMin   uint64
		want         uint64
	}{
		{
			name:         "keeps requested when already valid",
			requested:    8 * gb,
			frameworkMin: 4 * gb,
			frameworkMax: 64 * gb,
			restoreMin:   6 * gb,
			want:         8 * gb,
		},
		{
			name:         "raises to restore minimum",
			requested:    4 * gb,
			frameworkMin: 2 * gb,
			frameworkMax: 64 * gb,
			restoreMin:   6 * gb,
			want:         6 * gb,
		},
		{
			name:         "raises to framework minimum",
			requested:    2 * gb,
			frameworkMin: 4 * gb,
			frameworkMax: 64 * gb,
			restoreMin:   2 * gb,
			want:         4 * gb,
		},
		{
			name:         "caps at framework maximum",
			requested:    128 * gb,
			frameworkMin: 4 * gb,
			frameworkMax: 64 * gb,
			restoreMin:   6 * gb,
			want:         64 * gb,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampInstallMemorySize(tt.requested, tt.frameworkMin, tt.frameworkMax, tt.restoreMin)
			if got != tt.want {
				t.Fatalf("clampInstallMemorySize(%d, %d, %d, %d) = %d, want %d",
					tt.requested, tt.frameworkMin, tt.frameworkMax, tt.restoreMin, got, tt.want)
			}
		})
	}
}
