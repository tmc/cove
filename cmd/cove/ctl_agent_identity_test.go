package main

import (
	"encoding/json"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestCtlAgentIdentityPreservesRuntime(t *testing.T) {
	response := &controlpb.ControlResponse{Success: true, Data: `{"runtimeVersion":{"commit":"previous-runtime"},"agentVersions":{"daemon":"guest","user":"unknown"},"readiness":{"daemon":true,"user":false,"unlocked":"unknown"}}`}
	got := ctlEnrichResponseForPrint("", response, "agent-status")
	var status map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got.Data), &status); err != nil {
		t.Fatal(err)
	}
	var runtime map[string]string
	if err := json.Unmarshal(status["runtimeVersion"], &runtime); err != nil {
		t.Fatal(err)
	}
	if runtime["commit"] != "previous-runtime" {
		t.Fatalf("runtime identity replaced: %v", runtime)
	}
	if len(status["cliVersion"]) == 0 {
		t.Fatal("missing separate CLI identity")
	}
	if got.GetMessage().GetMessage() != got.Data {
		t.Fatal("message and data disagree")
	}
	if response == got || response.Data == got.Data {
		t.Fatal("source response modified")
	}
}
