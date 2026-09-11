package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestDeadOrStaleMountDetection(t *testing.T) {
	tests := []struct {
		name     string
		errText  string
		wantDead bool
	}{
		{"io error", "ls: /Volumes/My Shared Files: Input/output error", true},
		{"device not configured", "ls: /Volumes/My Shared Files: Device not configured", true},
		{"stale file handle", "ls: /Volumes/My Shared Files: Stale file handle", true},
		{"stale nfs handle", "ls: /Volumes/My Shared Files: Stale NFS file handle", true},
		{"transport disconnected", "ls: /mnt/work: Transport endpoint is not connected", true},
		{"bad file descriptor", "ls: /mnt/work: Bad file descriptor", true},
		{"connection reset", "ls: /Volumes/My Shared Files: Connection reset by peer", true},
		{"connection abort", "ls: /mnt/work: Software caused connection abort", true},
		{"host down", "ls: /mnt/work: Host is down", true},
		{"normal permission", "mkdir: .cove-write-probe: Operation not permitted", false},
		{"normal not found", "ls: nonexistent: No such file or directory", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDeadOrStaleMountError(tt.errText)
			if got != tt.wantDead {
				t.Fatalf("isDeadOrStaleMountError(%q) = %v, want %v", tt.errText, got, tt.wantDead)
			}
		})
	}
}

func TestMountSharedFoldersInGuestRecoversDeadMount(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")

	vmDir := shortSharedFolderVMDir(t)
	hostDir := filepath.Join(t.TempDir(), "alpha")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostDir, "alpha", false); err != nil {
		t.Fatalf("addSharedFolderEntry(alpha): %v", err)
	}

	// Steps:
	// 1. ping agent -> ok
	// 2. mkdir -p /Volumes/My Shared Files -> ok
	// 3. mount -> reports mounted on /Volumes/My Shared Files
	// 4. ls -1 /Volumes/My Shared Files -> returns error (Input/output error)
	// 5. umount /Volumes/My Shared Files -> ok
	// 6. mkdir -p /Volumes/My Shared Files -> ok
	// 7. mount_virtiofs -> ok
	steps := []sharedFolderControlStep{
		{
			wantType: "agent-ping",
			resp:     &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentPing{AgentPing: &controlpb.AgentPingResponse{Version: "test-agent"}}},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mkdir", "-p", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec-auto",
			wantArgs: []string{"mount"},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
					Stdout:   "/dev/virtiofs on " + defaultSharedFoldersMountPoint + " (virtiofs)\n",
				}},
			},
		},
		{
			wantType: "agent-exec-auto",
			wantArgs: []string{"ls", "-1", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 1,
					Stderr:   "ls: " + defaultSharedFoldersMountPoint + ": Input/output error\n",
				}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"umount", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mkdir", "-p", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"mount_virtiofs", SharedFoldersVirtioFSTag, defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
	}

	verify := serveSharedFolderControlSteps(t, vmDir, "test-token", steps)
	mounted, err := mountSharedFoldersInGuestWithTimeouts(vmDir, defaultSharedFoldersMountPoint, sharedFolderMountTimeouts{
		agentPing: 200 * time.Millisecond,
		mkdir:     200 * time.Millisecond,
		mounts:    200 * time.Millisecond,
		listTags:  200 * time.Millisecond,
		unmount:   200 * time.Millisecond,
		mount:     200 * time.Millisecond,
	})
	verify()

	if err != nil {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts(): %v", err)
	}
	if !mounted {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts() = false, want true after recovering dead mount")
	}
}

