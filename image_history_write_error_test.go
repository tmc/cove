package main

import (
	"errors"
	"strings"
	"testing"
)

func TestWriteImageHistoryJSONWriteError(t *testing.T) {
	w := &errWriter{err: errors.New("disk full")}
	err := writeImageHistoryJSON(w, ImageHistory{})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v, want 'disk full'", err)
	}
}
