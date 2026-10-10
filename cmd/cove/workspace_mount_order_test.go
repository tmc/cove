package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func workspaceMountResult(stdout string, exit int32) *controlpb.ControlResponse {
	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{Stdout: stdout, ExitCode: exit}}}
}

func TestWorkspaceRuntimeMountOrdering(t *testing.T) {
	want := []string{"-vm", "workspace", "-headless", "-auto-mount-shared-folders=false", "run"}
	if got := workspaceRuntimeArgs("workspace"); !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime args %v, want %v", got, want)
	}
}

func TestWorkspaceDefersSharedFolderReconciliation(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")
	dir := shortSharedFolderVMDir(t)
	if _, _, err := addSharedFolderEntry(dir, t.TempDir(), "output", false); err != nil {
		t.Fatal(err)
	}
	old := autoMountSharedFolders
	autoMountSharedFolders = false
	defer func() { autoMountSharedFolders = old }()
	verify := serveSharedFolderControlSteps(t, dir, "token", nil)
	reconcileAllMounts(context.Background(), nil, nil, dir)
	verify()
}

func TestWorkspaceFirstMountUsesDiscoveredOwner(t *testing.T) {
	t.Setenv(controlTokenEnvVar, "")
	dir := shortSharedFolderVMDir(t)
	if err := vmconfig.Save(dir, &vmconfig.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hw.model"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := addSharedFolderEntry(dir, t.TempDir(), "output", false); err != nil {
		t.Fatal(err)
	}
	verify := serveSharedFolderControlSteps(t, dir, "token", []sharedFolderControlStep{
		{wantType: "agent-user-exec", wantArgs: []string{"/usr/bin/id", "-u"}, resp: workspaceMountResult("502\n", 0)},
		{wantType: "agent-user-exec", wantArgs: []string{"/usr/bin/id", "-g"}, resp: workspaceMountResult("20\n", 0)},
		{wantType: "agent-ping", resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentPing{AgentPing: &controlpb.AgentPingResponse{Version: "test"}}}},
		{wantType: "agent-exec", wantArgs: []string{"mkdir", "-p", defaultSharedFoldersMountPoint}, resp: workspaceMountResult("", 0)},
		{wantType: "agent-exec-auto", wantArgs: []string{"mount"}, resp: workspaceMountResult("", 0)},
		{wantType: "agent-exec", wantArgs: []string{"mount_virtiofs", "-u", "502", "-g", "20", SharedFoldersVirtioFSTag, defaultSharedFoldersMountPoint}, resp: workspaceMountResult("", 0)},
	})
	changed, err := configureWorkspaceGuestOwner(context.Background(), workspacePlan{GuestOS: "darwin"}, dir)
	if err != nil || !changed {
		t.Fatalf("owner changed %v: %v", changed, err)
	}
	mounted, err := refreshSharedFoldersInGuest(dir, defaultSharedFoldersMountPoint, defaultSharedFolderMountTimeouts(), changed)
	if err != nil || !mounted {
		t.Fatalf("first mount %v: %v", mounted, err)
	}
	verify()
}
