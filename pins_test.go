package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/storagepins"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

// pinsTestHome points coveRoot() at a temp directory via HOME override.
func pinsTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if !strings.HasPrefix(vmconfig.BaseDir(), home) {
		t.Fatalf("BaseDir() = %q, want under %q", vmconfig.BaseDir(), home)
	}
	return home
}

func TestRunPinsListEmpty(t *testing.T) {
	pinsTestHome(t)
	var buf bytes.Buffer
	if err := runPinsList(nil, &buf); err != nil {
		t.Fatalf("runPinsList: %v", err)
	}
	if got := buf.String(); got != "no pins\n" {
		t.Errorf("empty output = %q, want %q", got, "no pins\n")
	}
}

func TestRunPinsListJSON(t *testing.T) {
	pinsTestHome(t)
	if err := handlePinCommand([]string{"image:alpine:3"}); err != nil {
		t.Fatalf("pin: %v", err)
	}
	var buf bytes.Buffer
	if err := runPinsList([]string{"-json"}, &buf); err != nil {
		t.Fatalf("runPinsList -json: %v", err)
	}
	var pins []storagepins.Pin
	if err := json.Unmarshal(buf.Bytes(), &pins); err != nil {
		t.Fatalf("decode: %v (out=%q)", err, buf.String())
	}
	if len(pins) != 1 || pins[0].Ref() != "image:alpine:3" {
		t.Errorf("pins = %+v, want one image:alpine:3", pins)
	}
}

func TestPinUnpinRoundTrip(t *testing.T) {
	home := pinsTestHome(t)
	if err := handlePinCommand([]string{"vm:default"}); err != nil {
		t.Fatalf("pin: %v", err)
	}
	// Persisted on disk.
	f, err := storagepins.Load(filepath.Dir(vmconfig.BaseDir()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !f.IsPinned("vm", "default") {
		t.Fatalf("vm:default not persisted under %s", home)
	}
	if err := handleUnpinCommand([]string{"vm:default"}); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	f, err = storagepins.Load(filepath.Dir(vmconfig.BaseDir()))
	if err != nil {
		t.Fatalf("load2: %v", err)
	}
	if f.IsPinned("vm", "default") {
		t.Errorf("vm:default still pinned after unpin")
	}
}

func TestPinCommandParseErrors(t *testing.T) {
	pinsTestHome(t)
	cases := []struct {
		name string
		args []string
	}{
		{"missing arg", nil},
		{"too many args", []string{"vm:a", "vm:b"}},
		{"bad category", []string{"bogus:x"}},
		{"missing id", []string{"vm:"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := handlePinCommand(tc.args); err == nil {
				t.Errorf("handlePinCommand(%v) = nil, want error", tc.args)
			}
			if err := handleUnpinCommand(tc.args); err == nil {
				t.Errorf("handleUnpinCommand(%v) = nil, want error", tc.args)
			}
		})
	}
}
