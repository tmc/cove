package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHTTPHandlerDiskSnapshotList(t *testing.T) {
	vmDir := t.TempDir()
	writeTestDiskSnapshot(t, vmDir, "clean")

	h := NewHTTPHandler(NewControlServerWithVMDir("", vmDir), "test", "test-token", nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/vms/test/disk-snapshots", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Snapshots []DiskSnapshotInfo `json:"snapshots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Snapshots) != 1 || body.Snapshots[0].Name != "clean" {
		t.Fatalf("snapshots = %+v, want clean", body.Snapshots)
	}
}

func TestHTTPHandlerPITSnapshotList(t *testing.T) {
	vmDir := t.TempDir()
	writeTestPITSnapshot(t, vmDir, "rollback")

	h := NewHTTPHandler(NewControlServerWithVMDir("", vmDir), "test", "test-token", nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/vms/test/pit-snapshots", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Snapshots []PITSnapshotInfo `json:"snapshots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Snapshots) != 1 || body.Snapshots[0].Name != "rollback" {
		t.Fatalf("snapshots = %+v, want rollback", body.Snapshots)
	}
}

func TestMCPSnapshotInventoryDoesNotRequireRunningVM(t *testing.T) {
	root := t.TempDir()
	vmDir := filepath.Join(root, "dev")
	writeTestDiskSnapshot(t, vmDir, "clean")
	writeTestPITSnapshot(t, vmDir, "rollback")

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vm_disk_snapshot_list","arguments":{"name":"dev"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"vm_pit_snapshot_list","arguments":{"name":"dev"}}}` + "\n"
	frames := runMCP(t, root, input)
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want 2", len(frames))
	}

	var diskBody struct {
		Snapshots []DiskSnapshotInfo `json:"snapshots"`
	}
	if err := json.Unmarshal([]byte(mcpToolText(t, frames[0])), &diskBody); err != nil {
		t.Fatal(err)
	}
	if len(diskBody.Snapshots) != 1 || diskBody.Snapshots[0].Name != "clean" {
		t.Fatalf("disk snapshots = %+v, want clean", diskBody.Snapshots)
	}

	var pitBody struct {
		Snapshots []PITSnapshotInfo `json:"snapshots"`
	}
	if err := json.Unmarshal([]byte(mcpToolText(t, frames[1])), &pitBody); err != nil {
		t.Fatal(err)
	}
	if len(pitBody.Snapshots) != 1 || pitBody.Snapshots[0].Name != "rollback" {
		t.Fatalf("pit snapshots = %+v, want rollback", pitBody.Snapshots)
	}
}

func mcpToolText(t *testing.T, frame jsonrpcResponse) string {
	t.Helper()
	if frame.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", frame.Error)
	}
	res, _ := frame.Result.(map[string]any)
	if res["isError"] != false {
		t.Fatalf("tool error result: %+v", res)
	}
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content len = %d, want 1", len(content))
	}
	item, _ := content[0].(map[string]any)
	text, _ := item["text"].(string)
	if text == "" {
		t.Fatal("empty tool text")
	}
	return text
}

func writeTestDiskSnapshot(t *testing.T, vmDir, name string) {
	t.Helper()
	dir := filepath.Join(vmDir, "disk-snapshots", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	info := DiskSnapshotInfo{
		Name:        name,
		Created:     time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		Target:      DiskSnapshotSystem,
		SystemSize:  12,
		Description: "test disk snapshot",
		FilePath:    dir,
	}
	writeTestJSON(t, filepath.Join(dir, "metadata.json"), info)
}

func writeTestPITSnapshot(t *testing.T, vmDir, name string) {
	t.Helper()
	dir := filepath.Join(vmDir, "pit", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	info := PITSnapshotInfo{
		Name:             name,
		Created:          time.Date(2026, 4, 23, 13, 0, 0, 0, time.UTC),
		FilePath:         dir,
		DiskFileName:     "disk.img",
		DiskPath:         filepath.Join(dir, "disk.img"),
		DiskSize:         24,
		VMStatePath:      filepath.Join(dir, "state.vmstate"),
		VMStateSize:      8,
		StateDescription: "paused",
	}
	writeTestJSON(t, filepath.Join(dir, "manifest.json"), info)
}

func writeTestJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}
