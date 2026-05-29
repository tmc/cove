//go:build integration && darwin && arm64

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func testRuntimeSurface(t *testing.T, vm *testVM) {
	t.Run("parallel-agent-exec", func(t *testing.T) {
		assertParallelAgentExec(t, vm, "agent-exec")
	})

	t.Run("parallel-agent-user-exec", func(t *testing.T) {
		requireUserAgent(t, vm)
		assertParallelAgentExec(t, vm, "agent-user-exec")
	})

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
			if !runtimeSurfaceContainsString(caps.GetCommands(), cmd) {
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
		disks := runtimeSurfaceDiskList(t, vm)
		if disks.Count == 0 || len(disks.Disks) == 0 {
			t.Fatalf("disk list empty: %+v", disks)
		}
		if disks.Disks[0].Index != 0 {
			t.Fatalf("first disk index = %d, want 0", disks.Disks[0].Index)
		}
		assertRuntimeSurfacePrimaryDiskImage(t, vm, disks.Disks[0])
	})

	t.Run("disk-resize-live", func(t *testing.T) {
		artifacts := newIntegrationArtifacts(t, "disk-resize-live")
		baseDisks := runtimeSurfaceDiskList(t, vm)
		artifacts.writeJSON("base-disk-list.json", baseDisks)
		if len(baseDisks.Disks) == 0 {
			t.Fatalf("disk list empty: %+v", baseDisks)
		}
		assertRuntimeSurfacePrimaryDiskImage(t, vm, baseDisks.Disks[0])

		clone := cloneTestVM(t, vm)
		artifacts.writeText("clone.txt", fmt.Sprintf("name=%s\ndir=%s\nlog=%s\n", clone.name, clone.dir, clone.logPath))

		disks := runtimeSurfaceDiskList(t, clone)
		artifacts.writeJSON("clone-disk-list-before.json", disks)
		if len(disks.Disks) == 0 {
			t.Fatalf("disk list empty: %+v", disks)
		}
		assertRuntimeSurfacePrimaryDiskImage(t, clone, disks.Disks[0])

		diskPath := filepath.Join(clone.dir, runtimeSurfaceDiskFileName(clone))
		before, err := os.Stat(diskPath)
		if err != nil {
			t.Fatalf("stat disk %q: %v", diskPath, err)
		}
		artifacts.writeText("host-disk-before.txt", fmt.Sprintf("path=%s\nsize=%d\n", diskPath, before.Size()))

		var beforeGuest macOSRuntimeDiskState
		if !clone.linux {
			beforeGuest = captureMacOSRuntimeDiskState(t, artifacts, clone, "before")
		}
		targetSize := uint64(before.Size()) + 512*1024*1024

		resp, err := ctlSendJSON(clone.sock, map[string]interface{}{
			"type": "disk",
			"data": map[string]any{
				"action":     "resize",
				"index":      0,
				"size_bytes": targetSize,
			},
		}, 5*time.Minute)
		artifacts.writeJSON("resize-response.json", resp)
		if err != nil {
			t.Fatalf("disk resize: %v", err)
		}
		if !resp.Success {
			if !clone.linux {
				_ = captureMacOSRuntimeDiskState(t, artifacts, clone, "after-failed")
				if runtimeSurfaceMacOSRecoveryBlocksAPFS(resp.Error) {
					t.Skipf("macOS APFS expansion unsupported for this guest partition layout: %s", resp.Error)
				}
			}
			t.Fatalf("disk resize failed: %s", resp.Error)
		}
		var resized RuntimeDiskMutationResponse
		if err := json.Unmarshal([]byte(resp.GetData()), &resized); err != nil {
			t.Fatalf("decode disk resize: %v\n%s", err, resp.GetData())
		}
		if resized.Index != 0 {
			t.Fatalf("resize index = %d, want 0", resized.Index)
		}

		if !waitRuntimeSurfaceDiskSize(t, diskPath, targetSize, 30*time.Second) {
			info, err := os.Stat(diskPath)
			if err != nil {
				t.Fatalf("stat resized disk %q: %v", diskPath, err)
			}
			t.Fatalf("disk size after resize = %d, want at least %d", info.Size(), targetSize)
		}
		after, err := os.Stat(diskPath)
		if err != nil {
			t.Fatalf("stat resized disk %q: %v", diskPath, err)
		}
		artifacts.writeText("host-disk-after.txt", fmt.Sprintf("path=%s\nsize=%d\ntarget=%d\n", diskPath, after.Size(), targetSize))
		if !clone.linux {
			if resized.GuestResize == nil || !resized.GuestResize.Attempted || !resized.GuestResize.Expanded {
				t.Fatalf("macOS disk resize did not report APFS expansion: %+v", resized.GuestResize)
			}
			afterGuest := captureMacOSRuntimeDiskState(t, artifacts, clone, "after")
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
			before := agentExecResult(t, vm, "/bin/cat", guestFile)
			if before.GetExitCode() != 0 {
				assertVirtioFSBoundedPermissionError(t, "cat", before)
				t.Skip("shared folder contents not readable in this guest")
			}
			if before.GetStdout() != "shared-folder-pause-resume\n" {
				t.Fatalf("guest sentinel before pause = %q, want %q", before.GetStdout(), "shared-folder-pause-resume\n")
			}

			ctlDo(t, vm, &controlpb.ControlRequest{Type: "pause"})
			waitVMState(t, vm, "paused", 30*time.Second)

			ctlDo(t, vm, &controlpb.ControlRequest{Type: "resume"})
			waitVMState(t, vm, "running", 30*time.Second)
			waitVMReady(t, vm, integrationVMReadyTimeout(vm, false))

			agentExecExpectCode(t, vm, 0, "/bin/test", "-d", defaultSharedFoldersMountPoint)
			agentExecExpectCode(t, vm, 0, "/bin/test", "-f", guestFile)
			after := agentExecResult(t, vm, "/bin/cat", guestFile)
			if after.GetExitCode() != 0 {
				assertVirtioFSBoundedPermissionError(t, "cat", after)
				t.Skip("shared folder contents not readable in this guest")
			}
			if after.GetStdout() != "shared-folder-pause-resume\n" {
				t.Fatalf("guest sentinel after resume = %q, want %q", after.GetStdout(), "shared-folder-pause-resume\n")
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

func runtimeSurfaceDiskList(t *testing.T, vm *testVM) RuntimeDiskListResponse {
	t.Helper()

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
	return disks
}

func assertRuntimeSurfacePrimaryDiskImage(t *testing.T, vm *testVM, disk RuntimeDiskInfo) {
	t.Helper()

	path := filepath.Join(vm.dir, runtimeSurfaceDiskFileName(vm))
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("stat primary disk %q: %v", path, err)
	}
	if disk.Kind != "disk-image" {
		t.Fatalf("disk 0 kind = %q, want disk-image for cove-managed primary disk %s", disk.Kind, path)
	}
	if disk.Path == "" {
		t.Fatalf("disk 0 path is empty, want %s", path)
	}
	if disk.FileSizeBytes == 0 {
		t.Fatalf("disk 0 file_size_bytes = 0, want current host file size")
	}
}

func waitRuntimeSurfaceDiskSize(t *testing.T, path string, want uint64, timeout time.Duration) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat resized disk %q: %v", path, err)
		}
		if uint64(info.Size()) >= want {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

type macOSRuntimeDiskState struct {
	PhysicalDiskBytes uint64 `json:"physical_disk_bytes"`
	ContainerBytes    uint64 `json:"container_bytes"`
	RootKBytes        uint64 `json:"root_kbytes"`
}

func captureMacOSRuntimeDiskState(t *testing.T, artifacts *integrationArtifacts, vm *testVM, phase string) macOSRuntimeDiskState {
	t.Helper()

	script := strings.Join([]string{
		"set +e",
		"echo '== diskutil-list ==' ; /usr/sbin/diskutil list",
		"echo '== diskutil-apfs-list ==' ; /usr/sbin/diskutil apfs list",
		"echo '== diskutil-info-root ==' ; /usr/sbin/diskutil info /",
		"echo '== diskutil-info-disk0 ==' ; /usr/sbin/diskutil info /dev/disk0",
		"echo '== df-root ==' ; /bin/df -k /",
	}, "\n")
	result := agentExecResultTimeoutTB(t, vm, 3*time.Minute, "/bin/sh", "-lc", script)
	stdout := result.GetStdout()
	text := "exit=" + strconv.FormatInt(int64(result.GetExitCode()), 10) + "\n" +
		"stdout:\n" + stdout + "\n" +
		"stderr:\n" + result.GetStderr()
	artifacts.writeText("macos-disk-state-"+phase+".txt", text)
	if result.GetExitCode() != 0 {
		t.Fatalf("capture macOS disk state %s: exit %d\nstdout:\n%s\nstderr:\n%s", phase, result.GetExitCode(), stdout, result.GetStderr())
	}

	state := macOSRuntimeDiskState{
		PhysicalDiskBytes: parseDiskutilByteLine(t, runtimeSurfaceOutputSection(stdout, "== diskutil-info-disk0 =="), "Disk Size:"),
		ContainerBytes:    parseDiskutilByteLine(t, runtimeSurfaceOutputSection(stdout, "== diskutil-info-root =="), "Container Total Space:"),
		RootKBytes:        parseDFRootKBytes(t, stdout),
	}
	artifacts.writeJSON("macos-disk-state-"+phase+".json", state)
	return state
}

func runtimeSurfaceOutputSection(text, marker string) string {
	idx := strings.Index(text, marker)
	if idx < 0 {
		return text
	}
	section := text[idx+len(marker):]
	if next := strings.Index(section, "\n== "); next >= 0 {
		section = section[:next]
	}
	return section
}

func parseDiskutilByteLine(t *testing.T, text, label string) uint64 {
	t.Helper()

	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, label) {
			continue
		}
		close := strings.Index(line, " Bytes")
		if close < 0 {
			continue
		}
		open := strings.LastIndex(line[:close], "(")
		if open < 0 {
			continue
		}
		raw := strings.TrimSpace(line[open+1 : close])
		raw = strings.ReplaceAll(raw, ",", "")
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			t.Fatalf("parse %s byte count from %q: %v", label, line, err)
		}
		return n
	}
	t.Fatalf("missing %s in guest disk state:\n%s", label, text)
	return 0
}

