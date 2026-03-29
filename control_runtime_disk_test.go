package main

import (
	"encoding/json"
	"testing"
)

func TestParseRuntimeDiskActionRequest(t *testing.T) {
	req, err := parseRuntimeDiskActionRequest([]byte(`{"type":"disk","data":{"action":"swap","storage_index":2,"path":"./disk.img","readOnly":true}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := req.actionName(); got != "swap" {
		t.Fatalf("action = %q", got)
	}
	if idx, ok := req.targetIndex(); !ok || idx != 2 {
		t.Fatalf("target index = %d %v", idx, ok)
	}
	if got := req.targetPath(); got != "./disk.img" {
		t.Fatalf("target path = %q", got)
	}
	if !req.readOnlyValue() {
		t.Fatalf("read only = false")
	}
}

func TestRuntimeDiskRequestedSizeBytes(t *testing.T) {
	tests := []struct {
		name string
		req  RuntimeDiskActionRequest
		want uint64
	}{
		{
			name: "bytes",
			req:  RuntimeDiskActionRequest{SizeBytes: uint64Ptr(1234)},
			want: 1234,
		},
		{
			name: "mb",
			req:  RuntimeDiskActionRequest{SizeMB: uint64Ptr(4)},
			want: 4 * 1024 * 1024,
		},
		{
			name: "gb",
			req:  RuntimeDiskActionRequest{SizeGB: float64Ptr(1.5)},
			want: uint64(1.5 * 1024 * 1024 * 1024),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.req.requestedSizeBytes()
			if err != nil {
				t.Fatalf("requested size: %v", err)
			}
			if got != tt.want {
				t.Fatalf("requested size = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRuntimeDiskRequestedSizeBytesRejectsAmbiguousSizes(t *testing.T) {
	_, err := (RuntimeDiskActionRequest{
		SizeBytes: uint64Ptr(1),
		SizeMB:    uint64Ptr(1),
	}).requestedSizeBytes()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestHandleDiskJSONRequestNoVM(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handleDiskJSONRequest([]byte(`{"action":"list"}`))
	if resp == nil {
		t.Fatal("nil response")
	}
	if resp.Error == "" {
		t.Fatalf("expected error, got %#v", resp)
	}
}

func TestRuntimeDiskResponseJSONRoundTrip(t *testing.T) {
	want := RuntimeDiskListResponse{
		Action: "list",
		Count:  1,
		Disks: []RuntimeDiskInfo{{
			Index:    0,
			Kind:     "disk-image",
			Path:     "/tmp/disk.img",
			ReadOnly: true,
		}},
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got RuntimeDiskListResponse
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Count != 1 || len(got.Disks) != 1 || got.Disks[0].Path != "/tmp/disk.img" || !got.Disks[0].ReadOnly {
		t.Fatalf("round trip = %#v", got)
	}
}

func uint64Ptr(v uint64) *uint64 { return &v }

func float64Ptr(v float64) *float64 { return &v }
