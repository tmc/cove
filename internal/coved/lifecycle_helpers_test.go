package coved

import (
	"bytes"
	"testing"
)

func TestBytesTrimSpace(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []byte
	}{
		{"nil", nil, nil},
		{"empty", []byte{}, []byte{}},
		{"all whitespace", []byte("  \n\t\r "), []byte{}},
		{"no whitespace", []byte("abc"), []byte("abc")},
		{"leading", []byte("  hi"), []byte("hi")},
		{"trailing", []byte("hi  \n"), []byte("hi")},
		{"both sides", []byte("\t\r hello \n "), []byte("hello")},
		{"interior preserved", []byte("  a b\tc  "), []byte("a b\tc")},
		{"single char", []byte("x"), []byte("x")},
		{"single space", []byte(" "), []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bytesTrimSpace(tt.in)
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("bytesTrimSpace(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
