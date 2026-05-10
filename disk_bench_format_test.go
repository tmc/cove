package main

import "testing"

func TestFormatDiskBenchSize(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{2 * 1024 * 1024 * 1024, "2GiB"},
		{5 * 1024 * 1024, "5MiB"},
		{4 * 1024, "4KiB"},
		{0, "0GiB"}, // zero is divisible by GiB
		{777, "777B"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := formatDiskBenchSize(tt.size); got != tt.want {
				t.Fatalf("formatDiskBenchSize(%d) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}
