package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func linuxTestVMDir(t *testing.T) string {
	t.Helper()
	dir := shortSharedFolderVMDir(t)
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), nil, 0644); err != nil {
		t.Fatalf("write linux marker: %v", err)
	}
	return dir
}

func TestSIPRefusesLinuxBeforeCreatingDisk(t *testing.T) {
	oldVMDir := vmDir
	vmDir = linuxTestVMDir(t)
	t.Cleanup(func() { vmDir = oldVMDir })

	err := handleSIPCommand([]string{"create-disk"})
	if err == nil {
		t.Fatal("handleSIPCommand() error = nil, want linux refusal")
	}
	if !strings.Contains(err.Error(), "sip is only supported for macOS VMs") {
		t.Fatalf("handleSIPCommand() error = %v", err)
	}
	if _, statErr := os.Stat(RecoveryDiskPath(vmDir)); !os.IsNotExist(statErr) {
		t.Fatalf("recovery disk stat err = %v, want not exist", statErr)
	}
}

func TestSetupAssistRefusesLinuxBeforeAutomation(t *testing.T) {
	vmDir := linuxTestVMDir(t)
	err := ctlSetupAssist(GetControlSocketPathForVM(vmDir), "desk", "secret")
	if err == nil {
		t.Fatal("ctlSetupAssist() error = nil, want linux refusal")
	}
	if !strings.Contains(err.Error(), "setup-assist is only supported for macOS guests") {
		t.Fatalf("ctlSetupAssist() error = %v", err)
	}
}

func TestLinuxResetPasswordUsesChpasswd(t *testing.T) {
	vmDir := linuxTestVMDir(t)
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "agent-exec",
			wantArgs: []string{
				"sh",
				"-lc",
				linuxResetPasswordScript("desk", "new secret"),
			},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
				}},
			},
		},
	})
	defer stop()

	out := captureStdout(t, func() error {
		return ctlResetPasswordForVM(vmSelection{Directory: vmDir, Name: "linux"}, GetControlSocketPathForVM(vmDir), time.Second, "desk", "new secret")
	})
	if !strings.Contains(out, "Password reset for desk (via Linux guest agent)") {
		t.Fatalf("output = %q", out)
	}
}
