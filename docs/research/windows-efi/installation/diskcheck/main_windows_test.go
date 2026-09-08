package main

import "testing"

func TestUnpartitioned(t *testing.T) {
	for _, tt := range []struct {
		name   string
		offset int
		length int
		want   bool
	}{
		{name: "blank", offset: -1, length: 1 << 20, want: true},
		{name: "signature start", offset: 440, length: 1 << 20, want: true},
		{name: "signature end", offset: 443, length: 1 << 20, want: true},
		{name: "boot code", offset: 0, length: 1 << 20},
		{name: "reserved", offset: 444, length: 1 << 20},
		{name: "partition table", offset: 446, length: 1 << 20},
		{name: "MBR magic", offset: 510, length: 1 << 20},
		{name: "GPT header", offset: 512, length: 1 << 20},
		{name: "tail", offset: (1 << 20) - 1, length: 1 << 20},
		{name: "short read", offset: -1, length: 512},
	} {
		t.Run(tt.name, func(t *testing.T) {
			data := make([]byte, tt.length)
			if tt.offset >= 0 {
				data[tt.offset] = 1
			}
			if got := unpartitioned(data); got != tt.want {
				t.Fatalf("unpartitioned() = %v, want %v", got, tt.want)
			}
		})
	}
}
