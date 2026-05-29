package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureRosettaStdout swaps os.Stdout, runs fn, returns captured output.
func captureRosettaStdout(t *testing.T, fn func()) string {
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

func TestHandleRosettaCommandEmptyPrintsHelp(t *testing.T) {
	var err error
	out := captureRosettaStdout(t, func() { err = handleRosettaCommand(nil) })
	if err != nil {
		t.Fatalf("handleRosettaCommand(nil) err = %v, want nil", err)
	}
	for _, want := range []string{"status", "install", "setup"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty-args stdout missing %q\nstdout: %s", want, out)
		}
	}
}

func TestHandleRosettaCommandHelpPrintsHelp(t *testing.T) {
	var err error
	out := captureRosettaStdout(t, func() { err = handleRosettaCommand([]string{"help"}) })
	if err != nil {
		t.Fatalf("handleRosettaCommand([help]) err = %v, want nil", err)
	}
	if !strings.Contains(out, "Apple Silicon") {
		t.Errorf("help stdout missing Apple Silicon\nstdout: %s", out)
	}
}

func TestHandleRosettaCommandSetupPrintsGuestInstructions(t *testing.T) {
	var err error
	out := captureRosettaStdout(t, func() { err = handleRosettaCommand([]string{"setup"}) })
	if err != nil {
		t.Fatalf("handleRosettaCommand([setup]) err = %v, want nil", err)
	}
	for _, want := range []string{"virtiofs", "/run/rosetta", "--register"} {
		if !strings.Contains(out, want) {
			t.Errorf("setup stdout missing %q\nstdout: %s", want, out)
		}
	}
}
