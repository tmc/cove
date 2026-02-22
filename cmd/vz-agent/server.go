package main

import (
	"bytes"
	"context"
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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/tmc/vz-macos/proto/agentpb"
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
	pb.UnimplementedAgentServer
}

func newAgentServer() *agentServer {
	return &agentServer{}
}

func (s *agentServer) Ping(_ context.Context, _ *pb.PingRequest) (*pb.PingResponse, error) {
	return &pb.PingResponse{
		Timestamp: timestamppb.Now(),
		Version:   agentVersion(),
	}, nil
}

func (s *agentServer) Info(_ context.Context, _ *pb.InfoRequest) (*pb.InfoResponse, error) {
	hostname, _ := os.Hostname()

	resp := &pb.InfoResponse{
		Hostname: hostname,
		Arch:     runtime.GOARCH,
	}

	// Platform-specific OS/kernel/memory/load/uptime info.
	var si systemInfo
	populateSystemInfo(&si)
	resp.OsVersion = si.OSVersion
	resp.KernelVersion = si.KernelVersion
	resp.MemoryTotal = si.MemoryTotal
	resp.MemoryAvailable = si.MemoryAvailable
	resp.LoadAvg_1 = si.LoadAvg1
	resp.LoadAvg_5 = si.LoadAvg5
	resp.LoadAvg_15 = si.LoadAvg15
	resp.UptimeSeconds = si.UptimeSeconds

	// Disk usage (syscall.Statfs_t is portable across macOS and Linux).
	var stat syscall.Statfs_t
	if err := syscall.Statfs("/", &stat); err == nil {
		resp.DiskTotal = stat.Blocks * uint64(stat.Bsize)
		resp.DiskAvailable = stat.Bavail * uint64(stat.Bsize)
	}

	// Users (platform-specific: dscl on macOS, /etc/passwd on Linux).
	if users, err := listLocalUsers(); err == nil {
		resp.Users = users
	}

	return resp, nil
}

func (s *agentServer) Exec(ctx context.Context, req *pb.ExecRequest) (*pb.ExecResponse, error) {
	if len(req.Args) == 0 {
		return nil, status.Error(codes.InvalidArgument, "args required")
	}

	cmd := exec.CommandContext(ctx, req.Args[0], req.Args[1:]...)
	if req.WorkingDir != "" {
		cmd.Dir = req.WorkingDir
	}
	if len(req.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	if req.User != nil {
		if err := setUser(cmd, *req.User); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "set user: %v", err)
		}
	}
	if req.Stdin != nil {
		cmd.Stdin = bytes.NewReader(req.Stdin)
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
			return nil, status.Errorf(codes.Internal, "exec: %v", err)
		}
	}

	return &pb.ExecResponse{
		ExitCode:        int32(exitCode),
		Stdout:          stdout.Bytes(),
		Stderr:          stderr.Bytes(),
		DurationSeconds: duration,
	}, nil
}

func (s *agentServer) ExecStream(req *pb.ExecRequest, stream pb.Agent_ExecStreamServer) error {
	if len(req.Args) == 0 {
		return status.Error(codes.InvalidArgument, "args required")
	}

	cmd := exec.CommandContext(stream.Context(), req.Args[0], req.Args[1:]...)
	if req.WorkingDir != "" {
		cmd.Dir = req.WorkingDir
	}
	if len(req.Env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range req.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	if req.Stdin != nil {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return status.Errorf(codes.Internal, "stdout pipe: %v", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return status.Errorf(codes.Internal, "stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return status.Errorf(codes.Internal, "start: %v", err)
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

func streamPipe(stream pb.Agent_ExecStreamServer, pipe io.ReadCloser, which pb.ExecOutput_Stream, done chan<- error) {
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

func (s *agentServer) WriteFile(_ context.Context, req *pb.WriteFileRequest) (*pb.WriteFileResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path required")
	}
	if req.CreateParents {
		if err := os.MkdirAll(filepath.Dir(req.Path), 0o755); err != nil {
			return nil, status.Errorf(codes.Internal, "mkdir: %v", err)
		}
	}
	flag := os.O_WRONLY | os.O_CREATE
	if req.Append {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
	}
	mode := os.FileMode(0o644)
	if req.Mode != 0 {
		mode = os.FileMode(req.Mode)
	}
	f, err := os.OpenFile(req.Path, flag, mode)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "open: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(req.Data); err != nil {
		return nil, status.Errorf(codes.Internal, "write: %v", err)
	}
	return &pb.WriteFileResponse{}, nil
}

func (s *agentServer) ReadFile(_ context.Context, req *pb.ReadFileRequest) (*pb.ReadFileResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path required")
	}
	info, err := os.Stat(req.Path)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "stat: %v", err)
	}
	f, err := os.Open(req.Path)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "open: %v", err)
	}
	defer f.Close()

	if req.Offset != nil {
		f.Seek(int64(*req.Offset), io.SeekStart)
	}
	var data []byte
	if req.Limit != nil {
		data = make([]byte, *req.Limit)
		n, err := io.ReadFull(f, data)
		if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
			return nil, status.Errorf(codes.Internal, "read: %v", err)
		}
		data = data[:n]
	} else {
		data, err = io.ReadAll(f)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "read: %v", err)
		}
	}

	return &pb.ReadFileResponse{
		Data: data,
		Size: uint64(info.Size()),
		Mode: uint32(info.Mode()),
	}, nil
}

