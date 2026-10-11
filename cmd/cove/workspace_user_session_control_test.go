package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestWorkspaceUserObservationFailedDialDoesNotRepair(t *testing.T) {
	s := &ControlServer{}
	calls := 0
	req := &controlpb.ControlRequest{Command: &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{Args: []string{"/bin/pwd", "-P"}, DeadlineUnixNano: time.Now().Add(time.Second).UnixNano()}}}
	resp := s.handleWorkspaceUserObserveWithDial(req, func(context.Context, uint32) (net.Conn, error) {
		calls++
		return nil, errors.New("user port unavailable")
	})
	if resp.Success || calls != 1 {
		t.Fatalf("response%v dial%d", resp, calls)
	}
}

func TestWorkspaceUserObservationExpiredBudgetDoesNotDial(t *testing.T) {
	s := &ControlServer{}
	req := &controlpb.ControlRequest{Command: &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{Args: []string{"/bin/pwd", "-P"}, DeadlineUnixNano: time.Now().Add(-time.Second).UnixNano()}}}
	resp := s.handleWorkspaceUserObserveWithDial(req, func(context.Context, uint32) (net.Conn, error) {
		t.Fatal("expired observation dialed")
		return nil, nil
	})
	if resp.Success {
		t.Fatal("expired observation succeeded")
	}
}

func TestWorkspaceUserObservationAcquisitionBudget(t *testing.T) {
	s := &ControlServer{}
	req := &controlpb.ControlRequest{Command: &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{Args: []string{"/bin/pwd", "-P"}, DeadlineUnixNano: time.Now().Add(100 * time.Millisecond).UnixNano()}}}
	calls := 0
	resp := s.handleWorkspaceUserObserveWithDial(req, func(ctx context.Context, _ uint32) (net.Conn, error) { calls++; <-ctx.Done(); return nil, ctx.Err() })
	if resp.Success || calls != 1 {
		t.Fatalf("acquisition %v calls%d", resp, calls)
	}
}
