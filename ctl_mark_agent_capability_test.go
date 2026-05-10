package main

import (
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestMarkAgentCapabilityForCommandNilResp(t *testing.T) {
	if err := markAgentCapabilityForCommand("/tmp/sock", "agent-ping", nil); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestMarkAgentCapabilityForCommandFailedResp(t *testing.T) {
	resp := &controlpb.ControlResponse{Success: false}
	if err := markAgentCapabilityForCommand("/tmp/sock", "agent-ping", resp); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestMarkAgentCapabilityForCommandUnknownCmdType(t *testing.T) {
	resp := &controlpb.ControlResponse{Success: true}
	if err := markAgentCapabilityForCommand("/tmp/sock", "frobnicate", resp); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}
