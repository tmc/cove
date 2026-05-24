package controlclient

import (
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

// TestFormatOperationsResponseSingleOp verifies that ctl operations get/wait
// renders the proto OperationInfo fields rather than falling through to the
// legacy "OK" path. Regression for v0.1.0 where operations get/wait/list
// printed only "OK" because the CLI ignored Result.Operation.
func TestFormatOperationsResponseSingleOp(t *testing.T) {
	op := &controlpb.OperationInfo{
		Id:        "op_07f8a057",
		Resource:  "snapshots/smoke-async",
		Status:    "succeeded",
		CreatedAt: "2026-04-26T12:00:00Z",
		UpdatedAt: "2026-04-26T12:00:01Z",
	}
	resp := &controlpb.ControlResponse{
		Success: true,
		Result:  &controlpb.ControlResponse_Operation{Operation: op},
	}

	got := FormatOperationsResponse(resp)
	for _, want := range []string{"op_07f8a057", "succeeded", "snapshots/smoke-async", "2026-04-26T12:00:00Z"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q; got:\n%s", want, got)
		}
	}
}

func TestFormatOperationsResponseFailedWithError(t *testing.T) {
	op := &controlpb.OperationInfo{
		Id:           "op_dead",
		Resource:     "snapshots/bad",
		Status:       "failed",
		ErrorCode:    "internal",
		ErrorMessage: "disk full",
	}
	resp := &controlpb.ControlResponse{
		Success: true,
		Result:  &controlpb.ControlResponse_Operation{Operation: op},
	}

	got := FormatOperationsResponse(resp)
	for _, want := range []string{"op_dead", "failed", "internal", "disk full"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q; got:\n%s", want, got)
		}
	}
}

func TestFormatOperationsResponseList(t *testing.T) {
	resp := &controlpb.ControlResponse{
		Success: true,
		Result: &controlpb.ControlResponse_OperationsList{
			OperationsList: &controlpb.OperationsListResponse{
				Operations: []*controlpb.OperationInfo{
					{Id: "op_1", Resource: "snapshots/a", Status: "succeeded", UpdatedAt: "2026-04-26T12:00:00Z"},
					{Id: "op_2", Resource: "snapshots/b", Status: "running", UpdatedAt: "2026-04-26T12:00:05Z"},
				},
			},
		},
	}

	got := FormatOperationsResponse(resp)
	for _, want := range []string{"op_1", "op_2", "succeeded", "running", "snapshots/a", "snapshots/b", "ID", "STATUS"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered output missing %q; got:\n%s", want, got)
		}
	}
}

func TestFormatOperationsResponseEmptyList(t *testing.T) {
	resp := &controlpb.ControlResponse{
		Success: true,
		Result: &controlpb.ControlResponse_OperationsList{
			OperationsList: &controlpb.OperationsListResponse{},
		},
	}
	got := FormatOperationsResponse(resp)
	if !strings.Contains(got, "no operations") {
		t.Errorf("expected empty-list marker, got: %q", got)
	}
}

func TestFormatOperationsResponseFallthrough(t *testing.T) {
	resp := &controlpb.ControlResponse{Success: true, Data: "legacy text"}
	if got := FormatOperationsResponse(resp); got != "" {
		t.Errorf("expected empty string for non-operations response, got %q", got)
	}
}

func TestFormatAgentSSHDResponseIsActive(t *testing.T) {
	resp := &controlpb.ControlResponse{
		Success: true,
		Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
			ExitCode: 0,
			Stdout:   "active\n",
		}},
	}
	got := FormatAgentSSHDResponse(resp)
	for _, want := range []string{"status: active", "exitCode: 0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("FormatAgentSSHDResponse() missing %q:\n%s", want, got)
		}
	}
}

func TestFormatAgentSSHDResponseLegacySystemctlStatus(t *testing.T) {
	resp := &controlpb.ControlResponse{
		Success: true,
		Data:    `{"exitCode":0,"stdout":"● ssh.service - OpenBSD Secure Shell server\n     Active: active (running) since Tue 2026-05-05 01:47:33 UTC\n","stderr":""}`,
	}
	got := FormatAgentSSHDResponse(resp)
	for _, want := range []string{"status: active", "exitCode: 0"} {
		if !strings.Contains(got, want) {
			t.Fatalf("FormatAgentSSHDResponse() missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "OpenBSD Secure Shell") {
		t.Fatalf("FormatAgentSSHDResponse() leaked raw systemd output:\n%s", got)
	}
}
