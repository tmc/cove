package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"connectrpc.com/connect"

	pb "github.com/tmc/cove/proto/agentpb"
)

func (s *agentServer) execStreamPTY(ctx context.Context, r *pb.ExecRequest, cmd *exec.Cmd, stream *connect.ServerStream[pb.ExecOutput]) error {
	code, err := s.runConPTY(ctx, r, cmd, func(data []byte) error {
		return stream.Send(&pb.ExecOutput{Stream: pb.ExecOutput_STDOUT, Data: data})
	}, nil)
	if err != nil {
		return terminalError(err)
	}
	return stream.Send(&pb.ExecOutput{ExitCode: &code})
}

func (s *agentServer) execAttachPTY(ctx context.Context, r *pb.ExecRequest, cmd *exec.Cmd, stream *connect.BidiStream[pb.ExecAttachRequest, pb.ExecAttachOutput]) error {
	code, err := s.runConPTY(ctx, r, cmd, func(data []byte) error {
		return stream.Send(&pb.ExecAttachOutput{Output: &pb.ExecAttachOutput_Stdout{Stdout: &pb.StdoutChunk{Data: data}}})
	}, stream.Receive)
	if err != nil {
		return terminalError(err)
	}
	return stream.Send(&pb.ExecAttachOutput{Output: &pb.ExecAttachOutput_ExitStatus{ExitStatus: &pb.ExitStatus{ExitCode: code}}})
}

func terminalError(err error) error {
	code := connect.CodeInternal
	switch {
	case errors.Is(err, errConPTYUnsupported):
		code = connect.CodeUnimplemented
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = connect.CodeDeadlineExceeded
	}
	var ce *connect.Error
	if errors.As(err, &ce) {
		return err
	}
	return connect.NewError(code, err)
}

func runConPTY(ctx context.Context, r *pb.ExecRequest, cmd *exec.Cmd, send func([]byte) error, receive func() (*pb.ExecAttachRequest, error)) (int32, error) {
	return newAgentServer().runConPTY(ctx, r, cmd, send, receive)
}

func (s *agentServer) runConPTY(ctx context.Context, r *pb.ExecRequest, cmd *exec.Cmd, send func([]byte) error, receive func() (*pb.ExecAttachRequest, error)) (int32, error) {
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	var entry *activeExec
	id := r.GetExecId()
	if id != "" {
		entry = &activeExec{ttyFD: -1}
		s.mu.Lock()
		_, exists := s.execs[id]
		if !exists {
			s.execs[id] = entry
		}
		s.mu.Unlock()
		if exists {
			return -1, connect.NewError(connect.CodeAlreadyExists, errors.New("exec id already active"))
		}
		defer func() {
			s.mu.Lock()
			if s.execs[id] == entry {
				delete(s.execs, id)
			}
			s.mu.Unlock()
		}()
	}
	p, err := startConPTY(cmd, 24, 80)
	if err != nil {
		return -1, fmt.Errorf("start terminal: %w", err)
	}
	defer p.close()
	entry = &activeExec{pid: p.pid, tty: true, ttyFD: -1, resize: func(rows, cols uint32) error {
		if _, err := conPTYSize(rows, cols); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, err)
		}
		return p.resize(rows, cols)
	}, signal: func(sig int32) error {
		if sig != 9 {
			return errors.New("unsupported terminal signal")
		}
		return p.terminate()
	}}
	if id != "" {
		s.mu.Lock()
		s.execs[id] = entry
		s.mu.Unlock()
	}
	stopped, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			p.terminate()
			p.input.Close()
			p.output.Close()
		case <-stopped:
		}
	}()
	defer func() { close(stopped); <-watcherDone }()

	outputDone := make(chan error, 1)
	go func() {
		err := sendTerminalOutput(p.output, send)
		p.output.Close()
		p.terminate()
		outputDone <- err
	}()
	controlDone := make(chan error, 1)
	go func() {
		if len(r.Stdin) != 0 {
			if _, err := p.input.Write(r.Stdin); err != nil {
				controlDone <- err
				p.terminate()
				return
			}
		}
		if receive == nil {
			return
		}
		err := receiveTerminalInput(p, receive, stopped)
		controlDone <- err
		p.terminate()
		p.input.Close()
	}()

	code, waitErr := p.wait()
	// Descendants must not keep the console alive after the foreground command exits.
	p.terminate()
	p.input.Close()
	p.closeConsole()
	outputErr := <-outputDone
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	if waitErr != nil {
		return -1, waitErr
	}
	if outputErr != nil {
		return -1, outputErr
	}
	select {
	case err := <-controlDone:
		if err != nil && !errors.Is(err, os.ErrClosed) {
			return -1, err
		}
	default:
	}
	return code, nil
}

func sendTerminalOutput(src io.Reader, send func([]byte) error) error {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if err := send(append([]byte(nil), buf[:n]...)); err != nil {
				return err
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
				return nil
			}
			return err
		}
	}
}

func receiveTerminalInput(p *conPTY, receive func() (*pb.ExecAttachRequest, error), stopped <-chan struct{}) error {
	for {
		frame, err := receive()
		select {
		case <-stopped:
			return nil
		default:
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch req := frame.GetRequest().(type) {
		case *pb.ExecAttachRequest_Stdin:
			if _, err := p.input.Write(req.Stdin.GetData()); err != nil {
				return err
			}
		case *pb.ExecAttachRequest_Resize:
			if _, err := conPTYSize(req.Resize.GetRows(), req.Resize.GetCols()); err != nil {
				return connect.NewError(connect.CodeInvalidArgument, err)
			}
			if err := p.resize(req.Resize.GetRows(), req.Resize.GetCols()); err != nil {
				return err
			}
		case *pb.ExecAttachRequest_Signal:
			if req.Signal.GetSignal() != 9 {
				return connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported terminal signal; send ctrl+c as input"))
			}
			return nil
		case *pb.ExecAttachRequest_CloseStdin:
			return nil
		default:
			return connect.NewError(connect.CodeInvalidArgument, errors.New("unexpected terminal control frame"))
		}
	}
}
