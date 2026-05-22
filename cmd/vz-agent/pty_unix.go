//go:build darwin || linux

package main

import (
	"fmt"
	"io"
	"os/exec"

	"connectrpc.com/connect"
	"github.com/creack/pty"

	pb "github.com/tmc/cove/proto/agentpb"
)

func (s *agentServer) execStreamPTY(r *pb.ExecRequest, cmd *exec.Cmd, stream *connect.ServerStream[pb.ExecOutput]) error {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("pty start: %v", err))
	}
	s.trackExecWithPty(r, cmd, ptmx)
	defer s.untrackExec(r.GetExecId())

	done := make(chan error, 1)
	go streamPipe(stream, ptmx, pb.ExecOutput_STDOUT, done)

	exitCode := int32(0)
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = int32(exitErr.ExitCode())
		}
	}
	<-done
	return stream.Send(&pb.ExecOutput{ExitCode: &exitCode})
}

func (s *agentServer) execAttachPTY(r *pb.ExecRequest, cmd *exec.Cmd, stream *connect.BidiStream[pb.ExecAttachRequest, pb.ExecAttachOutput]) error {
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("pty start: %v", err))
	}
	s.trackExecWithPty(r, cmd, ptmx)
	defer s.untrackExec(r.GetExecId())

	go s.receiveExecAttachControl(stream, nopWriteCloser{Writer: ptmx}, r.GetExecId(), true)

	done := make(chan error, 1)
	go streamAttachPipe(stream, ptmx, pb.ExecOutput_STDOUT, done)

	exitCode := int32(0)
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = int32(exitErr.ExitCode())
		}
	}
	<-done
	return stream.Send(&pb.ExecAttachOutput{
		Output: &pb.ExecAttachOutput_ExitStatus{ExitStatus: &pb.ExitStatus{ExitCode: exitCode}},
	})
}

type nopWriteCloser struct {
	io.Writer
}

func (w nopWriteCloser) Close() error {
	return nil
}
