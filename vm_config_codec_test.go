package main

import (
	"bytes"
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
