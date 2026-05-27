//go:build integration && darwin && arm64

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestIntegrationDiskResize(t *testing.T) {
	name := strings.TrimSpace(*flagIntegrationVM)
	if name == "" {
		t.Skip("set -integration.vm or VZ_TEST_VM to a macOS VM name")
	}
	ensureIntegrationBaseVM(t, name, false)

	baseDir := resolvePath(vmconfig.Path(name))
	token, err := LoadControlTokenFromPath(GetControlTokenPathForVM(baseDir))
	if err != nil {
		t.Fatalf("load control token for %q: %v", name, err)
	}
	if controlSocketReady(GetControlSocketPathForVM(baseDir), token) {
		t.Skip("macOS disk resize integration requires the named base VM to be stopped so the test can clone it safely")
	}

	artifacts := newIntegrationArtifacts(t, "disk-resize")
	writeIntegrationBinaryArtifact(t, artifacts)

	cloneName := integrationCloneName(t.Name())
	if err := CloneVM(CloneOptions{Source: name, Target: cloneName, Linked: true}); err != nil {
		t.Fatalf("CloneVM() error = %v", err)
	}
	clone := clonedTestVM(t, cloneName, false)
	artifacts.writeText("clone.txt", fmt.Sprintf("name=%s\ndir=%s\n", clone.name, clone.dir))

	diskPath := filepath.Join(clone.dir, runtimeSurfaceDiskFileName(clone))
	before, err := os.Stat(diskPath)
	if err != nil {
		t.Fatalf("stat clone disk %q: %v", diskPath, err)
	}
	targetSize := uint64(before.Size()) + 512*1024*1024
	artifacts.writeText("stopped-host-disk-before.txt", fmt.Sprintf("path=%s\nsize=%d\ntarget=%d\n", diskPath, before.Size(), targetSize))

	out, err := runIntegrationBinaryCommand(t, "disk", "resize", clone.name, fmt.Sprintf("%dB", targetSize))
	artifacts.writeText("stopped-resize.txt", fmt.Sprintf("command=cove disk resize %s %dB\nerror=%v\noutput:\n%s", clone.name, targetSize, err, out))
	if err != nil {
		t.Fatalf("stopped disk resize: %v\n%s", err, out)
	}
	if !waitRuntimeSurfaceDiskSize(t, diskPath, targetSize, 30*time.Second) {
		info, err := os.Stat(diskPath)
		if err != nil {
			t.Fatalf("stat stopped resized disk %q: %v", diskPath, err)
		}
		t.Fatalf("stopped disk size after resize = %d, want at least %d", info.Size(), targetSize)
	}

	startTestVM(t, clone)
	waitVMReadyTB(t, clone, integrationVMReadyTimeout(clone, false))
	artifacts.writeText("clone-running.txt", fmt.Sprintf("name=%s\ndir=%s\nlog=%s\n", clone.name, clone.dir, clone.logPath))
	beforeGuest := captureMacOSRuntimeDiskState(t, artifacts, clone, "before-live-expand")

	resp, err := ctlSendJSON(clone.sock, map[string]interface{}{
		"type": "disk",
		"data": map[string]any{
			"action":     "resize",
			"index":      0,
			"size_bytes": targetSize,
		},
	}, 5*time.Minute)
	artifacts.writeJSON("live-expand-response.json", resp)
	if err != nil {
		t.Fatalf("live disk resize: %v", err)
	}
	if !resp.Success {
		_ = captureMacOSRuntimeDiskState(t, artifacts, clone, "after-live-expand-failed")
		if runtimeSurfaceMacOSRecoveryBlocksAPFS(resp.Error) {
			t.Skipf("macOS APFS expansion unsupported for this guest partition layout after stopped host resize: %s", resp.Error)
		}
		t.Fatalf("live disk resize failed: %s", resp.Error)
	}
	var resized RuntimeDiskMutationResponse
	if err := json.Unmarshal([]byte(resp.GetData()), &resized); err != nil {
		t.Fatalf("decode live disk resize: %v\n%s", err, resp.GetData())
	}
	if resized.GuestResize == nil || !resized.GuestResize.Attempted || !resized.GuestResize.Expanded {
		t.Fatalf("live disk resize did not report APFS expansion: %+v", resized.GuestResize)
	}
	afterGuest := captureMacOSRuntimeDiskState(t, artifacts, clone, "after-live-expand")
	if afterGuest.PhysicalDiskBytes < targetSize {
		t.Fatalf("guest disk0 physical size = %d, want at least %d", afterGuest.PhysicalDiskBytes, targetSize)
	}
	if afterGuest.ContainerBytes <= beforeGuest.ContainerBytes {
		t.Fatalf("guest APFS container size = %d, want greater than before %d", afterGuest.ContainerBytes, beforeGuest.ContainerBytes)
	}
	if afterGuest.RootKBytes <= beforeGuest.RootKBytes {
		t.Fatalf("guest root df blocks = %d, want greater than before %d", afterGuest.RootKBytes, beforeGuest.RootKBytes)
	}
}

