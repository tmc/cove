package main

import (
	"errors"
	"os/exec"

	"connectrpc.com/connect"

	pb "github.com/tmc/cove/proto/agentpb"
)

func (s *agentServer) execStreamPTY(*pb.ExecRequest, *exec.Cmd, *connect.ServerStream[pb.ExecOutput]) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("tty exec unsupported on windows"))
}

func (s *agentServer) execAttachPTY(*pb.ExecRequest, *exec.Cmd, *connect.BidiStream[pb.ExecAttachRequest, pb.ExecAttachOutput]) error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("tty exec unsupported on windows"))
}
