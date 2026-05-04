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
	"runtime"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/tmc/vz-macos/proto/agentpb"
	"github.com/tmc/vz-macos/proto/agentpbconnect"
)

// systemInfo holds platform-agnostic system information.
// Populated by populateSystemInfo in info_darwin.go / info_linux.go.
type systemInfo struct {
	OSVersion       string
	KernelVersion   string
	MemoryTotal     uint64
	MemoryAvailable uint64
	LoadAvg1        float64
	LoadAvg5        float64
	LoadAvg15       float64
	UptimeSeconds   uint64
}

type agentServer struct {
	agentpbconnect.UnimplementedAgentHandler
	mu    sync.Mutex
	execs map[string]*activeExec
}

type activeExec struct {
	pid   int
	tty   bool
	ttyFD int
	// ptmx holds the PTY master file when the exec was launched with tty=true
	// via pty.Start. The agent retains it so reads can be drained into the
	// output stream and so the master fd survives until untrackExec closes it.
	ptmx *os.File
}

func newAgentServer() *agentServer {
	return &agentServer{execs: make(map[string]*activeExec)}
}

func (s *agentServer) Ping(_ context.Context, _ *connect.Request[pb.PingRequest]) (*connect.Response[pb.PingResponse], error) {
	return connect.NewResponse(&pb.PingResponse{
		Timestamp: timestamppb.Now(),
		Version:   agentVersion(),
	}), nil
}

func (s *agentServer) Info(ctx context.Context, _ *connect.Request[pb.InfoRequest]) (*connect.Response[pb.InfoResponse], error) {
	hostname, _ := os.Hostname()

	resp := &pb.InfoResponse{
		Hostname: hostname,
		Arch:     runtime.GOARCH,
	}

	var si systemInfo
	populateSystemInfo(ctx, &si)
	resp.OsVersion = si.OSVersion
	resp.KernelVersion = si.KernelVersion
	resp.MemoryTotal = si.MemoryTotal
	resp.MemoryAvailable = si.MemoryAvailable
	resp.LoadAvg_1 = si.LoadAvg1
	resp.LoadAvg_5 = si.LoadAvg5
	resp.LoadAvg_15 = si.LoadAvg15
	resp.UptimeSeconds = si.UptimeSeconds

	if total, available, err := statFilesystem("/"); err == nil {
		resp.DiskTotal = total
		resp.DiskAvailable = available
	}

	if users, err := listLocalUsers(ctx); err == nil {
		resp.Users = users
	}

	v, c, d := resolvedVersionInfo()
	resp.AgentVersion = v
	resp.AgentCommit = c
	resp.AgentBuildDate = d
	resp.AgentSha256 = selfSHA256()

	return connect.NewResponse(resp), nil
}

func (s *agentServer) Exec(ctx context.Context, req *connect.Request[pb.ExecRequest]) (*connect.Response[pb.ExecResponse], error) {
	r := req.Msg
	cmd, err := newExecCommand(ctx, r)
	if err != nil {
		return nil, err
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("start: %v", err))
	}
	s.trackExec(r, cmd)
	err = cmd.Wait()
	s.untrackExec(r.GetExecId())
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

func (s *agentServer) ExecStream(ctx context.Context, req *connect.Request[pb.ExecRequest], stream *connect.ServerStream[pb.ExecOutput]) error {
	r := req.Msg
	cmd, err := newExecCommand(ctx, r)
	if err != nil {
		return err
	}

	if r.GetTty() {
		return s.execStreamPTY(r, cmd, stream)
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
	s.trackExec(r, cmd)
	defer s.untrackExec(r.GetExecId())

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

func (s *agentServer) ResizeExecTTY(_ context.Context, req *connect.Request[pb.ResizeExecTTYRequest]) (*connect.Response[pb.ResizeExecTTYResponse], error) {
	r := req.Msg
	if r.GetExecId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("exec_id required"))
	}
	if r.GetRows() == 0 || r.GetCols() == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("rows and cols required"))
	}
	exec, ok := s.lookupExec(r.GetExecId())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("exec not found"))
	}
	if !exec.tty || exec.ttyFD < 0 {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("exec has no tty"))
	}
	if err := resizeTTY(exec.ttyFD, r.GetRows(), r.GetCols()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("resize tty: %v", err))
	}
	return connect.NewResponse(&pb.ResizeExecTTYResponse{}), nil
}

