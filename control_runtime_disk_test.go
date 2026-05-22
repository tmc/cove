package main

import (
	"context"
	"encoding/json"
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
}

func TestExpandMacOSRootAPFS(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	agent := &fakeRuntimeDiskGuestAgent{
		resp: &agentpb.ExecResponse{
			ExitCode: 0,
			Stdout:   []byte("expanded APFS container disk3\n"),
		},
	}

	got, err := s.expandMacOSRootAPFS(agent)
	if err != nil {
		t.Fatalf("expandMacOSRootAPFS: %v", err)
	}
	if got.Platform != agentstate.PlatformMacOS || !got.Attempted || !got.Expanded {
		t.Fatalf("guest resize = %+v", got)
	}
	if len(agent.args) != 3 || agent.args[0] != "/bin/sh" || agent.args[1] != "-c" {
		t.Fatalf("agent args = %#v", agent.args)
	}
	if !strings.Contains(agent.args[2], `diskutil apfs resizeContainer "$container" 0`) {
		t.Fatalf("script missing resizeContainer:\n%s", agent.args[2])
	}
}

func TestExpandMacOSRootAPFSReportsDiskutilFailure(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	agent := &fakeRuntimeDiskGuestAgent{
		resp: &agentpb.ExecResponse{
			ExitCode: 64,
			Stderr:   []byte("could not find APFS container\n"),
		},
	}

	guest, err := s.expandMacOSRootAPFS(agent)
	if err == nil {
		t.Fatal("expected error")
	}
	if guest == nil || guest.Expanded {
		t.Fatalf("guest resize = %+v, want attempted failure", guest)
	}
	if !strings.Contains(err.Error(), "diskutil exit 64: could not find APFS container") {
		t.Fatalf("error = %q", err)
	}
}

func TestMacOSResizeErrorsIncludeNextAction(t *testing.T) {
	unavailable := macOSResizeAgentUnavailableError(0, 96<<30, context.Canceled)
	for _, want := range []string{"no changes made", "cove ctl agent-ping", "cove ctl disk resize 0 103079215104B"} {
		if !strings.Contains(unavailable.Error(), want) {
			t.Fatalf("unavailable error missing %q: %s", want, unavailable)
		}
	}

	failed := macOSResizeGuestFailedError(0, 96<<30, context.Canceled)
	for _, want := range []string{"resized disk 0 to 103079215104 bytes", "macOS APFS expansion failed", macOSResizeAPFSManualCommand} {
		if !strings.Contains(failed.Error(), want) {
			t.Fatalf("failed error missing %q: %s", want, failed)
		}
	}
}

type fakeRuntimeDiskGuestAgent struct {
	args []string
	resp *agentpb.ExecResponse
	err  error
}

func (f *fakeRuntimeDiskGuestAgent) Exec(ctx context.Context, args []string, env map[string]string, workDir string) (*agentpb.ExecResponse, error) {
	f.args = append([]string(nil), args...)
	return f.resp, f.err
}

func uint64Ptr(v uint64) *uint64 { return &v }

func float64Ptr(v float64) *float64 { return &v }