func (s *agentServer) Mkdir(_ context.Context, req *pb.MkdirRequest) (*pb.MkdirResponse, error) {
	if req.Path == "" {
		return nil, status.Error(codes.InvalidArgument, "path required")
	}
	mode := os.FileMode(0o755)
	if req.Mode != 0 {
		mode = os.FileMode(req.Mode)
	}
	var err error
	if req.All {
		err = os.MkdirAll(req.Path, mode)
	} else {
		err = os.Mkdir(req.Path, mode)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "mkdir: %v", err)
	}
	return &pb.MkdirResponse{}, nil
}

func (s *agentServer) Mount(_ context.Context, req *pb.MountRequest) (*pb.MountResponse, error) {
	args := []string{"-t", req.Type}
	if len(req.Options) > 0 {
		args = append(args, "-o", strings.Join(req.Options, ","))
	}
	args = append(args, req.Source, req.Destination)
	if err := os.MkdirAll(req.Destination, 0o755); err != nil {
		return nil, status.Errorf(codes.Internal, "mkdir mount point: %v", err)
	}
	out, err := exec.Command("mount", args...).CombinedOutput()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "mount: %v: %s", err, out)
	}
	return &pb.MountResponse{}, nil
}

func (s *agentServer) Unmount(_ context.Context, req *pb.UnmountRequest) (*pb.UnmountResponse, error) {
	args := []string{}
	if req.Force {
		args = append(args, "-f")
	}
	args = append(args, req.Path)
	out, err := exec.Command("umount", args...).CombinedOutput()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "umount: %v: %s", err, out)
	}
	return &pb.UnmountResponse{}, nil
}

func (s *agentServer) CopyIn(stream pb.Agent_CopyInServer) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.Internal, "recv init: %v", err)
	}
	init := first.GetInit()
	if init == nil {
		return status.Error(codes.InvalidArgument, "first message must be init")
	}

	if init.CreateParents {
		if err := os.MkdirAll(filepath.Dir(init.Path), 0o755); err != nil {
			return status.Errorf(codes.Internal, "mkdir: %v", err)
		}
	}

	mode := os.FileMode(0o644)
	if init.Mode != 0 {
		mode = os.FileMode(init.Mode)
	}
	f, err := os.OpenFile(init.Path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return status.Errorf(codes.Internal, "create: %v", err)
	}
	defer f.Close()

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return status.Errorf(codes.Internal, "recv: %v", err)
		}
		data := chunk.GetData()
		if len(data) > 0 {
			if _, err := f.Write(data); err != nil {
				return status.Errorf(codes.Internal, "write: %v", err)
			}
		}
	}

	return stream.SendAndClose(&pb.CopyInResponse{})
}

func (s *agentServer) CopyOut(req *pb.CopyOutRequest, stream pb.Agent_CopyOutServer) error {
	info, err := os.Stat(req.Path)
	if err != nil {
		return status.Errorf(codes.NotFound, "stat: %v", err)
	}

	f, err := os.Open(req.Path)
	if err != nil {
		return status.Errorf(codes.Internal, "open: %v", err)
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
			return status.Errorf(codes.Internal, "read: %v", err)
		}
	}
	return nil
}

func (s *agentServer) Shutdown(_ context.Context, req *pb.ShutdownRequest) (*pb.ShutdownResponse, error) {
	log.Println("shutdown requested")
	go func() {
		time.Sleep(500 * time.Millisecond)
		args := []string{"now"}
		if req.Force {
			args = []string{"-h", "now"}
		}
		exec.Command("shutdown", args...).Run()
	}()
	return &pb.ShutdownResponse{}, nil
}

func (s *agentServer) Reboot(_ context.Context, _ *pb.RebootRequest) (*pb.RebootResponse, error) {
	log.Println("reboot requested")
	go func() {
		time.Sleep(500 * time.Millisecond)
		exec.Command("shutdown", "-r", "now").Run()
	}()
	return &pb.RebootResponse{}, nil
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