func (s *agentServer) SignalExec(_ context.Context, req *connect.Request[pb.SignalExecRequest]) (*connect.Response[pb.SignalExecResponse], error) {
	r := req.Msg
	if r.GetExecId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("exec_id required"))
	}
	if !allowedExecSignal(r.GetSignal()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported signal"))
	}
	exec, ok := s.lookupExec(r.GetExecId())
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("exec not found"))
	}
	if err := signalExec(exec.pid, r.GetSignal()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("signal exec: %v", err))
	}
	return connect.NewResponse(&pb.SignalExecResponse{}), nil
}

func (s *agentServer) SetTime(_ context.Context, req *connect.Request[pb.SetTimeRequest]) (*connect.Response[pb.SetTimeResponse], error) {
	ts := req.Msg.GetTime()
	if ts == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("time required"))
	}
	if err := ts.CheckValid(); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid time: %v", err))
	}
	if err := setSystemTime(ts.AsTime()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("set time: %v", err))
	}
	return connect.NewResponse(&pb.SetTimeResponse{}), nil
}

func (s *agentServer) trackExec(r *pb.ExecRequest, cmd *exec.Cmd) {
	if r.GetExecId() == "" || cmd.Process == nil {
		return
	}
	s.mu.Lock()
	s.execs[r.GetExecId()] = &activeExec{pid: cmd.Process.Pid, tty: r.GetTty(), ttyFD: -1}
	s.mu.Unlock()
}

// trackExecWithPty records an exec that owns a PTY master file. The fd is
// stored so ResizeExecTTY can issue TIOCSWINSZ; the *os.File is held so
// untrackExec can close it after the process exits.
func (s *agentServer) trackExecWithPty(r *pb.ExecRequest, cmd *exec.Cmd, ptmx *os.File) {
	if r.GetExecId() == "" || cmd.Process == nil {
		return
	}
	s.mu.Lock()
	s.execs[r.GetExecId()] = &activeExec{
		pid:   cmd.Process.Pid,
		tty:   true,
		ttyFD: int(ptmx.Fd()),
		ptmx:  ptmx,
	}
	s.mu.Unlock()
}

func (s *agentServer) untrackExec(execID string) {
	if execID == "" {
		return
	}
	s.mu.Lock()
	entry := s.execs[execID]
	delete(s.execs, execID)
	s.mu.Unlock()
	if entry != nil && entry.ptmx != nil {
		entry.ptmx.Close()
	}
}

func (s *agentServer) lookupExec(execID string) (*activeExec, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exec, ok := s.execs[execID]
	return exec, ok
}

func newExecCommand(ctx context.Context, r *pb.ExecRequest) (*exec.Cmd, error) {
	if len(r.Args) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("args required"))
	}

	cmd := exec.CommandContext(ctx, r.Args[0], r.Args[1:]...)
	if r.WorkingDir != "" {
		cmd.Dir = r.WorkingDir
	}
	if len(r.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range r.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	if r.User != nil {
		if err := setUser(cmd, *r.User); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("set user: %v", err))
		}
	}
	if r.GetExecId() != "" {
		configureProcessGroup(cmd)
	}
	if r.Stdin != nil {
		cmd.Stdin = bytes.NewReader(r.Stdin)
	}
	return cmd, nil
}

func streamPipe(stream *connect.ServerStream[pb.ExecOutput], pipe io.ReadCloser, which pb.ExecOutput_Stream, done chan<- error) {
	buf := make([]byte, 32*1024)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&pb.ExecOutput{
				Stream: which,
				Data:   append([]byte(nil), buf[:n]...),
			}); sendErr != nil {
				done <- sendErr
				return
			}
		}
		if err != nil {
			done <- err
			return
		}
	}
}

func (s *agentServer) WriteFile(_ context.Context, req *connect.Request[pb.WriteFileRequest]) (*connect.Response[pb.WriteFileResponse], error) {
	r := req.Msg
	if r.Path == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("path required"))
	}
	if r.CreateParents {
		if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("mkdir: %v", err))
		}
	}
	flag := os.O_WRONLY | os.O_CREATE
	if r.Append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	mode := os.FileMode(0o644)
	if r.Mode != 0 {
		mode = os.FileMode(r.Mode)
	}
	f, err := os.OpenFile(r.Path, flag, mode)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("open: %v", err))
	}
	defer f.Close()
	if _, err := f.Write(r.Data); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("write: %v", err))
	}
	return connect.NewResponse(&pb.WriteFileResponse{}), nil
}

