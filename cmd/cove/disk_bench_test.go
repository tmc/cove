package main

import "testing"

func TestParseDiskBenchSizes(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []diskBenchSize
	}{
		{
			name:  "default",
			value: "",
			want: []diskBenchSize{
				{Label: "8MiB", Bytes: 8 * 1024 * 1024},
				{Label: "64MiB", Bytes: 64 * 1024 * 1024},
			},
		},
		{
			name:  "explicit",
			value: "4KiB, 2MiB, 1GiB",
			want: []diskBenchSize{
				{Label: "4KiB", Bytes: 4 * 1024},
				{Label: "2MiB", Bytes: 2 * 1024 * 1024},
				{Label: "1GiB", Bytes: 1024 * 1024 * 1024},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDiskBenchSizes(tt.value)
			if err != nil {
				t.Fatalf("parseDiskBenchSizes(%q): %v", tt.value, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseDiskBenchSizes(%q): got %d sizes, want %d", tt.value, len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("parseDiskBenchSizes(%q)[%d] = %+v, want %+v", tt.value, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseDiskBenchSizeError(t *testing.T) {
	for _, value := range []string{"", "MiB", "0MiB", "nope"} {
		if _, err := parseDiskBenchSize(value); err == nil {
			t.Fatalf("parseDiskBenchSize(%q): got nil error, want error", value)
		}
	}
}

func TestSanitizeBenchConfigValue(t *testing.T) {
	if got, want := sanitizeBenchConfigValue("cache=none / APFS CoW"), "cache_none_APFS_CoW"; got != want {
		t.Fatalf("sanitizeBenchConfigValue() = %q, want %q", got, want)
	}
}

func TestDiskBenchName(t *testing.T) {
	got := diskBenchName("guest", "shared-folder", "work", "write sync", diskBenchSize{Label: "8MiB"}, "raw+qcow", "cache=none")
	want := "scope=guest/location=shared-folder/tag=work/op=write_sync/size=8MiB/disk=raw_qcow/mount=cache_none"
	if got != want {
		t.Fatalf("diskBenchName() = %q, want %q", got, want)
	}
}
