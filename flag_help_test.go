package main

import (
	"errors"
	"flag"
	"io"
	"testing"
)

func TestIsHelpArg(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"help", true},
		{"-h", true},
		{"--help", true},
		{"", false},
		{"-help", false},
		{"Help", false},
		{"foo", false},
	}
	for _, tt := range tests {
		if got := isHelpArg(tt.in); got != tt.want {
			t.Errorf("isHelpArg(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func newTestFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.String("name", "", "")
	return fs
}

func TestParseFlagsOrHelp(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		fs := newTestFlagSet()
		err := parseFlagsOrHelp(fs, []string{"-h"})
		if !errors.Is(err, errFlagHelp) {
			t.Fatalf("got %v, want errFlagHelp", err)
		}
	})
	t.Run("normal", func(t *testing.T) {
		fs := newTestFlagSet()
		if err := parseFlagsOrHelp(fs, []string{"-name", "x"}); err != nil {
			t.Fatalf("unexpected: %v", err)
		}
	})
	t.Run("error", func(t *testing.T) {
		fs := newTestFlagSet()
		err := parseFlagsOrHelp(fs, []string{"-nope"})
		if err == nil || errors.Is(err, errFlagHelp) {
			t.Fatalf("got %v, want real parse error", err)
		}
	})
}

func TestParseFlagsOrHelpExit(t *testing.T) {
	fs := newTestFlagSet()
	handled, err := parseFlagsOrHelpExit(fs, []string{"-h"})
	if !handled || err != nil {
		t.Errorf("help: handled=%v err=%v, want true,nil", handled, err)
	}
	fs = newTestFlagSet()
	handled, err = parseFlagsOrHelpExit(fs, []string{"-name", "x"})
	if handled || err != nil {
		t.Errorf("ok: handled=%v err=%v, want false,nil", handled, err)
	}
	fs = newTestFlagSet()
	handled, err = parseFlagsOrHelpExit(fs, []string{"-nope"})
	if handled || err == nil {
		t.Errorf("err: handled=%v err=%v, want false,non-nil", handled, err)
	}
}
