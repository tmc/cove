package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentstate "github.com/tmc/cove/internal/agent"
	agentpb "github.com/tmc/cove/proto/agentpb"
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

func TestHandleDiskJSONRequestDispatchErrors(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty", ``, "empty request"},
		{"bad json", `{`, "parse runtime disk request"},
		{"unknown action", `{"action":"frobnicate"}`, "unknown disk action"},
		{"swap missing index", `{"action":"swap","path":"/tmp/a.img"}`, "disk swap requires index"},
		{"swap missing path", `{"action":"swap","index":0}`, "disk swap requires path"},
		{"resize missing index", `{"action":"resize","size_bytes":1024}`, "disk resize requires index"},
		{"envelope unwrap unknown", `{"data":{"action":"nope"}}`, "unknown disk action"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := s.handleDiskJSONRequest([]byte(tt.body))
			if resp == nil || resp.Error == "" {
				t.Fatalf("want error containing %q, got %#v", tt.want, resp)
			}
			if !strings.Contains(resp.Error, tt.want) {
				t.Fatalf("error = %q, want substring %q", resp.Error, tt.want)
			}
		})
	}
}

func TestRuntimeDiskActionNameAliasAndCase(t *testing.T) {
	req := RuntimeDiskActionRequest{Type: "  SWAP  "}
	if got := req.actionName(); got != "swap" {
		t.Fatalf("actionName via Type alias = %q, want %q", got, "swap")
	}
}

func TestRuntimeDiskShouldExpandMacOSRootAPFS(t *testing.T) {
	macDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(macDir, "hw.model"), []byte("mac"), 0644); err != nil {
		t.Fatal(err)
	}
	if !runtimeDiskShouldExpandMacOSRootAPFS(macDir, runtimeDiskEntry{Index: 0}) {
		t.Fatalf("macOS primary disk should expand APFS")
	}
	if runtimeDiskShouldExpandMacOSRootAPFS(macDir, runtimeDiskEntry{Index: 1}) {
		t.Fatalf("macOS secondary disk should not use root APFS expansion")
	}

	linuxDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(linuxDir, "linux-disk.img"), []byte("linux"), 0644); err != nil {
		t.Fatal(err)
	}
	if runtimeDiskShouldExpandMacOSRootAPFS(linuxDir, runtimeDiskEntry{Index: 0}) {
		t.Fatalf("Linux disk should not use macOS APFS expansion")
	}

	windowsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(windowsDir, "windows-disk.img"), []byte("windows"), 0644); err != nil {
		t.Fatal(err)
	}
	if runtimeDiskShouldExpandMacOSRootAPFS(windowsDir, runtimeDiskEntry{Index: 0}) {
		t.Fatalf("Windows disk should not use macOS APFS expansion")
	}
}

