package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureSIPStdout swaps os.Stdout, runs fn, and returns captured output.
func captureSIPStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })
	fn()
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestHandleSIPCommandUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"empty", nil},
		{"help-word", []string{"help"}},
		{"dash-h", []string{"-h"}},
		{"dash-dash-help", []string{"--help"}},
		{"sub-dash-h", []string{"status", "-h"}},
		{"sub-help-word", []string{"enable", "help"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			out := captureSIPStdout(t, func() { err = handleSIPCommand(tc.args) })
			if err != nil {
				t.Fatalf("handleSIPCommand(%v) err = %v, want nil", tc.args, err)
			}
			if !strings.Contains(out, "Usage:") && !strings.Contains(out, "sip") {
				t.Fatalf("handleSIPCommand(%v) stdout = %q, want sip usage", tc.args, out)
			}
		})
	}
}

func TestHandleSIPCommandRefusesLinuxForAllSubcommands(t *testing.T) {
	oldVMDir := vmDir
	vmDir = linuxTestVMDir(t)
	t.Cleanup(func() { vmDir = oldVMDir })

	cases := [][]string{
		{"enable"},
		{"disable"},
		{"status"},
		{"enable-auto"},
		{"disable-auto"},
	}
	for _, args := range cases {
		t.Run(args[0], func(t *testing.T) {
			err := handleSIPCommand(args)
			if err == nil {
				t.Fatalf("handleSIPCommand(%v) err = nil, want linux refusal", args)
			}
			if !strings.Contains(err.Error(), "sip is only supported for macOS VMs") {
				t.Fatalf("handleSIPCommand(%v) err = %v, want linux refusal", args, err)
			}
		})
	}
}

func TestHandleSIPCommandUnknown(t *testing.T) {
	err := handleSIPCommand([]string{"bogus"})
	if err == nil {
		t.Fatal("handleSIPCommand([bogus]) err = nil, want unknown sip command")
	}
	if !strings.Contains(err.Error(), "unknown sip command") {
		t.Fatalf("handleSIPCommand([bogus]) err = %v, want unknown sip command", err)
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("handleSIPCommand([bogus]) err = %v, want subcommand name in message", err)
	}
}
