package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type errWriter struct{ err error }

func (e *errWriter) Write(p []byte) (int, error) { return 0, e.err }

func TestWriteImageDiffJSONWriteError(t *testing.T) {
	w := &errWriter{err: errors.New("disk full")}
	err := writeImageDiffJSON(w, imageDiffOutput{})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("writeImageDiffJSON write-err = %v, want 'disk full'", err)
	}
}

func TestDiffCommandFlagPaths(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		wantSub string
	}{
		{name: "help short-circuits to nil", args: []string{"-h"}, wantErr: false},
		{name: "help long flag", args: []string{"--help"}, wantErr: false},
		{name: "unknown flag", args: []string{"-bogus"}, wantErr: true, wantSub: "flag provided but not defined"},
		{name: "no args", args: nil, wantErr: true, wantSub: "usage: cove diff"},
		{name: "one arg", args: []string{"only"}, wantErr: true, wantSub: "usage: cove diff"},
		{name: "three args", args: []string{"a", "b", "c"}, wantErr: true, wantSub: "usage: cove diff"},
		{name: "bad ref-a", args: []string{"::bad", "ok:tag"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := diffCommand(tt.args)
			if tt.wantErr && err == nil {
				t.Fatalf("diffCommand(%v) = nil, want error", tt.args)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("diffCommand(%v) = %v, want nil", tt.args, err)
			}
			if tt.wantSub != "" && err != nil && !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("diffCommand(%v) err = %q, want substring %q", tt.args, err.Error(), tt.wantSub)
			}
		})
	}
}

func TestDiffCommandJSONErrorWritesMachineReadableStdout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, err := captureStdoutResult(t, func() error {
		return diffCommand([]string{"missing-a:latest", "missing-b:latest", "-json"})
	})
	if err == nil {
		t.Fatal("diffCommand missing -json = nil, want error")
	}
	if strings.Contains(out, "error:") {
		t.Fatalf("stdout contains plain text diagnostic: %q", out)
	}
	var got imageDiffErrorOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if got.Refs != [2]string{"missing-a:latest", "missing-b:latest"} || got.Error == "" {
		t.Fatalf("JSON output = %#v, want refs/error", got)
	}
}
