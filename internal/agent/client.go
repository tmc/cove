// agent_client.go - Host-side connect-go client for the guest agent.
//
// Connects to the vz-agent daemon running inside the guest via
// VZVirtioSocketDevice on vsock port 1024.
package agent

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
)

const DaemonPort = 1024
const UserPort = 1025
const defaultAgentExecArgvLimit = 16 * 1024
const agentExecArgvLimitEnv = "COVE_AGENT_EXEC_ARGV_LIMIT"

// AgentClient wraps the connect-go client for the guest agent.
type AgentClient struct {
	client    agentpbconnect.AgentClient
	transport *http2.Transport
	closeFn   func()
	closeOnce sync.Once
}

func checkAgentExecArgv(args []string) error {
	limit := agentExecArgvLimit()
	got := argvBytes(args)
	if got <= limit {
		return nil
	}
	return fmt.Errorf("agent-exec: argv exceeds %d bytes (got %d); use 'cove agent-cp' or 'cove agent-write' for large blobs, or pipe via stdin if your command supports it", limit, got)
}

func agentExecArgvLimit() int {
	raw := os.Getenv(agentExecArgvLimitEnv)
	if raw == "" {
		return defaultAgentExecArgvLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultAgentExecArgvLimit
	}
	return n
}

func argvBytes(args []string) int {
	var n int
	for _, arg := range args {
		n += len(arg)
	}
	return n
}

// NewAgentClient creates a client connected to the guest agent.
// The netConn should be obtained from VZVirtioSocketDevice.ConnectToPort.
func NewAgentClient(netConn net.Conn) (*AgentClient, error) {
	dial, closeFn, err := newOneShotConnDialer(netConn)
	if err != nil {
		return nil, err
	}
	return NewAgentClientWithDial(dial, closeFn)
}

// NewAgentClientWithDial creates a client that dials a fresh connection as needed.
func NewAgentClientWithDial(dial func(context.Context) (net.Conn, error), closeFns ...func()) (*AgentClient, error) {
	if dial == nil {
		return nil, fmt.Errorf("nil dialer")
	}
	httpClient, transport := newH2CClientWithDial(dial)
	client := agentpbconnect.NewAgentClient(
		httpClient,
		"http://vsock-guest",
		connect.WithGRPC(),
	)
	var closeFn func()
	if len(closeFns) > 0 {
		closeFn = closeFns[0]
	}
	return &AgentClient{client: client, transport: transport, closeFn: closeFn}, nil
}

// Close closes the underlying vsock connection.
func (c *AgentClient) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		if c.closeFn != nil {
			c.closeFn()
		}
		if c.transport != nil {
			c.transport.CloseIdleConnections()
		}
	})
}

// Ping checks that the agent is alive.
func (c *AgentClient) Ping(ctx context.Context) (string, error) {
	resp, err := c.client.Ping(ctx, connect.NewRequest(&pb.PingRequest{}))
	if err != nil {
		return "", err
	}
	return resp.Msg.Version, nil
}

