package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"time"

	"connectrpc.com/connect"

	pb "github.com/tmc/vz-macos/proto/agentpb"
	"github.com/tmc/vz-macos/proto/agentpbconnect"
)

// userAgentServer implements the UserAgent service.
// It runs commands as the current user, inheriting the session's TCC/FDA grants.
type userAgentServer struct {
	agentpbconnect.UnimplementedUserAgentHandler
}

func newUserAgentServer() *userAgentServer {
	return &userAgentServer{}
}

func (s *userAgentServer) UserExec(ctx context.Context, req *connect.Request[pb.ExecRequest]) (*connect.Response[pb.ExecResponse], error) {
	r := req.Msg
	if len(r.Args) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("args required"))
	}

	cmd := exec.CommandContext(ctx, r.Args[0], r.Args[1:]...)
	if r.WorkingDir != "" {
		cmd.Dir = r.WorkingDir
	}
	if r.Stdin != nil {
		cmd.Stdin = bytes.NewReader(r.Stdin)
	}
	// Do NOT call setUser — run as the current user to inherit TCC/FDA.
	if r.User != nil {
		slog.Info("user-exec: ignoring user field (runs as current user)",
			slog.String("requested_user", *r.User))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start).Seconds()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("exec: %v", err))
		}
	}

	return connect.NewResponse(&pb.ExecResponse{
		ExitCode:        int32(exitCode),
		Stdout:          stdout.Bytes(),
		Stderr:          stderr.Bytes(),
		DurationSeconds: duration,
	}), nil
}

func (s *userAgentServer) UserExecStream(ctx context.Context, req *connect.Request[pb.ExecRequest], stream *connect.ServerStream[pb.ExecOutput]) error {
	r := req.Msg
	if len(r.Args) == 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("args required"))
	}

	cmd := exec.CommandContext(ctx, r.Args[0], r.Args[1:]...)
	if r.WorkingDir != "" {
		cmd.Dir = r.WorkingDir
	}
	if r.Stdin != nil {
		cmd.Stdin = bytes.NewReader(r.Stdin)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("stdout pipe: %v", err))
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("stderr pipe: %v", err))
	}

	if err := cmd.Start(); err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("start: %v", err))
	}

	done := make(chan error, 2)
	go streamPipe(stream, stdoutPipe, pb.ExecOutput_STDOUT, done)
	go streamPipe(stream, stderrPipe, pb.ExecOutput_STDERR, done)
	<-done
	<-done

	exitCode := int32(0)
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = int32(exitErr.ExitCode())
		}
	}
	return stream.Send(&pb.ExecOutput{ExitCode: &exitCode})
}

// userStreamPipe reads from a pipe and sends chunks to the stream.
// Reuses the same streamPipe from server.go.
func userStreamPipe(stream *connect.ServerStream[pb.ExecOutput], pipe io.ReadCloser, which pb.ExecOutput_Stream, done chan<- error) {
	streamPipe(stream, pipe, which, done)
}
