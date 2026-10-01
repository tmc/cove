package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestRefreshSharedFoldersInGuestWithUnchangedTags(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")

	vmDir := shortSharedFolderVMDir(t)
	hostA := filepath.Join(t.TempDir(), "alpha")
	hostB := filepath.Join(t.TempDir(), "beta")
	if err := os.MkdirAll(hostA, 0755); err != nil {
		t.Fatalf("mkdir alpha: %v", err)
	}
	if err := os.MkdirAll(hostB, 0755); err != nil {
		t.Fatalf("mkdir beta: %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostA, "alpha", false); err != nil {
		t.Fatalf("addSharedFolderEntry(alpha): %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostB, "beta", false); err != nil {
		t.Fatalf("addSharedFolderEntry(beta): %v", err)
	}

	verify := serveSharedFolderControlSteps(t, vmDir, "test-token", []sharedFolderControlStep{
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
					ExitCode: 0,
					Stdout:   "alpha\nbeta\n",
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
	})

	mounted, err := refreshSharedFoldersInGuest(vmDir, defaultSharedFoldersMountPoint, sharedFolderMountTimeouts{
		agentPing: 200 * time.Millisecond,
		mkdir:     200 * time.Millisecond,
		mounts:    200 * time.Millisecond,
		listTags:  200 * time.Millisecond,
		unmount:   200 * time.Millisecond,
		mount:     200 * time.Millisecond,
	}, true)
	verify()

	if err != nil {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts(): %v", err)
	}
	if !mounted {
		t.Fatalf("mountSharedFoldersInGuestWithTimeouts() = false, want true after remount")
	}
}
