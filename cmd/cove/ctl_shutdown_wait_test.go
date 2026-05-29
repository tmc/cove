package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestParseCtlShutdownWaitArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		allowForce bool
		wantForce  bool
		wantWait   time.Duration
		wantErr    string
	}{
		{name: "none"},
		{name: "bare wait", args: []string{"--wait"}, wantWait: ctlShutdownDefaultWait},
		{name: "wait value", args: []string{"--wait", "12s"}, wantWait: 12 * time.Second},
		{name: "wait equals", args: []string{"--wait=250ms"}, wantWait: 250 * time.Millisecond},
		{name: "force wait", args: []string{"force", "--wait=1s"}, allowForce: true, wantForce: true, wantWait: time.Second},
		{name: "force disallowed", args: []string{"force"}, wantErr: `unexpected argument "force"`},
		{name: "unknown", args: []string{"soon"}, allowForce: true, wantErr: `unexpected argument "soon"`},
		{name: "zero wait", args: []string{"--wait=0"}, wantErr: "wait duration must be positive"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotForce, gotWait, err := parseCtlShutdownWaitArgs(tc.args, tc.allowForce)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCtlShutdownWaitArgs() error = %v", err)
			}
			if gotForce != tc.wantForce || gotWait != tc.wantWait {
				t.Fatalf("force, wait = %v, %s; want %v, %s", gotForce, gotWait, tc.wantForce, tc.wantWait)
			}
		})
	}
}

func TestCtlRequestStopWaitStops(t *testing.T) {
	oldPoll := ctlShutdownPollInterval
	ctlShutdownPollInterval = time.Millisecond
	t.Cleanup(func() { ctlShutdownPollInterval = oldPoll })

	vmDir := shortSharedFolderVMDir(t)
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "request-stop",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    "stop requested",
			},
		},
		{
			wantType: "status",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_Status{Status: &controlpb.StatusResponse{
					State: "stopped",
				}},
			},
		},
	})
	defer stop()

	out := captureStdout(t, func() error {
		return ctlCommand([]string{"-socket", GetControlSocketPathForVM(vmDir), "request-stop", "--wait=50ms"})
	})
	if !strings.Contains(out, "stop requested") || !strings.Contains(out, "VM state: stopped") {
		t.Fatalf("output = %q", out)
	}
}

func TestCtlAgentShutdownWaitTimeoutReportsForceStop(t *testing.T) {
	oldPoll := ctlShutdownPollInterval
	ctlShutdownPollInterval = time.Millisecond
	t.Cleanup(func() { ctlShutdownPollInterval = oldPoll })

	vmDir := shortSharedFolderVMDir(t)
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "agent-shutdown",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    "shutdown initiated",
			},
		},
		{
			wantType: "status",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_Status{Status: &controlpb.StatusResponse{
					State: "running",
				}},
			},
		},
	})
	defer stop()

	err := ctlCommand([]string{"-socket", GetControlSocketPathForVM(vmDir), "agent-shutdown", "force", "--wait=1ns"})
	if err == nil {
		t.Fatal("ctlCommand() succeeded, want timeout")
	}
	if got := err.Error(); !strings.Contains(got, "shutdown requested but VM still running after 1ns") || !strings.Contains(got, "cove ctl -socket") || !strings.Contains(got, "stop") {
		t.Fatalf("error = %q", got)
	}
}

func TestRunningRuntimeListFieldsUsesOwnerServerInfo(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "server-info",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    `{"pid":82431,"start_source":"cove run","started_at":"` + time.Now().Add(-2*time.Minute).Format(time.RFC3339) + `"}`,
			},
		},
	})
	defer stop()

	uptime, note := runningRuntimeListFields(vmDir)
	if uptime == "-" {
		t.Fatalf("uptime = %q, want running duration", uptime)
	}
	if !strings.Contains(note, "owner pid=82431") || !strings.Contains(note, "cove run") {
		t.Fatalf("note = %q", note)
	}
}

func TestCtlServerInfoEnrichesOldOwnerSchema(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "server-info",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    fmt.Sprintf(`{"pid":%d,"version":"old","commit":"old"}`, os.Getpid()),
			},
		},
	})
	defer stop()

	out := captureStdout(t, func() error {
		return ctlCommand([]string{"-socket", GetControlSocketPathForVM(vmDir), "server-info"})
	})
	for _, want := range []string{`"pid":`, `"ppid":`, `"command":`, `"start_source":`} {
		if !strings.Contains(out, want) {
			t.Fatalf("server-info output missing %s:\n%s", want, out)
		}
	}
}

func TestCtlStatusEnrichesOldOwnerSchema(t *testing.T) {
	vmDir := shortSharedFolderVMDir(t)
	stop := serveSharedFolderControlSteps(t, vmDir, "token", []sharedFolderControlStep{
		{
			wantType: "status",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    `{"state":"running"}`,
			},
		},
		{
			wantType: "server-info",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    fmt.Sprintf(`{"pid":%d,"version":"old","commit":"old"}`, os.Getpid()),
			},
		},
	})
	defer stop()

	out := captureStdout(t, func() error {
		return ctlCommand([]string{"-socket", GetControlSocketPathForVM(vmDir), "status"})
	})
	for _, want := range []string{`"ownerPID":`, `"ownerPPID":`, `"ownerCommand":`, `"startSource":`} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output missing %s:\n%s", want, out)
		}
	}
}
