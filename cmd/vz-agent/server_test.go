//go:build darwin || linux

package main

import (
	"context"
	"io"
	"strings"
	"syscall"
	"testing"

	"connectrpc.com/connect"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"

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

func TestNewExecCommandTTYWithExecIDLeavesPTYSessionAttrs(t *testing.T) {
	cmd, err := newExecCommand(context.Background(), &pb.ExecRequest{
		Args:   []string{"/usr/bin/true"},
		ExecId: "exec-1",
		Tty:    true,
	})
	if err != nil {
		t.Fatalf("newExecCommand: %v", err)
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		t.Fatalf("SysProcAttr.Setpgid = true for tty exec; pty.Start owns session setup")
	}
}

func TestInfoAdvertisesExecAttach(t *testing.T) {
	s := newAgentServer()
	resp, err := s.Info(context.Background(), connect.NewRequest(&pb.InfoRequest{}))
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if !containsString(resp.Msg.GetFeatures(), "exec_attach") {
		t.Fatalf("features = %v, want exec_attach", resp.Msg.GetFeatures())
	}
}

func TestReceiveExecAttachStdinWritesFrames(t *testing.T) {
	stream := &fakeExecAttachReceiver{reqs: []*pb.ExecAttachRequest{
		{Request: &pb.ExecAttachRequest_Stdin{Stdin: []byte("hello")}},
		{Request: &pb.ExecAttachRequest_Stdin{Stdin: []byte(" world")}},
	}}
	dst := &recordWriteCloser{}
	receiveExecAttachStdin(stream, dst)
	if got, want := dst.String(), "hello world"; got != want {
		t.Fatalf("stdin writes = %q, want %q", got, want)
	}
	if !dst.closed {
		t.Fatal("stdin destination was not closed")
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

// TestResizeExecTTYAppliesWinsize allocates a real PTY pair, registers it as
// an active exec, and verifies that ResizeExecTTY issues TIOCSWINSZ visibly
// on the slave side. This is the smoke test that the hardcoded ttyFD: -1
// stub has been replaced with real PTY allocation.
func TestResizeExecTTYAppliesWinsize(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	t.Cleanup(func() {
		ptmx.Close()
		tty.Close()
	})

	s := newAgentServer()
	s.execs["exec-1"] = &activeExec{
		pid:   syscall.Getpid(),
		tty:   true,
		ttyFD: int(ptmx.Fd()),
		ptmx:  ptmx,
	}

	const wantRows, wantCols = 42, 137
	if _, err := s.ResizeExecTTY(context.Background(), connect.NewRequest(&pb.ResizeExecTTYRequest{
		ExecId: "exec-1",
		Rows:   wantRows,
		Cols:   wantCols,
	})); err != nil {
		t.Fatalf("ResizeExecTTY: %v", err)
	}

	got, err := unix.IoctlGetWinsize(int(tty.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		t.Fatalf("IoctlGetWinsize: %v", err)
	}
	if got.Row != wantRows || got.Col != wantCols {
		t.Fatalf("winsize after resize = %dx%d, want %dx%d", got.Row, got.Col, wantRows, wantCols)
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

type fakeExecAttachReceiver struct {
	reqs []*pb.ExecAttachRequest
}

func (f *fakeExecAttachReceiver) Receive() (*pb.ExecAttachRequest, error) {
	if len(f.reqs) == 0 {
		return nil, io.EOF
	}
	req := f.reqs[0]
	f.reqs = f.reqs[1:]
	return req, nil
}

type recordWriteCloser struct {
	strings.Builder
	closed bool
}

func (r *recordWriteCloser) Close() error {
	r.closed = true
	return nil
}
