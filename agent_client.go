// agent_client.go - Host-side GRPC client for the guest agent.
//
// Connects to the vz-agent daemon running inside the guest via
// VZVirtioSocketDevice on vsock port 1024.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	pb "github.com/tmc/vz-macos/proto/agentpb"
)

const agentPort = 1024

// AgentClient wraps the GRPC client for the guest agent.
type AgentClient struct {
	conn   *grpc.ClientConn
	client pb.AgentClient
}

// NewAgentClient creates a client connected to the guest agent.
// The netConn should be obtained from VZVirtioSocketDevice.ConnectToPort.
func NewAgentClient(netConn net.Conn) (*AgentClient, error) {
	// Use the existing connection as the GRPC transport.
	// passthrough scheme avoids gRPC's URL resolver which chokes on "vsock://guest:1024".
	conn, err := grpc.NewClient(
		"passthrough:///vsock-guest",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return netConn, nil
		}),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64*1024*1024)),
	)
	if err != nil {
		return nil, fmt.Errorf("grpc dial: %w", err)
	}
	return &AgentClient{
		conn:   conn,
		client: pb.NewAgentClient(conn),
	}, nil
}

// Close closes the gRPC connection.
func (c *AgentClient) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

// Ping checks that the agent is alive.
func (c *AgentClient) Ping(ctx context.Context) (string, error) {
	resp, err := c.client.Ping(ctx, &pb.PingRequest{})
	if err != nil {
		return "", err
	}
	return resp.Version, nil
}

// Info returns guest system information.
func (c *AgentClient) Info(ctx context.Context) (*pb.InfoResponse, error) {
	return c.client.Info(ctx, &pb.InfoRequest{})
}

// Exec runs a command in the guest and returns its output.
func (c *AgentClient) Exec(ctx context.Context, args []string, env map[string]string, workDir string) (*pb.ExecResponse, error) {
	return c.client.Exec(ctx, &pb.ExecRequest{
		Args:       args,
		Env:        env,
		WorkingDir: workDir,
	})
}

// ExecAs runs a command in the guest as the specified user.
func (c *AgentClient) ExecAs(ctx context.Context, user string, args []string, env map[string]string, workDir string) (*pb.ExecResponse, error) {
	return c.client.Exec(ctx, &pb.ExecRequest{
		Args:       args,
		Env:        env,
		WorkingDir: workDir,
		User:       &user,
	})
}

// WriteFile writes data to a file in the guest.
func (c *AgentClient) WriteFile(ctx context.Context, path string, data []byte, mode uint32) error {
	_, err := c.client.WriteFile(ctx, &pb.WriteFileRequest{
		Path:          path,
		Data:          data,
		Mode:          mode,
		CreateParents: true,
	})
	return err
}

// ReadFile reads a file from the guest.
func (c *AgentClient) ReadFile(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.client.ReadFile(ctx, &pb.ReadFileRequest{Path: path})
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// CopyToGuest streams a local file into the guest at guestPath.
func (c *AgentClient) CopyToGuest(ctx context.Context, localPath, guestPath string, mode os.FileMode) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	stream, err := c.client.CopyIn(ctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

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

	if _, err := stream.CloseAndRecv(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return nil
}

// CopyFromGuest streams a file from the guest to a local path.
func (c *AgentClient) CopyFromGuest(ctx context.Context, guestPath, localPath string) error {
	stream, err := c.client.CopyOut(ctx, &pb.CopyOutRequest{Path: guestPath})
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("recv init: %w", err)
	}
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

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("recv: %w", err)
		}
		if data := chunk.GetData(); len(data) > 0 {
			if _, err := f.Write(data); err != nil {
				return fmt.Errorf("write local: %w", err)
			}
		}
	}
	return nil
}

// Shutdown initiates a graceful shutdown.
func (c *AgentClient) Shutdown(ctx context.Context, force bool) error {
	_, err := c.client.Shutdown(ctx, &pb.ShutdownRequest{Force: force})
	return err
}

// Reboot initiates a guest reboot.
func (c *AgentClient) Reboot(ctx context.Context) error {
	_, err := c.client.Reboot(ctx, &pb.RebootRequest{})
	return err
}
