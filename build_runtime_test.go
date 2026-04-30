package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestWaitBuildAgentRetriesUntilSuccess(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		if sock != "sock" || req.Type != "agent-ping" || cmdType != "agent-ping" {
			t.Fatalf("request = sock %q type %q cmd %q", sock, req.Type, cmdType)
		}
		if *call == 1 {
			return &controlpb.ControlResponse{Success: false, Error: "booting"}, nil
		}
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restore()
	if err := waitBuildAgent(context.Background(), "sock", time.Second); err != nil {
		t.Fatalf("waitBuildAgent(): %v", err)
	}
}

func TestWaitBuildAgentHonorsContext(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		return nil, errors.New("unreachable")
	})
	defer restore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitBuildAgent(ctx, "sock", time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitBuildAgent() = %v, want context.Canceled", err)
	}
}

func TestShutdownBuildGuest(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		if sock != "sock" || req.Type != "agent-shutdown" || req.GetAgentShutdown() == nil || cmdType != "agent-shutdown" {
			t.Fatalf("request = sock %q type %q cmd %q", sock, req.Type, cmdType)
		}
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restore()
	if err := shutdownBuildGuest(context.Background(), "sock"); err != nil {
		t.Fatalf("shutdownBuildGuest(): %v", err)
	}
}

func TestShutdownBuildGuestReportsFailure(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		return &controlpb.ControlResponse{Success: false, Error: "denied"}, nil
	})
	defer restore()
	err := shutdownBuildGuest(context.Background(), "sock")
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("shutdownBuildGuest() = %v, want denial", err)
	}
}

func stubBuildControlSender(t *testing.T, fn func(*int, string, *controlpb.ControlRequest, time.Duration, string) (*controlpb.ControlResponse, error)) func() {
	t.Helper()
	old := sendBuildControlRequest
	calls := 0
	sendBuildControlRequest = func(sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return fn(&calls, sock, req, timeout, cmdType)
	}
	return func() {
		sendBuildControlRequest = old
	}
}