// Info returns guest system information.
func (c *AgentClient) Info(ctx context.Context) (*pb.InfoResponse, error) {
	resp, err := c.client.Info(ctx, connect.NewRequest(&pb.InfoRequest{}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// Exec runs a command in the guest and returns its output.
func (c *AgentClient) Exec(ctx context.Context, args []string, env map[string]string, workDir string) (*pb.ExecResponse, error) {
	if err := checkAgentExecArgv(args); err != nil {
		return nil, err
	}
	resp, err := c.client.Exec(ctx, connect.NewRequest(&pb.ExecRequest{
		Args:       args,
		Env:        env,
		WorkingDir: workDir,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// execStreamReceiver wraps connect's ServerStreamForClient to provide a Recv() method
// compatible with callers that expect the gRPC streaming interface.
type execStreamReceiver struct {
	stream *connect.ServerStreamForClient[pb.ExecOutput]
}

func (r *execStreamReceiver) Recv() (*pb.ExecOutput, error) {
	if r.stream.Receive() {
		return r.stream.Msg(), nil
	}
	if err := r.stream.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

// ExecStreamReceiver is the interface returned by ExecStream.
type ExecStreamReceiver interface {
	Recv() (*pb.ExecOutput, error)
}

type execAttachStream struct {
	stream *connect.BidiStreamForClient[pb.ExecAttachRequest, pb.ExecAttachOutput]
}

func (s *execAttachStream) Recv() (*pb.ExecOutput, error) {
	msg, err := s.stream.Receive()
	if err != nil {
		return nil, err
	}
	switch out := msg.GetOutput().(type) {
	case *pb.ExecAttachOutput_Stdout:
		return &pb.ExecOutput{Stream: pb.ExecOutput_STDOUT, Data: out.Stdout.GetData()}, nil
	case *pb.ExecAttachOutput_Stderr:
		return &pb.ExecOutput{Stream: pb.ExecOutput_STDERR, Data: out.Stderr.GetData()}, nil
	case *pb.ExecAttachOutput_ExitStatus:
		exit := out.ExitStatus.GetExitCode()
		return &pb.ExecOutput{ExitCode: &exit}, nil
	default:
		return &pb.ExecOutput{}, nil
	}
}

func (s *execAttachStream) SendStdin(data []byte) error {
	return s.stream.Send(&pb.ExecAttachRequest{
		Request: &pb.ExecAttachRequest_Stdin{Stdin: &pb.StdinChunk{Data: data}},
	})
}

func (s *execAttachStream) SendResize(rows, cols uint32) error {
	return s.stream.Send(&pb.ExecAttachRequest{
		Request: &pb.ExecAttachRequest_Resize{Resize: &pb.ResizeRequest{Rows: rows, Cols: cols}},
	})
}

func (s *execAttachStream) SendSignal(signal int32) error {
	return s.stream.Send(&pb.ExecAttachRequest{
		Request: &pb.ExecAttachRequest_Signal{Signal: &pb.SignalRequest{Signal: signal}},
	})
}

func (s *execAttachStream) CloseStdin() error {
	if err := s.stream.Send(&pb.ExecAttachRequest{
		Request: &pb.ExecAttachRequest_CloseStdin{CloseStdin: &pb.CloseStdinRequest{}},
	}); err != nil {
		return err
	}
	return s.stream.CloseRequest()
}

type ExecAttachStream interface {
	ExecStreamReceiver
	SendStdin([]byte) error
	SendResize(rows, cols uint32) error
	SendSignal(signal int32) error
	CloseStdin() error
}

// ExecStream runs a command in the guest and streams stdout/stderr chunks.
func (c *AgentClient) ExecStream(ctx context.Context, args []string, env map[string]string, workDir string) (ExecStreamReceiver, error) {
	return c.ExecStreamAs(ctx, "", args, env, workDir)
}

// ExecStreamAs runs a command in the guest as the specified user and streams output.
func (c *AgentClient) ExecStreamAs(ctx context.Context, user string, args []string, env map[string]string, workDir string) (ExecStreamReceiver, error) {
	return c.ExecStreamControl(ctx, "", false, user, args, env, workDir)
}

func (c *AgentClient) ExecStreamControl(ctx context.Context, execID string, tty bool, user string, args []string, env map[string]string, workDir string) (ExecStreamReceiver, error) {
	if err := checkAgentExecArgv(args); err != nil {
		return nil, err
	}
	req := &pb.ExecRequest{
		Args:       args,
		Env:        env,
		WorkingDir: workDir,
		ExecId:     execID,
		Tty:        tty,
	}
	if user != "" {
		req.User = &user
	}
	stream, err := c.client.ExecStream(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, err
	}
	return &execStreamReceiver{stream: stream}, nil
}

func (c *AgentClient) ExecAttachSupported(ctx context.Context) (bool, error) {
	info, err := c.Info(ctx)
	if err != nil {
		return false, err
	}
	for _, f := range info.GetFeatures() {
		if f == "exec_attach" || f == "exec_attach_v3" {
			return true, nil
		}
	}
	return false, nil
}

func (c *AgentClient) ExecAttachControl(ctx context.Context, execID string, tty bool, user string, args []string, env map[string]string, workDir string) (ExecAttachStream, error) {
	if err := checkAgentExecArgv(args); err != nil {
		return nil, err
	}
	req := &pb.ExecRequest{
		Args:       args,
		Env:        env,
		WorkingDir: workDir,
		ExecId:     execID,
		Tty:        tty,
	}
	if user != "" {
		req.User = &user
	}
	stream := c.client.ExecAttach(ctx)
	if err := stream.Send(&pb.ExecAttachRequest{
		Request: &pb.ExecAttachRequest_Start{Start: req},
	}); err != nil {
		return nil, err
	}
	return &execAttachStream{stream: stream}, nil
}

func (c *AgentClient) ResizeExec(ctx context.Context, execID string, rows, cols uint32) error {
	_, err := c.client.ResizeExecTTY(ctx, connect.NewRequest(&pb.ResizeExecTTYRequest{
		ExecId: execID,
		Rows:   rows,
		Cols:   cols,
	}))
	return err
}

func (c *AgentClient) SignalExec(ctx context.Context, execID string, signal int32) error {
	_, err := c.client.SignalExec(ctx, connect.NewRequest(&pb.SignalExecRequest{
		ExecId: execID,
		Signal: signal,
	}))
	return err
}

func (c *AgentClient) SetTime(ctx context.Context, t time.Time) error {
	_, err := c.client.SetTime(ctx, connect.NewRequest(&pb.SetTimeRequest{
		Time: timestamppb.New(t),
	}))
	return err
}

func (c *AgentClient) ResizeMacOSAPFS(ctx context.Context, preflightOnly bool) (*pb.ResizeMacOSAPFSResponse, error) {
	resp, err := c.client.ResizeMacOSAPFS(ctx, connect.NewRequest(&pb.ResizeMacOSAPFSRequest{
		PreflightOnly: preflightOnly,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// ExecAs runs a command in the guest as the specified user.
func (c *AgentClient) ExecAs(ctx context.Context, user string, args []string, env map[string]string, workDir string) (*pb.ExecResponse, error) {
	if err := checkAgentExecArgv(args); err != nil {
		return nil, err
	}
	resp, err := c.client.Exec(ctx, connect.NewRequest(&pb.ExecRequest{
		Args:       args,
		Env:        env,
		WorkingDir: workDir,
		User:       &user,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// WriteFile writes data to a file in the guest.
func (c *AgentClient) WriteFile(ctx context.Context, path string, data []byte, mode uint32) error {
	_, err := c.client.WriteFile(ctx, connect.NewRequest(&pb.WriteFileRequest{
		Path:          path,
		Data:          data,
		Mode:          mode,
		CreateParents: true,
	}))
	return err
}

// ReadFile reads a file from the guest.
func (c *AgentClient) ReadFile(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.client.ReadFile(ctx, connect.NewRequest(&pb.ReadFileRequest{Path: path}))
	if err != nil {
		return nil, err
	}
	return resp.Msg.Data, nil
}

// CopyToGuest streams a local file into the guest at guestPath.
func (c *AgentClient) CopyToGuest(ctx context.Context, localPath, guestPath string, mode os.FileMode) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}

	stream := c.client.CopyIn(ctx)

	if err := stream.Send(&pb.CopyInChunk{
		Content: &pb.CopyInChunk_Init{Init: &pb.CopyInInit{
			Path:          guestPath,
			Mode:          uint32(mode),
			CreateParents: true,
		}},
	}); err != nil {
		return fmt.Errorf("send init: %w", err)
	}

	var sent int64
	total := fi.Size()
	start := time.Now()
	lastLog := start

	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&pb.CopyInChunk{
				Content: &pb.CopyInChunk_Data{Data: buf[:n]},
			}); sendErr != nil {
				return fmt.Errorf("send data: %w", sendErr)
			}
			sent += int64(n)
			if now := time.Now(); now.Sub(lastLog) >= 3*time.Second {
				logCopyProgress(localPath, sent, total, start)
				lastLog = now
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read local: %w", err)
		}
	}

	if _, err := stream.CloseAndReceive(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	logCopyDone(localPath, sent, start)
	return nil
}

// CopyReaderToGuest streams data from an io.Reader to a guest file path.
func (c *AgentClient) CopyReaderToGuest(ctx context.Context, r io.Reader, guestPath string, mode os.FileMode) error {
	stream := c.client.CopyIn(ctx)

	if err := stream.Send(&pb.CopyInChunk{
		Content: &pb.CopyInChunk_Init{Init: &pb.CopyInInit{
			Path:          guestPath,
			Mode:          uint32(mode),
			CreateParents: true,
		}},
	}); err != nil {
		return fmt.Errorf("send init: %w", err)
	}

	var sent int64
	start := time.Now()
	lastLog := start

	buf := make([]byte, 256*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&pb.CopyInChunk{
				Content: &pb.CopyInChunk_Data{Data: buf[:n]},
			}); sendErr != nil {
				return fmt.Errorf("send data: %w", sendErr)
			}
			sent += int64(n)
			if now := time.Now(); now.Sub(lastLog) >= 3*time.Second {
				logCopyProgress(guestPath, sent, 0, start)
				lastLog = now
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
	}

	if _, err := stream.CloseAndReceive(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	logCopyDone(guestPath, sent, start)
	return nil
}

// CopyFromGuest streams a file from the guest to a local path.
func (c *AgentClient) CopyFromGuest(ctx context.Context, guestPath, localPath string) error {
	stream, err := c.client.CopyOut(ctx, connect.NewRequest(&pb.CopyOutRequest{Path: guestPath}))
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	if !stream.Receive() {
		if err := stream.Err(); err != nil {
			return fmt.Errorf("recv init: %w", err)
		}
		return fmt.Errorf("expected init message")
	}
	first := stream.Msg()
	init := first.GetInit()
	if init == nil {
		return fmt.Errorf("expected init message")
	}

	mode := os.FileMode(init.Mode)
	if mode == 0 {
		mode = 0644
	}

	f, err := os.OpenFile(localPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()

	for stream.Receive() {
		chunk := stream.Msg()
		if data := chunk.GetData(); len(data) > 0 {
			if _, err := f.Write(data); err != nil {
				return fmt.Errorf("write local: %w", err)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("recv: %w", err)
	}
	return nil
}

// Shutdown initiates a graceful shutdown.
func (c *AgentClient) Shutdown(ctx context.Context, force bool) error {
	_, err := c.client.Shutdown(ctx, connect.NewRequest(&pb.ShutdownRequest{Force: force}))
	return err
}

// Reboot initiates a guest reboot.
func (c *AgentClient) Reboot(ctx context.Context) error {
	_, err := c.client.Reboot(ctx, connect.NewRequest(&pb.RebootRequest{}))
	return err
}

// UserAgentClient wraps the connect-go client for the user session agent (port 1025).
type UserAgentClient struct {
	client    agentpbconnect.UserAgentClient
	transport *http2.Transport
	closeFn   func()
	closeOnce sync.Once
}

// NewUserAgentClientWithDial creates a user-agent client that dials a fresh connection as needed.
func NewUserAgentClientWithDial(dial func(context.Context) (net.Conn, error), closeFns ...func()) (*UserAgentClient, error) {
	if dial == nil {
		return nil, fmt.Errorf("nil dialer")
	}
	httpClient, transport := newH2CClientWithDial(dial)
	client := agentpbconnect.NewUserAgentClient(
		httpClient,
		"http://vsock-guest-user",
		connect.WithGRPC(),
	)
	var closeFn func()
	if len(closeFns) > 0 {
		closeFn = closeFns[0]
	}
	return &UserAgentClient{client: client, transport: transport, closeFn: closeFn}, nil
}

// Close closes the underlying vsock connection.
func (c *UserAgentClient) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		if c.closeFn != nil {
			c.closeFn()
		}
		if c.transport != nil {
			c.transport.CloseIdleConnections()
		}
	})
}

// logCopyProgress logs periodic transfer progress.
// If total is 0, only bytes sent and rate are shown (unknown total).
func logCopyProgress(name string, sent, total int64, start time.Time) {
	elapsed := time.Since(start).Seconds()
	rateMB := float64(sent) / (1024 * 1024) / elapsed
	sentMB := float64(sent) / (1024 * 1024)
	if total > 0 {
		totalMB := float64(total) / (1024 * 1024)
		pct := float64(sent) / float64(total) * 100
		log.Printf("agent-cp: %s %.0f/%.0f MB (%.0f%%) %.1f MB/s", name, sentMB, totalMB, pct, rateMB)
	} else {
		log.Printf("agent-cp: %s %.0f MB sent, %.1f MB/s", name, sentMB, rateMB)
	}
}

// logCopyDone logs the final transfer summary.
func logCopyDone(name string, sent int64, start time.Time) {
	elapsed := time.Since(start)
	sentMB := float64(sent) / (1024 * 1024)
	rateMB := sentMB / elapsed.Seconds()
	log.Printf("agent-cp: %s done: %.0f MB in %s (%.1f MB/s)", name, sentMB, elapsed.Round(time.Millisecond), rateMB)
}

// UserExec runs a command in the user session context (inherits TCC/FDA).
func (c *UserAgentClient) UserExec(ctx context.Context, args []string, env map[string]string, workDir string) (*pb.ExecResponse, error) {
	if err := checkAgentExecArgv(args); err != nil {
		return nil, err
	}
	resp, err := c.client.UserExec(ctx, connect.NewRequest(&pb.ExecRequest{
		Args:       args,
		Env:        env,
		WorkingDir: workDir,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

// UserExecStream runs a command in user context with streaming output.
func (c *UserAgentClient) UserExecStream(ctx context.Context, args []string, env map[string]string, workDir string) (ExecStreamReceiver, error) {
	if err := checkAgentExecArgv(args); err != nil {
		return nil, err
	}
	stream, err := c.client.UserExecStream(ctx, connect.NewRequest(&pb.ExecRequest{
		Args:       args,
		Env:        env,
		WorkingDir: workDir,
	}))
	if err != nil {
		return nil, err
	}
	return &execStreamReceiver{stream: stream}, nil
}

func newH2CClientWithDial(dial func(context.Context) (net.Conn, error)) (*http.Client, *http2.Transport) {
	transport := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return dial(ctx)
		},
	}
	return &http.Client{Transport: transport}, transport
}

func newOneShotConnDialer(netConn net.Conn) (func(context.Context) (net.Conn, error), func(), error) {
	if netConn == nil {
		return nil, nil, fmt.Errorf("nil conn")
	}
	var (
		mu     sync.Mutex
		conn   = netConn
		used   bool
		closed bool
	)
	dial := func(ctx context.Context) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if closed {
			return nil, fmt.Errorf("client closed")
		}
		if used {
			return nil, fmt.Errorf("connection already used")
		}
		used = true
		return conn, nil
	}
	closeFn := func() {
		mu.Lock()
		defer mu.Unlock()
		closed = true
		if !used && conn != nil {
			conn.Close()
			conn = nil
		}
	}
	return dial, closeFn, nil
}
