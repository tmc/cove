package main

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestDefaultAppleLogPredicateShape(t *testing.T) {
	term := `(?:subsystem\s+BEGINSWITH\[c\]|senderImagePath\s+CONTAINS|process\s+CONTAINS)\s+"[^"]+"`
	predicateRE, err := regexp.Compile(`^` + term + `(?:\s+OR\s+` + term + `)*$`)
	if err != nil {
		t.Fatalf("compile predicate regex: %v", err)
	}
	if !predicateRE.MatchString(defaultAppleLogPredicate) {
		t.Fatalf("defaultAppleLogPredicate has unexpected shape: %s", defaultAppleLogPredicate)
	}
	for _, want := range []string{
		`subsystem BEGINSWITH[c] "com.apple.virtualization"`,
		`senderImagePath CONTAINS "Virtualization.framework"`,
		`process CONTAINS "Virtual Machine Service"`,
		`subsystem BEGINSWITH[c] "com.apple.MobileDevice"`,
		`senderImagePath CONTAINS "MobileDevice.framework"`,
	} {
		if !strings.Contains(defaultAppleLogPredicate, want) {
			t.Fatalf("defaultAppleLogPredicate missing %q: %s", want, defaultAppleLogPredicate)
		}
	}
}

func TestPrefixLines(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		prefix string
		want   string
	}{
		{"empty", "", "[tag]", ""},
		{"single", "hello\n", "[tag]", "[tag] hello\n"},
		{"multi", "a\nb\nc\n", "[x]", "[x] a\n[x] b\n[x] c\n"},
		{"skips blank and trims", "one\n\n  two  \n\t\n", "[p]", "[p] one\n[p] two\n"},
		{"no trailing newline", "only", "[p]", "[p] only\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}
			oldStdout := os.Stdout
			os.Stdout = w
			done := make(chan []byte, 1)
			go func() {
				var buf bytes.Buffer
				_, _ = io.Copy(&buf, r)
				done <- buf.Bytes()
			}()
			prefixLines(strings.NewReader(tt.input), tt.prefix)
			_ = w.Close()
			os.Stdout = oldStdout
			got := string(<-done)
			if got != tt.want {
				t.Fatalf("prefixLines: got %q want %q", got, tt.want)
			}
		})
	}
}