func TestRuntimeDiskCurrentSizeUsesConfiguredPrimaryDisk(t *testing.T) {
	vmDir := t.TempDir()
	diskPath := filepath.Join(vmDir, "disk.img")
	if err := os.WriteFile(filepath.Join(vmDir, "hw.model"), []byte("mac"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(diskPath, []byte("disk-bytes"), 0644); err != nil {
		t.Fatal(err)
	}

	entry := runtimeDiskEntry{Index: 0, Info: RuntimeDiskInfo{Kind: "storage-device"}}
	got, ok := runtimeDiskCurrentSize(vmDir, entry)
	if !ok || got != uint64(len("disk-bytes")) {
		t.Fatalf("runtimeDiskCurrentSize = %d, %v, want %d, true", got, ok, len("disk-bytes"))
	}

	augmentRuntimeDiskEntryInfo(vmDir, &entry)
	if entry.Info.Kind != "disk-image" || entry.Info.Path != diskPath || entry.Info.FileSizeBytes != uint64(len("disk-bytes")) {
		t.Fatalf("augmented entry = %+v", entry.Info)
	}
}

func TestExpandMacOSRootAPFS(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	agent := &fakeRuntimeDiskGuestAgent{
		resp: &agentpb.ResizeMacOSAPFSResponse{
			Expanded:                  true,
			Container:                 "disk3",
			PhysicalStore:             "disk0s2",
			ContainerTotalBytesBefore: 58 << 30,
			ContainerTotalBytesAfter:  96 << 30,
			Stdout:                    "started APFS operation\nfinished APFS operation\n",
		},
	}

	got, err := s.expandMacOSRootAPFS(agent)
	if err != nil {
		t.Fatalf("expandMacOSRootAPFS: %v", err)
	}
	if got.Platform != agentstate.PlatformMacOS || !got.Attempted || !got.Expanded {
		t.Fatalf("guest resize = %+v", got)
	}
	if agent.preflightOnly || !agent.called {
		t.Fatalf("agent call = called %v preflight %v, want resize", agent.called, agent.preflightOnly)
	}
	if got.Container != "disk3" || got.PhysicalStore != "disk0s2" {
		t.Fatalf("guest resize = %+v", got)
	}
	if got.ContainerTotalBytesBefore != 58<<30 || got.ContainerTotalBytesAfter != 96<<30 {
		t.Fatalf("guest resize bytes = %+v", got)
	}
}

func TestPreflightMacOSRootAPFSReportsRecoveryBlocker(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	agent := &fakeRuntimeDiskGuestAgent{
		err: errors.New("Recovery partition blocks APFS expansion: disk0s2 is followed by disk0s3 on /dev/disk0"),
	}

	err := s.preflightMacOSRootAPFS(agent)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{
		"run diskutil preflight",
		"Recovery partition blocks APFS expansion",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %q", want, err)
		}
	}
	if !agent.called || !agent.preflightOnly {
		t.Fatalf("agent call = called %v preflight %v, want preflight", agent.called, agent.preflightOnly)
	}
}

func TestExpandMacOSRootAPFSReportsDiskutilFailure(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	agent := &fakeRuntimeDiskGuestAgent{
		err: errors.New("resize APFS container: stderr: could not find APFS container"),
	}

	guest, err := s.expandMacOSRootAPFS(agent)
	if err == nil {
		t.Fatal("expected error")
	}
	if guest != nil {
		t.Fatalf("guest resize = %+v, want nil", guest)
	}
	for _, want := range []string{
		"run diskutil",
		"could not find APFS container",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %q", want, err)
		}
	}
}

func TestMacOSResizeErrorsIncludeNextAction(t *testing.T) {
	unavailable := macOSResizeAgentUnavailableError(0, 96<<30, context.Canceled)
	for _, want := range []string{"root guest agent", "no host disk changes made", "cove ctl agent-ping", "cove ctl disk resize 0 103079215104B"} {
		if !strings.Contains(unavailable.Error(), want) {
			t.Fatalf("unavailable error missing %q: %s", want, unavailable)
		}
	}

	failed := macOSResizeGuestFailedError(0, 96<<30, context.Canceled)
	for _, want := range []string{"resized disk 0 to 103079215104 bytes", "macOS APFS expansion failed", "host disk is already grown", "root guest agent", macOSResizeAPFSManualCommand} {
		if !strings.Contains(failed.Error(), want) {
			t.Fatalf("failed error missing %q: %s", want, failed)
		}
	}

	preflight := macOSResizePreflightFailedError(0, 96<<30, false, errors.New("Recovery partition blocks APFS expansion"))
	for _, want := range []string{"APFS expansion preflight failed", "Recovery partition blocks", "no host disk changes made", "cove ctl disk resize 0 103079215104B"} {
		if !strings.Contains(preflight.Error(), want) {
			t.Fatalf("preflight error missing %q: %s", want, preflight)
		}
	}
}

func TestRuntimeDiskListSummary(t *testing.T) {
	got := runtimeDiskListSummary([]RuntimeDiskInfo{
		{Index: 0, FileSizeBytes: 8 << 30},
		{Index: 1, FileSizeBytes: 512 << 20, ReadOnly: true},
	})
	for _, want := range []string{"2 disks", "8.5 GB backing files", "1 read-only"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary missing %q: %q", want, got)
		}
	}
}

type fakeRuntimeDiskGuestAgent struct {
	resp          *agentpb.ResizeMacOSAPFSResponse
	err           error
	called        bool
	preflightOnly bool
}

func (f *fakeRuntimeDiskGuestAgent) ResizeMacOSAPFS(ctx context.Context, preflightOnly bool) (*agentpb.ResizeMacOSAPFSResponse, error) {
	f.called = true
	f.preflightOnly = preflightOnly
	return f.resp, f.err
}

func uint64Ptr(v uint64) *uint64 { return &v }

func float64Ptr(v float64) *float64 { return &v }
