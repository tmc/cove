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

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    uint64
		wantErr bool
	}{
		{"1024", 1024, false},
		{"1k", 1024, false},
		{"2KB", 2048, false},
		{"1MiB", 1024 * 1024, false},
		{"3g", 3 * 1024 * 1024 * 1024, false},
		{"1.5K", 1536, false},
		{"  4kb  ", 4096, false},
		{"", 0, true},
		{"abc", 0, true},
		{"10xb", 0, true},
		{"-5k", 0, true},
		{"0", 0, true},
		{"1.5B", 0, true},
		{"18446744073709551616B", 0, true},
		{"100000000000000000000000000000000000000T", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Parse(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}
