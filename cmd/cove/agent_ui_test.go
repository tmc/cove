package main

import (
	"bytes"
	"strings"
	"testing"

	pb "github.com/tmc/cove/proto/agentpb"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestAgentUIEpochInvalidation(t *testing.T) {
	server := &ControlServer{}
	first, err := server.uiEpoch()
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := server.uiEpoch()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 || first != repeated {
		t.Fatalf("epoch=%q repeat=%q", first, repeated)
	}
	server.invalidateUISession()
	next, err := server.uiEpoch()
	if err != nil {
		t.Fatal(err)
	}
	if next == first {
		t.Fatal("restore epoch retained")
	}
}

func TestAgentUIUnavailableAndUnsupported(t *testing.T) {
	previousLinux, previousWindows := linuxMode, windowsMode
	t.Cleanup(func() { linuxMode = previousLinux; windowsMode = previousWindows })
	for _, name := range []string{"mac-unavailable", "linux", "windows"} {
		t.Run(name, func(t *testing.T) {
			linuxMode = name == "linux"
			windowsMode = name == "windows"
			server := &ControlServer{}
			request := &controlpb.ControlRequest{Type: "agent-ui-status", Command: &controlpb.ControlRequest_AgentUi{AgentUi: &pb.UIRequest{}}}
			response := server.handleAgentUI(request)
			if !response.Success || response.GetAgentUi() == nil {
				t.Fatalf("response=%v", response)
			}
			want := "unsupported"
			if name == "mac-unavailable" {
				want = "unavailable"
			}
			if response.GetAgentUi().Status.State != want {
				t.Fatalf("state=%s want=%s", response.GetAgentUi().Status.State, want)
			}
		})
	}
}

func TestUICommandRejectsBoundsBeforeGuestAccess(t *testing.T) {
	for _, args := range [][]string{{"inspect", "-vm", "missing", "-pid", "-1"}, {"inspect", "-vm", "missing", "-pid", "1", "-nodes", "1025"}, {"find", "-vm", "missing", "-pid", "1"}, {"inspect", "-vm", "missing", "-pid", "1", "-timeout", "1h"}} {
		err := handleUICommand(commandEnv{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, args)
		if err == nil || strings.Contains(err.Error(), "not found") {
			t.Fatalf("args=%v error=%v", args, err)
		}
	}
}
