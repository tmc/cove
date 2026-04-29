package main

import (
	"context"
	"strings"
	"syscall"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/tmc/vz-macos/proto/agentpb"
)

func TestNewExecCommandRejectsUnknownUser(t *testing.T) {
	user := "vz-agent-missing-user"
	_, err := newExecCommand(context.Background(), &pb.ExecRequest{
		Args: []string{"/usr/bin/true"},
		User: &user,
	})
	if err == nil {
		t.Fatalf("newExecCommand() error = nil, want invalid user error")
	}
}

func TestNewExecCommandWithExecIDCreatesProcessGroup(t *testing.T) {
	cmd, err := newExecCommand(context.Background(), &pb.ExecRequest{
		Args:   []string{"/usr/bin/true"},
		ExecId: "exec-1",
	})
	if err != nil {
		t.Fatalf("newExecCommand: %v", err)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("SysProcAttr.Setpgid = false, want true")
	}
}

func TestSignalExecValidatesSignal(t *testing.T) {
	s := newAgentServer()
	_, err := s.SignalExec(context.Background(), connect.NewRequest(&pb.SignalExecRequest{
		ExecId: "exec-1",
		Signal: int32(syscall.SIGHUP),
	}))
	if err == nil || !strings.Contains(err.Error(), "unsupported signal") {
		t.Fatalf("SignalExec unsupported signal error = %v", err)
	}
}

func TestResizeExecTTYRequiresTTY(t *testing.T) {
	s := newAgentServer()
	s.execs["exec-1"] = &activeExec{pid: 123, tty: false, ttyFD: -1}
	_, err := s.ResizeExecTTY(context.Background(), connect.NewRequest(&pb.ResizeExecTTYRequest{
		ExecId: "exec-1",
		Rows:   24,
		Cols:   80,
	}))
	if err == nil || !strings.Contains(err.Error(), "exec has no tty") {
		t.Fatalf("ResizeExecTTY error = %v", err)
	}
}
