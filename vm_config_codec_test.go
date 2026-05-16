package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/apple/x/vzkit/configcodec"
)

func TestFrameworkConfigSnapshotRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		format  configcodec.Format
		payload []byte
	}{
		{"default-empty", configcodec.DefaultFormat, []byte{}},
		{"format-100", configcodec.Format(100), []byte("hello world")},
		{"format-200-binary", configcodec.Format(200), []byte{0x00, 0x01, 0xff, '\n', 0x7f}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := marshalFrameworkConfigSnapshot(tt.format, tt.payload)
			gotFormat, gotPayload, err := unmarshalFrameworkConfigSnapshot(snap)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if gotFormat != tt.format {
				t.Errorf("format: got %d want %d", gotFormat, tt.format)
			}
			if !bytes.Equal(gotPayload, tt.payload) {
				t.Errorf("payload: got %q want %q", gotPayload, tt.payload)
			}
		})
	}
}

func TestUnmarshalFrameworkConfigSnapshotMissingHeader(t *testing.T) {
	data := []byte("no-header-here")
	format, payload, err := unmarshalFrameworkConfigSnapshot(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if format != configcodec.DefaultFormat {
		t.Errorf("format: got %d want default", format)
	}
	if !bytes.Equal(payload, data) {
		t.Errorf("payload: got %q want %q", payload, data)
	}
}

func TestUnmarshalFrameworkConfigSnapshotInvalidFormat(t *testing.T) {
	data := []byte(frameworkConfigFormatPrefix + "not-a-number\npayload")
	if _, _, err := unmarshalFrameworkConfigSnapshot(data); err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestEmptyIfBlank(t *testing.T) {
	if got := emptyIfBlank(""); got != "<none>" {
		t.Errorf("empty: got %q want <none>", got)
	}
	if got := emptyIfBlank("foo"); got != "foo" {
		t.Errorf("non-empty: got %q want foo", got)
	}
}

func TestEnsureReadableFile(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope")
	err := ensureReadableFile(missing)
	if err == nil || !strings.Contains(err.Error(), "missing required file") {
		t.Errorf("missing: got %v", err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0644); err != nil {
		t.Fatal(err)
	}
	err = ensureReadableFile(empty)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("empty: got %v", err)
	}
	good := filepath.Join(dir, "good")
	if err := os.WriteFile(good, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ensureReadableFile(good); err != nil {
		t.Errorf("good: %v", err)
	}
}

func TestWriteFrameworkConfigBytes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "config.bin")
	want := []byte("payload")
	if err := writeFrameworkConfigBytes(target, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q want %q", got, want)
	}
}
