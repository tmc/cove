package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestWriteInspectJSONWriteError(t *testing.T) {
	w := &errWriter{err: errors.New("disk full")}
	err := writeInspectJSON(w, ImageInspectOutput{})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want disk full", err)
	}
}

func TestWriteInspectDiffJSONWriteError(t *testing.T) {
	w := &errWriter{err: errors.New("disk full")}
	err := writeInspectDiffJSON(w, imageInspectDiff{})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want disk full", err)
	}
}

func TestWriteInspectErrorJSONWriteError(t *testing.T) {
	w := &errWriter{err: errors.New("disk full")}
	err := writeInspectErrorJSON(w, imageInspectErrorOutput{})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want disk full", err)
	}
}

func TestWriteImageSearchJSONWriteError(t *testing.T) {
	w := &errWriter{err: errors.New("disk full")}
	err := writeImageSearchJSON(w, nil)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want disk full", err)
	}
}

func TestRunImageInspectMissingHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := runImageInspect([]string{"missing:latest"})
	if err == nil {
		t.Fatal("runImageInspect(missing) = nil, want error")
	}
	for _, want := range []string{"cove image list", "cove image search missing"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
}

func TestRunImageInspectMissingJSONWritesMachineReadableStdout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, err := captureStdoutResult(t, func() error {
		return runImageInspect([]string{"-json", "missing:latest"})
	})
	if err == nil {
		t.Fatal("runImageInspect -json missing = nil, want error")
	}
	if strings.Contains(out, "error:") || strings.Contains(out, "hint:") {
		t.Fatalf("stdout contains plain text diagnostic: %q", out)
	}
	var got imageInspectErrorOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if got.Ref != "missing:latest" || got.Error == "" || got.Hint == "" {
		t.Fatalf("JSON output = %#v, want ref/error/hint", got)
	}
}