func parseDFRootKBytes(t *testing.T, text string) uint64 {
	t.Helper()

	inDF := false
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "== df-root ==") {
			inDF = true
			continue
		}
		if !inDF {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] == "Filesystem" {
			continue
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			t.Fatalf("parse df root blocks from %q: %v", line, err)
		}
		return n
	}
	t.Fatalf("missing df -k / output in guest disk state:\n%s", text)
	return 0
}

func runtimeSurfaceMacOSRecoveryBlocksAPFS(text string) bool {
	return strings.Contains(text, "Recovery partition blocks APFS expansion")
}

func runtimeSurfaceContainsString(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}

func assertParallelAgentExec(t *testing.T, vm *testVM, reqType string) {
	t.Helper()

	type testCase struct {
		name string
		args []string
	}
	cases := []testCase{
		{name: "alpha", args: []string{"/bin/sh", "-lc", "sleep 2; echo alpha"}},
		{name: "beta", args: []string{"/bin/sh", "-lc", "sleep 2; echo beta"}},
		{name: "gamma", args: []string{"/bin/sh", "-lc", "sleep 2; echo gamma"}},
	}

	type result struct {
		name string
		resp *controlpb.AgentExecResponse
		err  error
	}

	start := time.Now()
	results := make(chan result, len(cases))
	var wg sync.WaitGroup
	for _, tc := range cases {
		tc := tc
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := &controlpb.ControlRequest{
				Type:      reqType,
				AuthToken: vm.token,
				Command: &controlpb.ControlRequest_AgentExec{
					AgentExec: &controlpb.AgentExecCommand{Args: tc.args},
				},
			}
			resp, err := ctlSendRequest(vm.sock, req, 30*time.Second, reqType)
			if err != nil {
				results <- result{name: tc.name, err: err}
				return
			}
			if !resp.Success {
				results <- result{name: tc.name, err: fmt.Errorf("%s", resp.Error)}
				return
			}
			results <- result{name: tc.name, resp: resp.GetAgentExecResult()}
		}()
	}
	wg.Wait()
	close(results)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("%s elapsed = %s, want parallel completion under 5s", reqType, elapsed)
	}

	for res := range results {
		if res.err != nil {
			t.Fatalf("%s %s: %v", reqType, res.name, res.err)
		}
		if res.resp == nil {
			t.Fatalf("%s %s: missing typed response", reqType, res.name)
		}
		if res.resp.GetExitCode() != 0 {
			t.Fatalf("%s %s: exit %d\nstdout:\n%s\nstderr:\n%s", reqType, res.name, res.resp.GetExitCode(), res.resp.GetStdout(), res.resp.GetStderr())
		}
		if got := res.resp.GetStdout(); got != res.name+"\n" {
			t.Fatalf("%s %s stdout = %q, want %q", reqType, res.name, got, res.name+"\n")
		}
	}
}
