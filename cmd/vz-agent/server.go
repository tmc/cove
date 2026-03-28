package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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
}

func newAgentServer() *agentServer {
	return &agentServer{}
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

	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err == nil {
		resp.DiskTotal = stat.Blocks * uint64(stat.Bsize)
		resp.DiskAvailable = stat.Bavail * uint64(stat.Bsize)
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
	err = cmd.Run()
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
	log.Println("shutdown requested")
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
	log.Println("reboot requested")
	go func() {
		time.Sleep(500 * time.Millisecond)
		exec.Command("shutdown", "-r", "now").Run()
	}()
	return connect.NewResponse(&pb.RebootResponse{}), nil
}

func setUser(cmd *exec.Cmd, username string) error {
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("lookup user %q: %w", username, err)
	}
	var uid, gid uint32
	fmt.Sscanf(u.Uid, "%d", &uid)
	fmt.Sscanf(u.Gid, "%d", &gid)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid: uid,
			Gid: gid,
		},
	}
	return nil
}
