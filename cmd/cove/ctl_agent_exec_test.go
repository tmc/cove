package main

import (
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestCtlAgentExecUsesAutoRouteByDefault(t *testing.T) {
	for _, cmd := range []string{"exec", "agent-exec"} {
		t.Run(cmd, func(t *testing.T) {
			vmDir := shortSharedFolderVMDir(t)
			stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
				{
					wantType: "agent-exec-auto",
					wantArgs: []string{"ls", "/etc/os-release"},
					resp: &controlpb.ControlResponse{
						Success: true,
						Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
							ExitCode: 0,
							Stdout:   "PRETTY_NAME=Ubuntu\n",
						}},
					},
				},
			})
			defer stop()

			if err := ctlCommand([]string{"-socket", GetControlSocketPathForVM(vmDir), cmd, "ls", "/etc/os-release"}); err != nil {
				t.Fatalf("ctlCommand() error = %v", err)
			}
		})
	}
}

func TestCtlAgentExecDaemonFlagForcesDaemon(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "agent-exec",
			wantArgs: []string{"ls", "/etc/os-release"},
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 0,
				}},
			},
		},
	})
	defer stop()

	if err := ctlCommand([]string{"-socket", GetControlSocketPathForVM(vmDir), "agent-exec", "--daemon", "ls", "/etc/os-release"}); err != nil {
		t.Fatalf("ctlCommand() error = %v", err)
	}
}

func TestCtlExecStreamAliasUsesStreamingTransport(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantType string
		wantArgs []string
	}{
		{
			name:     "auto route",
			args:     []string{"exec", "--stream", "tail", "-f", "/var/log/system.log"},
			wantType: "agent-exec-stream-auto",
			wantArgs: []string{"tail", "-f", "/var/log/system.log"},
		},
		{
			name:     "daemon",
			args:     []string{"exec", "--stream", "--daemon", "whoami"},
			wantType: "agent-exec-stream",
			wantArgs: []string{"whoami"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vmDir := shortSharedFolderVMDir(t)
			stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
				{
					wantType: tc.wantType,
					wantArgs: tc.wantArgs,
					resp: &controlpb.ControlResponse{
						Success: true,
						Data:    `{"done":true}`,
					},
				},
			})
			defer stop()

			args := append([]string{"-socket", GetControlSocketPathForVM(vmDir)}, tc.args...)
			if err := ctlCommand(args); err != nil {
				t.Fatalf("ctlCommand() error = %v", err)
			}
		})
	}
}

func TestCtlAgentExecAutoIsInternal(t *testing.T) {
	err := ctlCommand([]string{"-socket", "/tmp/missing.sock", "agent-exec-auto", "whoami"})
	if err == nil {
		t.Fatal("ctlCommand() succeeded, want error")
	}
	if got := err.Error(); got != `agent-exec-auto is an internal control request; use "exec" or "agent-exec"` {
		t.Fatalf("error = %q", got)
	}
}

func TestCtlSharedFoldersRuntimeStatus(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "shared-folders-runtime-status",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    `{"running":true,"virtiofs":true,"message":"shared folders VirtioFS device present"}`,
			},
		},
	})
	defer stop()

	if err := ctlCommand([]string{"-socket", GetControlSocketPathForVM(vmDir), "shared-folders-runtime-status"}); err != nil {
		t.Fatalf("ctlCommand() error = %v", err)
	}
}