func TestIntegrationDiskResizePreflightOnly(t *testing.T) {
	vm := acquireIntegrationVM(t)
	defer vm.Cleanup(t)
	artifacts := newIntegrationArtifacts(t, "disk-resize-preflight")
	writeIntegrationBinaryArtifact(t, artifacts)

	resp, err := ctlSendJSON(vm.sock, map[string]interface{}{
		"type": "disk",
		"data": map[string]any{
			"action": "list",
		},
	}, 30*time.Second)
	artifacts.writeJSON("disk-list.json", resp)
	if err != nil {
		t.Fatalf("disk list: %v", err)
	}
	if !resp.Success {
		t.Fatalf("disk list failed: %s", resp.Error)
	}
	var list RuntimeDiskListResponse
	if err := json.Unmarshal([]byte(resp.GetData()), &list); err != nil {
		t.Fatalf("decode disk list: %v\n%s", err, resp.GetData())
	}
	if len(list.Disks) == 0 {
		t.Fatalf("disk list returned no disks")
	}
	disk := list.Disks[0]
	if disk.Index != 0 {
		t.Fatalf("first disk index = %d, want 0", disk.Index)
	}
	if disk.FileSizeBytes == 0 {
		t.Fatalf("disk 0 file_size_bytes = 0: %+v", disk)
	}
	var beforeSize int64 = -1
	if disk.Path != "" {
		info, err := os.Stat(disk.Path)
		if err != nil {
			t.Fatalf("stat disk %q: %v", disk.Path, err)
		}
		beforeSize = info.Size()
	}
	targetSize := disk.FileSizeBytes + 512*1024*1024

	resp, err = ctlSendJSON(vm.sock, map[string]interface{}{
		"type": "disk",
		"data": map[string]any{
			"action":         "resize",
			"index":          0,
			"size_bytes":     targetSize,
			"preflight_only": true,
		},
	}, 30*time.Second)
	artifacts.writeJSON("resize-preflight-response.json", resp)
	if err != nil {
		t.Fatalf("disk resize preflight: %v", err)
	}
	if disk.Path != "" {
		info, err := os.Stat(disk.Path)
		if err != nil {
			t.Fatalf("stat disk after preflight %q: %v", disk.Path, err)
		}
		if info.Size() != beforeSize {
			t.Fatalf("disk size changed during preflight: before=%d after=%d", beforeSize, info.Size())
		}
	}
	if !resp.Success {
		if runtimeSurfaceMacOSRecoveryBlocksAPFS(resp.Error) {
			for _, want := range []string{"APFS expansion preflight failed", "Recovery partition blocks", "no host disk changes made"} {
				if !strings.Contains(resp.Error, want) {
					t.Fatalf("preflight blocker missing %q: %s", want, resp.Error)
				}
			}
			return
		}
		t.Fatalf("disk resize preflight failed: %s", resp.Error)
	}
	var preflight RuntimeDiskMutationResponse
	if err := json.Unmarshal([]byte(resp.GetData()), &preflight); err != nil {
		t.Fatalf("decode resize preflight: %v\n%s", err, resp.GetData())
	}
	if preflight.Action != "resize-preflight" {
		t.Fatalf("preflight action = %q, want resize-preflight", preflight.Action)
	}
	if !strings.Contains(preflight.Message, "no host disk changes made") {
		t.Fatalf("preflight message missing no-host-change text: %q", preflight.Message)
	}
}

func writeIntegrationBinaryArtifact(t *testing.T, artifacts *integrationArtifacts) {
	t.Helper()

	bin := buildIntegrationBinary(t)
	cmd := exec.Command("codesign", "-dv", bin)
	out, err := cmd.CombinedOutput()
	artifacts.writeText("integration-binary.txt", fmt.Sprintf("path=%s\ncodesign_error=%v\ncodesign:\n%s", bin, err, out))
}
