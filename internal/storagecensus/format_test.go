package storagecensus

import "testing"

func TestCategoryToPinNameForRender(t *testing.T) {
	tests := []struct {
		category string
		want     string
	}{
		{"vms", "vm"},
		{"images", "image"},
		{"runs", "run"},
		{"cache", "cache"},
		{"build-scratch", ""},
		{"unknown", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.category, func(t *testing.T) {
			if got := categoryToPinNameForRender(tt.category); got != tt.want {
				t.Errorf("categoryToPinNameForRender(%q) = %q, want %q", tt.category, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	const (
		kib = int64(1024)
		mib = kib * 1024
		gib = mib * 1024
	)
	tests := []struct {
		name string
		in   int64
		want string
	}{
		{"zero", 0, "0 B"},
		{"bytes", 512, "512 B"},
		{"just under KiB", 1023, "1023 B"},
		{"exact KiB", kib, "1.0 KB"},
		{"KiB range", 2 * kib, "2.0 KB"},
		{"MiB range", 5 * mib, "5.0 MB"},
		{"GiB range", 3 * gib, "3.0 GB"},
		{"negative bytes", -250, "-250 B"},
		{"negative KiB", -2 * kib, "-2.0 KB"},
		{"negative GiB", -1 * gib, "-1.0 GB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatBytes(tt.in); got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