func (s *agentServer) ReadFile(_ context.Context, req *connect.Request[pb.ReadFileRequest]) (*connect.Response[pb.ReadFileResponse], error) {
	r := req.Msg
	if r.Path == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("path required"))
	}
	info, err := os.Stat(r.Path)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("stat: %v", err))
	}
	f, err := os.Open(r.Path)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("open: %v", err))
	}
	defer f.Close()

	if r.Offset != nil {
		f.Seek(int64(*r.Offset), io.SeekStart)
	}
	var data []byte
	if r.Limit != nil {
		data = make([]byte, *r.Limit)
		n, err := io.ReadFull(f, data)
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read: %v", err))
		}
		data = data[:n]
	} else {
		data, err = io.ReadAll(f)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("read: %v", err))
		}
	}

	return connect.NewResponse(&pb.ReadFileResponse{
		Data: data,
		Size: uint64(info.Size()),
		Mode: uint32(info.Mode()),
	}), nil
}

func (s *agentServer) Mkdir(_ context.Context, req *connect.Request[pb.MkdirRequest]) (*connect.Response[pb.MkdirResponse], error) {
	r := req.Msg
	if r.Path == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("path required"))
	}
	mode := os.FileMode(0o755)
	if r.Mode != 0 {
		mode = os.FileMode(r.Mode)
	}
	var err error
	if r.All {
		err = os.MkdirAll(r.Path, mode)
	} else {
		err = os.Mkdir(r.Path, mode)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("mkdir: %v", err))
	}
	return connect.NewResponse(&pb.MkdirResponse{}), nil
}

func (s *agentServer) Mount(_ context.Context, req *connect.Request[pb.MountRequest]) (*connect.Response[pb.MountResponse], error) {
	r := req.Msg
	args := []string{"-t", r.Type}
	if len(r.Options) > 0 {
		args = append(args, "-o", strings.Join(r.Options, ","))
	}
	args = append(args, r.Source, r.Destination)
	if err := os.MkdirAll(r.Destination, 0o755); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("mkdir mount point: %v", err))
	}
	out, err := exec.Command("mount", args...).CombinedOutput()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("mount: %v: %s", err, out))
	}
	return connect.NewResponse(&pb.MountResponse{}), nil
}

func (s *agentServer) Unmount(_ context.Context, req *connect.Request[pb.UnmountRequest]) (*connect.Response[pb.UnmountResponse], error) {
	r := req.Msg
	args := []string{}
	if r.Force {
		args = append(args, "-f")
	}
	args = append(args, r.Path)
	out, err := exec.Command("umount", args...).CombinedOutput()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("umount: %v: %s", err, out))
	}
	return connect.NewResponse(&pb.UnmountResponse{}), nil
}

func (s *agentServer) CopyIn(_ context.Context, stream *connect.ClientStream[pb.CopyInChunk]) (*connect.Response[pb.CopyInResponse], error) {
	if !stream.Receive() {
		if err := stream.Err(); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("recv init: %v", err))
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("first message must be init"))
	}
	first := stream.Msg()
	init := first.GetInit()
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
		chunk := stream.Msg()
		data := chunk.GetData()
		if len(data) > 0 {
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

func (s *agentServer) CopyOut(_ context.Context, req *connect.Request[pb.CopyOutRequest], stream *connect.ServerStream[pb.CopyOutChunk]) error {
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
				Content: &pb.CopyOutChunk_Data{
					Data: append([]byte(nil), buf[:n]...),
				},
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

func (s *agentServer) Shutdown(_ context.Context, req *connect.Request[pb.ShutdownRequest]) (*connect.Response[pb.ShutdownResponse], error) {
	slog.Info("shutdown requested")
	go func() {
		time.Sleep(500 * time.Millisecond)
		args := []string{"now"}
		if req.Msg.Force {
			args = []string{"-h", "now"}
		}
		exec.Command("shutdown", args...).Run()
	}()
	return connect.NewResponse(&pb.ShutdownResponse{}), nil
}

func (s *agentServer) Reboot(_ context.Context, _ *connect.Request[pb.RebootRequest]) (*connect.Response[pb.RebootResponse], error) {
	slog.Info("reboot requested")
	go func() {
		time.Sleep(500 * time.Millisecond)
		exec.Command("shutdown", "-r", "now").Run()
	}()
	return connect.NewResponse(&pb.RebootResponse{}), nil
}
