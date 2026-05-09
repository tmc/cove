package action

import (
	"errors"
	"strings"
	"testing"
)

func TestTrimOutput(t *testing.T) {
	long := strings.Repeat("x", 350)
	tests := []struct {
		name string
		out  Output
		err  error
		want string
	}{
		{"empty no error", Output{}, nil, ""},
		{"empty with error uses err", Output{}, errors.New("boom"), "boom"},
		{"stdout wins", Output{Stdout: "  hi\n"}, nil, "hi"},
		{"stderr concatenated and trimmed", Output{Stdout: "a", Stderr: "b\n"}, nil, "ab"},
		{"err ignored when output non-empty", Output{Stdout: "out"}, errors.New("x"), "out"},
		{"truncate at 300", Output{Stdout: long}, nil, strings.Repeat("x", 300) + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trimOutput(tt.out, tt.err); got != tt.want {
				t.Fatalf("trimOutput = %q (len=%d), want %q (len=%d)", got, len(got), tt.want, len(tt.want))
			}
		})
	}
}

func TestFirstLine(t *testing.T) {
	tests := []struct {
		name string
		out  Output
		want string
	}{
		{"empty returns ok", Output{}, "ok"},
		{"whitespace only returns ok", Output{Stdout: "   \n  "}, "ok"},
		{"single line trimmed", Output{Stdout: "  hello  "}, "hello"},
		{"first line of multiline", Output{Stdout: "first\nsecond\nthird"}, "first"},
		{"stderr fallback", Output{Stderr: "err line\nmore"}, "err line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstLine(tt.out); got != tt.want {
				t.Fatalf("firstLine = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatGiB(t *testing.T) {
	tests := []struct {
		name string
		in   uint64
		want string
	}{
		{"zero", 0, "0.0 GiB"},
		{"one gib", 1 << 30, "1.0 GiB"},
		{"half gib", 1 << 29, "0.5 GiB"},
		{"ten gib", 10 * (1 << 30), "10.0 GiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatGiB(tt.in); got != tt.want {
				t.Fatalf("formatGiB(%d) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
