package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"connectrpc.com/connect"

	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
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

// UserCopyIn streams a file from the host into the guest, writing it as the
// current (logged-in) user so TCC-protected destinations succeed. The file I/O
// mirrors the daemon's CopyIn; only the process identity differs.
func (s *userAgentServer) UserCopyIn(_ context.Context, stream *connect.ClientStream[pb.CopyInChunk]) (*connect.Response[pb.CopyInResponse], error) {
	if !stream.Receive() {
		if err := stream.Err(); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("recv init: %v", err))
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("first message must be init"))
	}
	init := stream.Msg().GetInit()
	if init == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("first message must be init"))
	}

	if init.CreateParents {
		if err := os.MkdirAll(filepath.Dir(init.Path), 0o755); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("mkdir: %v", err))
		}
	}

	mode := os.FileMode(0o644)
	if init.Mode != 0 {
		mode = os.FileMode(init.Mode)
	}
	f, err := os.OpenFile(init.Path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create: %v", err))
	}
	defer f.Close()

	for stream.Receive() {
		if data := stream.Msg().GetData(); len(data) > 0 {
			if _, err := f.Write(data); err != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("write: %v", err))
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("recv: %v", err))
	}
	return connect.NewResponse(&pb.CopyInResponse{}), nil
}

// UserCopyOut streams a file from the guest to the host, reading it as the
// current (logged-in) user. Mirrors the daemon's CopyOut.
func (s *userAgentServer) UserCopyOut(_ context.Context, req *connect.Request[pb.CopyOutRequest], stream *connect.ServerStream[pb.CopyOutChunk]) error {
	r := req.Msg
	info, err := os.Stat(r.Path)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("stat: %v", err))
	}

	f, err := os.Open(r.Path)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("open: %v", err))
	}
	defer f.Close()

	if err := stream.Send(&pb.CopyOutChunk{
		Content: &pb.CopyOutChunk_Init{
			Init: &pb.CopyOutInit{
				TotalSize: uint64(info.Size()),
				Mode:      uint32(info.Mode()),
			},
		},
	}); err != nil {
		return err
	}

	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&pb.CopyOutChunk{
				Content: &pb.CopyOutChunk_Data{Data: append([]byte(nil), buf[:n]...)},
			}); sendErr != nil {
				return sendErr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("read: %v", err))
		}
	}
	return nil
}