func TestUnmountGuestMountPointForceFallback(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")

	vmDir := shortSharedFolderVMDir(t)
	steps := []sharedFolderControlStep{
		{
			wantType: "agent-exec",
			wantArgs: []string{"umount", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 1,
					Stderr:   "umount: " + defaultSharedFoldersMountPoint + ": Resource busy\n",
				}},
			},
		},
		{
			wantType: "agent-exec",
			wantArgs: []string{"umount", "-f", defaultSharedFoldersMountPoint},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
				}},
			},
		},
	}

	verify := serveSharedFolderControlSteps(t, vmDir, "test-token", steps)
	client := NewControlClient(GetControlSocketPathForVM(vmDir))
	err := unmountGuestMountPointWithTimeouts(client, defaultSharedFoldersMountPoint, false, 200*time.Millisecond)
	verify()

	if err != nil {
		t.Fatalf("unmountGuestMountPointWithTimeouts() error = %v, want successful force unmount", err)
	}
}

func TestSharedFoldersRuntimeStatusAbsentPaths(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	hostExists := filepath.Join(t.TempDir(), "present")
	hostMissing := filepath.Join(t.TempDir(), "missing-drive")
	if err := os.MkdirAll(hostExists, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(hostMissing, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, _, err := addSharedFolderEntry(vmDir, hostExists, "present", false); err != nil {
		t.Fatalf("add present: %v", err)
	}
	missingEntry, _, err := addSharedFolderEntry(vmDir, hostMissing, "missing", false)
	if err != nil {
		t.Fatalf("add missing: %v", err)
	}

	// Simulate host volume unmount/drop
	if err := os.RemoveAll(hostMissing); err != nil {
		t.Fatalf("remove hostMissing: %v", err)
	}

	s := NewControlServerWithVMDir("/tmp/test-resilience.sock", vmDir)
	status := s.sharedFoldersRuntimeStatus()
	if len(status.AbsentPaths) != 1 {
		t.Fatalf("AbsentPaths = %v, want 1 absent path", status.AbsentPaths)
	}
	if status.AbsentPaths[0] != missingEntry.Path {
		t.Fatalf("AbsentPaths[0] = %q, want %q", status.AbsentPaths[0], missingEntry.Path)
	}
}

func TestSharedFolderStatusReportsAbsentHostPath(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	hostMissing := filepath.Join(t.TempDir(), "absent-disk")
	if err := os.MkdirAll(hostMissing, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostMissing, "absent", false); err != nil {
		t.Fatalf("add absent: %v", err)
	}

	// Simulate host volume drop
	if err := os.RemoveAll(hostMissing); err != nil {
		t.Fatalf("remove hostMissing: %v", err)
	}

	out := captureStdout(t, func() error {
		return sharedFolderStatus(vmDir, defaultSharedFoldersMountPoint)
	})

	if !strings.Contains(out, "[host path absent]") {
		t.Fatalf("output missing '[host path absent]':\n%s", out)
	}
}

func TestSharedFolderProbeHintStaleMount(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	res := sharedFolderProbeResult{Detail: "ls: /Volumes/My Shared Files/work: Input/output error"}
	got := sharedFolderProbeHint(vmDir, "/Volumes/My Shared Files/work", res)
	if !strings.Contains(got, "mount point is stale or dead") {
		t.Fatalf("sharedFolderProbeHint() = %q, want stale mount hint", got)
	}
}

func TestMountMatchesExpectedTags(t *testing.T) {
	tests := []struct {
		name    string
		listing string
		tags    []string
		want    bool
	}{
		{"exact match", "alpha\nbeta\n", []string{"alpha", "beta"}, true},
		{"exact match different order", "beta\nalpha\n", []string{"alpha", "beta"}, true},
		{"missing tag in guest", "alpha\n", []string{"alpha", "beta"}, false},
		{"extra tag in guest", "alpha\nbeta\ngamma\n", []string{"alpha", "beta"}, false},
		{"empty both", "", nil, true},
		{"empty guest with expected tags", "", []string{"alpha"}, false},
		{"tags in guest with empty expected", "alpha\n", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mountMatchesExpectedTags(tt.listing, tt.tags)
			if got != tt.want {
				t.Fatalf("mountMatchesExpectedTags(%q, %v) = %v, want %v", tt.listing, tt.tags, got, tt.want)
			}
		})
	}
}
