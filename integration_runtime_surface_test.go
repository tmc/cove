//go:build integration && darwin && arm64

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func testRuntimeSurface(t *testing.T, vm *testVM) {
	t.Run("capabilities", func(t *testing.T) {
		resp := ctlDo(t, vm, &controlpb.ControlRequest{Type: "capabilities"})
		caps := resp.GetCapabilities()
		if caps == nil {
			t.Fatal("capabilities: missing typed response")
		}
		if caps.GetProtocolVersion() != "vz.control.v1" {
			t.Fatalf("capabilities protocol_version = %q, want %q", caps.GetProtocolVersion(), "vz.control.v1")
		}
		if !caps.GetAuthRequired() {
			t.Fatal("capabilities auth_required = false, want true")
		}
		for _, cmd := range []string{"capabilities", "disk", "usb", "vnc-status", "debug-stub-status"} {
			if !containsString(caps.GetCommands(), cmd) {
				t.Fatalf("capabilities missing command %q", cmd)
			}
		}
		for _, feature := range []string{"runtimeDiskControl", "runtimeUSBControl", "vncStatus", "debugStubStatus"} {
			if !caps.GetFeatures()[feature] {
				t.Fatalf("capabilities missing feature %q", feature)
			}
		}
	})

	t.Run("vnc-status", func(t *testing.T) {
		resp := ctlDo(t, vm, &controlpb.ControlRequest{Type: "vnc-status"})
		var status VNCStatus
		if err := json.Unmarshal([]byte(resp.GetData()), &status); err != nil {
			t.Fatalf("decode vnc status: %v\n%s", err, resp.GetData())
		}
		if status.Enabled {
			t.Fatalf("default vnc status = enabled, want disabled: %+v", status)
		}
	})

	t.Run("debug-stub-status", func(t *testing.T) {
		resp := ctlDo(t, vm, &controlpb.ControlRequest{Type: "debug-stub-status"})
		var status DebugStubStatus
		if err := json.Unmarshal([]byte(resp.GetData()), &status); err != nil {
			t.Fatalf("decode debug stub status: %v\n%s", err, resp.GetData())
		}
		if status.Enabled {
			t.Fatalf("default debug stub status = enabled, want disabled: %+v", status)
		}
	})

	t.Run("disk-list", func(t *testing.T) {
		resp, err := ctlSendJSON(vm.sock, map[string]interface{}{
			"type": "disk",
			"data": map[string]any{"action": "list"},
		}, 30*time.Second)
		if err != nil {
			t.Fatalf("disk list: %v", err)
		}
		if !resp.Success {
			t.Fatalf("disk list failed: %s", resp.Error)
		}
		var disks RuntimeDiskListResponse
		if err := json.Unmarshal([]byte(resp.GetData()), &disks); err != nil {
			t.Fatalf("decode disk list: %v\n%s", err, resp.GetData())
		}
		if disks.Count == 0 || len(disks.Disks) == 0 {
			t.Fatalf("disk list empty: %+v", disks)
		}
		if disks.Disks[0].Index != 0 {
			t.Fatalf("first disk index = %d, want 0", disks.Disks[0].Index)
		}
	})

	t.Run("disk-resize-live", func(t *testing.T) {
		cloneName := integrationCloneName(t.Name())
		if err := CloneVM(CloneOptions{Source: vm.name, Target: cloneName, Linked: true}); err != nil {
			t.Fatalf("CloneVM() error = %v", err)
		}
		clone := clonedTestVM(t, cloneName, vm.linux)

		startTestVM(t, clone)
		waitVMReadyTB(t, clone, integrationVMReadyTimeout(clone, false))

		diskPath := filepath.Join(clone.dir, runtimeSurfaceDiskFileName(clone))
		before, err := os.Stat(diskPath)
		if err != nil {
			t.Fatalf("stat disk %q: %v", diskPath, err)
		}
		targetSize := uint64(before.Size()) + 64*1024*1024

		resp, err := ctlSendJSON(clone.sock, map[string]interface{}{
			"type": "disk",
			"data": map[string]any{
				"action":     "resize",
				"index":      0,
				"size_bytes": targetSize,
			},
		}, 30*time.Second)
		if err != nil {
			t.Fatalf("disk resize: %v", err)
		}
		if !resp.Success {
			t.Fatalf("disk resize failed: %s", resp.Error)
		}
		var resized RuntimeDiskMutationResponse
		if err := json.Unmarshal([]byte(resp.GetData()), &resized); err != nil {
			t.Fatalf("decode disk resize: %v\n%s", err, resp.GetData())
		}
		if resized.Index != 0 {
			t.Fatalf("resize index = %d, want 0", resized.Index)
		}

		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			info, err := os.Stat(diskPath)
			if err != nil {
				t.Fatalf("stat resized disk %q: %v", diskPath, err)
			}
			if uint64(info.Size()) >= targetSize {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}

		info, err := os.Stat(diskPath)
		if err != nil {
			t.Fatalf("stat resized disk %q: %v", diskPath, err)
		}
		t.Fatalf("disk size after resize = %d, want at least %d", info.Size(), targetSize)
	})

	t.Run("usb-list", func(t *testing.T) {
		resp, err := ctlSendJSON(vm.sock, map[string]interface{}{
			"type": "usb",
			"data": map[string]any{"action": "list"},
		}, 30*time.Second)
		if err != nil {
			t.Fatalf("usb list: %v", err)
		}
		if !resp.Success {
			t.Fatalf("usb list failed: %s", resp.Error)
		}
		var status runtimeUSBResponse
		if err := json.Unmarshal([]byte(resp.GetData()), &status); err != nil {
			t.Fatalf("decode usb list: %v\n%s", err, resp.GetData())
		}
		if status.List == nil {
			t.Fatalf("usb list missing payload: %+v", status)
		}
		if len(status.List.Controllers) == 0 {
			t.Fatalf("usb controllers empty: %+v", status.List)
		}
	})

	if !vm.linux {
		t.Run("shared-folder-pause-resume", func(t *testing.T) {
			status := statusResponse(t, vm)
			if !status.GetCanPause() {
				t.Skip("pause not supported for this VM")
			}

			original := LoadSharedFolders(vm.dir)
			restoreOriginal := func() {
				if err := saveSharedFolders(vm.dir, original); err != nil {
					t.Fatalf("restore shared folders: %v", err)
				}
				ctlDo(t, vm, &controlpb.ControlRequest{Type: "shared-folders-apply"})
				_, _ = mountSharedFoldersInGuest(vm.dir, defaultSharedFoldersMountPoint)
			}
			defer restoreOriginal()

			hostDir := t.TempDir()
			hostFile := filepath.Join(hostDir, "sentinel.txt")
			if err := os.WriteFile(hostFile, []byte("shared-folder-pause-resume\n"), 0644); err != nil {
				t.Fatalf("write host sentinel: %v", err)
			}

			entry, _, err := addSharedFolderEntry(vm.dir, hostDir, "pause-resume", false)
			if err != nil {
				t.Fatalf("addSharedFolderEntry(): %v", err)
			}

			ctlDo(t, vm, &controlpb.ControlRequest{Type: "shared-folders-apply"})
			if _, err := mountSharedFoldersInGuest(vm.dir, defaultSharedFoldersMountPoint); err != nil {
				t.Fatalf("mountSharedFoldersInGuest(): %v", err)
			}

			guestFile := filepath.Join(defaultSharedFoldersMountPoint, entry.Tag, "sentinel.txt")
			agentExecExpectCode(t, vm, 0, "/bin/test", "-f", guestFile)
			before := agentExec(t, vm, "/bin/cat", guestFile)
			if before != "shared-folder-pause-resume\n" {
				t.Fatalf("guest sentinel before pause = %q, want %q", before, "shared-folder-pause-resume\n")
			}

			ctlDo(t, vm, &controlpb.ControlRequest{Type: "pause"})
			waitVMState(t, vm, "paused", 30*time.Second)

			ctlDo(t, vm, &controlpb.ControlRequest{Type: "resume"})
			waitVMState(t, vm, "running", 30*time.Second)
			waitVMReady(t, vm, integrationVMReadyTimeout(vm, false))

			agentExecExpectCode(t, vm, 0, "/bin/test", "-d", defaultSharedFoldersMountPoint)
			agentExecExpectCode(t, vm, 0, "/bin/test", "-f", guestFile)
			after := agentExec(t, vm, "/bin/cat", guestFile)
			if after != "shared-folder-pause-resume\n" {
				t.Fatalf("guest sentinel after resume = %q, want %q", after, "shared-folder-pause-resume\n")
			}
		})
	}
}

func runtimeSurfaceDiskFileName(vm *testVM) string {
	if vm != nil && vm.linux {
		return "linux-disk.img"
	}
	return "disk.img"
}

func containsString(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}
