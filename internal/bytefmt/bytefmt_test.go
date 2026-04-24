package bytefmt

import "testing"

func TestSize(t *testing.T) {
	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{name: "bytes", bytes: 12, want: "12 B"},
		{name: "kilobytes", bytes: 1536, want: "1.5 KB"},
		{name: "megabytes", bytes: 5 * 1024 * 1024, want: "5.0 MB"},
		{name: "gigabytes", bytes: 3*1024*1024*1024 + 512*1024*1024, want: "3.5 GB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Size(tt.bytes); got != tt.want {
				t.Fatalf("Size(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}
