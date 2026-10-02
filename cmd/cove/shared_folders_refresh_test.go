package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestRefreshSharedFoldersListingFailureRetainsMount(t *testing.T) {
	for _, tt := range []struct {
		name, detail string
		privacy      bool
	}{
		{"permission", "Operation not permitted", true},
		{"permission with stale phrase", "ls: Stale file handle: Operation not permitted", true},
		{"access", "Permission denied", true},
		{"other", "unexpected listing failure", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vmDir := shortSharedFolderVMDir(t)
			host := t.TempDir()
			if _, _, err := addSharedFolderEntry(vmDir, host, "alpha", true); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(vmDir, "shared_folders.json"))
			if err != nil {
				t.Fatal(err)
			}
			execResponse := func(r *controlpb.AgentExecResponse) *controlpb.ControlResponse {
				return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: r}}
			}
			verify := serveSharedFolderControlSteps(t, vmDir, "test-token", []sharedFolderControlStep{
				{wantType: "agent-ping", resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentPing{AgentPing: &controlpb.AgentPingResponse{Version: "test"}}}},
				{wantType: "agent-exec", wantArgs: []string{"mkdir", "-p", defaultSharedFoldersMountPoint}, resp: execResponse(&controlpb.AgentExecResponse{})},
				{wantType: "agent-exec-auto", wantArgs: []string{"mount"}, resp: execResponse(&controlpb.AgentExecResponse{Stdout: "virtio-fs on " + defaultSharedFoldersMountPoint + " (AppleVirtIOFS)\n"})},
				{wantType: "agent-exec-auto", wantArgs: []string{"ls", "-1", defaultSharedFoldersMountPoint}, resp: execResponse(&controlpb.AgentExecResponse{ExitCode: 1, Stderr: tt.detail})},
			})
			mounted, err := refreshSharedFoldersInGuest(vmDir, defaultSharedFoldersMountPoint, defaultSharedFolderMountTimeouts(), true)
			verify()
			if mounted || err == nil || !strings.Contains(err.Error(), tt.detail) {
				t.Fatalf("refresh = %v, %v; want original listing error", mounted, err)
			}
			if strings.Contains(err.Error(), "Privacy & Security") != tt.privacy {
				t.Fatalf("unexpected permission guidance: %v", err)
			}
			after, readErr := os.ReadFile(filepath.Join(vmDir, "shared_folders.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(after) != string(before) {
				t.Fatal("shared folder configuration changed")
			}
		})
	}
}
