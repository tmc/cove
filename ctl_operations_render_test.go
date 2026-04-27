package main

import (
	"strings"
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// TestFormatOperationsResponseSingleOp verifies that ctl operations get/wait
// renders the proto OperationInfo fields rather than falling through to the
// legacy "OK" path. Regression for v0.1.0 where operations get/wait/list
// printed only "OK" because ctlPrintResponse ignored Result.Operation.
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

	got := formatOperationsResponse(resp)
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

	got := formatOperationsResponse(resp)
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

	got := formatOperationsResponse(resp)
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
	got := formatOperationsResponse(resp)
	if !strings.Contains(got, "no operations") {
		t.Errorf("expected empty-list marker, got: %q", got)
	}
}

// TestFormatOperationsResponseFallthrough verifies the helper returns "" when
// neither Operation nor OperationsList is set, so ctlPrintResponse can fall
// through to legacy resp.Data rendering.
func TestFormatOperationsResponseFallthrough(t *testing.T) {
	resp := &controlpb.ControlResponse{Success: true, Data: "legacy text"}
	if got := formatOperationsResponse(resp); got != "" {
		t.Errorf("expected empty string for non-operations response, got %q", got)
	}
}
