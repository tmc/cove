package main

import (
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

func TestWriteImageSearchJSONWriteError(t *testing.T) {
	w := &errWriter{err: errors.New("disk full")}
	err := writeImageSearchJSON(w, nil)
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want disk full", err)
	}
}
