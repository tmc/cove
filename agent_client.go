// agent_client.go - Host-side connect-go client for the guest agent.
//
// Connects to the vz-agent daemon running inside the guest via
// VZVirtioSocketDevice on vsock port 1024.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"

	pb "github.com/tmc/vz-macos/proto/agentpb"
	"github.com/tmc/vz-macos/proto/agentpbconnect"
)

const agentPort = 1024

// AgentClient wraps the connect-go client for the guest agent.
type AgentClient struct {
	client agentpbconnect.AgentClient
	conn   net.Conn
}

// NewAgentClient creates a client connected to the guest agent.
// The netConn should be obtained from VZVirtioSocketDevice.ConnectToPort.
func NewAgentClient(netConn net.Conn) (*AgentClient, error) {
	httpClient := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return netConn, nil
			},
		},
	}
	client := agentpbconnect.NewAgentClient(
		httpClient,
		"http://vsock-guest",
		connect.WithGRPC(),
	)
	return &AgentClient{client: client, conn: netConn}, nil
}

// Close closes the underlying vsock connection.
func (c *AgentClient) Close() {
	if c == nil || c.conn == nil {
		return
	}
	c.conn.Close()
	c.conn = nil
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

// ExecStream runs a command in the guest and streams stdout/stderr chunks.
func (c *AgentClient) ExecStream(ctx context.Context, args []string, env map[string]string, workDir string) (ExecStreamReceiver, error) {
	return c.ExecStreamAs(ctx, "", args, env, workDir)
}

// ExecStreamAs runs a command in the guest as the specified user and streams output.
func (c *AgentClient) ExecStreamAs(ctx context.Context, user string, args []string, env map[string]string, workDir string) (ExecStreamReceiver, error) {
	req := &pb.ExecRequest{
		Args:       args,
		Env:        env,
		WorkingDir: workDir,
	}
	if user != "" {
		req.User = &user
	}
	stream, err := c.client.ExecStream(ctx, connect.NewRequest(&pb.ExecRequest{
		Args:       req.Args,
		Env:        req.Env,
		WorkingDir: req.WorkingDir,
		User:       req.User,
	}))
	if err != nil {
		return nil, err
	}
	return &execStreamReceiver{stream: stream}, nil
}

// ExecAs runs a command in the guest as the specified user.
func (c *AgentClient) ExecAs(ctx context.Context, user string, args []string, env map[string]string, workDir string) (*pb.ExecResponse, error) {
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

	buf := make([]byte, 64*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if sendErr := stream.Send(&pb.CopyInChunk{
				Content: &pb.CopyInChunk_Data{Data: buf[:n]},
			}); sendErr != nil {
				return fmt.Errorf("send data: %w", sendErr)
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
