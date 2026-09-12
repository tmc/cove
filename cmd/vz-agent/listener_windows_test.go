package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
)

func TestListenAgentWindows(t *testing.T) {
	for _, tt := range []struct {
		name    string
		port    uint32
		addr    string
		wantErr bool
	}{
		{name: "default ephemeral"},
		{name: "explicit address overrides port", port: 70000, addr: "127.0.0.1:0"},
		{name: "invalid default port", port: 70000, wantErr: true},
		{name: "missing port", addr: "127.0.0.1", wantErr: true},
		{name: "invalid explicit port", addr: "127.0.0.1:70000", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			lis, err := listenAgent(tt.port, tt.addr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("listenAgent() error = %v, want error %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			defer lis.Close()
			addr := lis.Addr().(*net.TCPAddr)
			if addr.Port == 0 {
				t.Fatal("listener has no assigned port")
			}
			if tt.addr != "" && !addr.IP.IsLoopback() {
				t.Fatalf("listener address = %v, want loopback", addr)
			}
		})
	}
}

func TestWindowsAgentTCP(t *testing.T) {
	t.Setenv("COVE_TRANSPORT_ENV", "inherited")
	t.Setenv("COVE_TRANSPORT_PRESERVED", "preserved")
	for _, protocol := range []string{"http1", "h2c"} {
		t.Run(protocol, func(t *testing.T) {
			lis, err := listenAgent(0, "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			path, handler := agentpbconnect.NewAgentHandler(newAgentServer())
			mux.Handle(path, handler)
			userPath, userHandler := agentpbconnect.NewUserAgentHandler(newUserAgentServer())
			mux.Handle(userPath, userHandler)
			srv := &http.Server{Handler: h2c.NewHandler(mux, &http2.Server{})}
			done := make(chan error, 1)
			go func() { done <- srv.Serve(lis) }()
			t.Cleanup(func() {
				srv.Close()
				if err := <-done; !errors.Is(err, http.ErrServerClosed) {
					t.Errorf("serve: %v", err)
				}
			})
			var transport interface {
				http.RoundTripper
				CloseIdleConnections()
			}
			if protocol == "h2c" {
				transport = &http2.Transport{
					AllowHTTP: true,
					DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, network, addr)
					},
				}
			} else {
				transport = &http.Transport{}
			}
			t.Cleanup(transport.CloseIdleConnections)
			client := agentpbconnect.NewAgentClient(&http.Client{Transport: transport}, "http://"+lis.Addr().String())
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ping, err := client.Ping(ctx, connect.NewRequest(&pb.PingRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			if ping.Msg.GetTimestamp() == nil {
				t.Fatal("ping timestamp is missing")
			}
			req := &pb.ExecRequest{Args: []string{"cmd.exe", "/d", "/c", "echo transport-ok& exit /b 7"}}
			result, err := client.Exec(ctx, connect.NewRequest(req))
			if err != nil {
				t.Fatal(err)
			}
			if result.Msg.GetExitCode() != 7 || strings.TrimSpace(string(result.Msg.GetStdout())) != "transport-ok" {
				t.Fatalf("exec result = %v", result.Msg)
			}
			stream, err := client.ExecStream(ctx, connect.NewRequest(req))
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			var stdout strings.Builder
			var exitCode *int32
			for stream.Receive() {
				msg := stream.Msg()
				if msg.GetStream() == pb.ExecOutput_STDOUT {
					stdout.Write(msg.GetData())
				}
				if msg.ExitCode != nil {
					code := msg.GetExitCode()
					exitCode = &code
				}
			}
			if err := stream.Err(); err != nil {
				t.Fatal(err)
			}
			if exitCode == nil || *exitCode != 7 || strings.TrimSpace(stdout.String()) != "transport-ok" {
				t.Fatalf("stream stdout = %q, exit code = %v", stdout.String(), exitCode)
			}
			userClient := agentpbconnect.NewUserAgentClient(&http.Client{Transport: transport}, "http://"+lis.Addr().String())
			userReq := &pb.ExecRequest{
				Args: []string{"cmd.exe", "/d", "/c", "echo %COVE_TRANSPORT_ENV%-%COVE_TRANSPORT_PRESERVED%"},
				Env:  map[string]string{"COVE_TRANSPORT_ENV": "overridden"},
			}
			userResult, err := userClient.UserExec(ctx, connect.NewRequest(userReq))
			if err != nil {
				t.Fatal(err)
			}
			if userResult.Msg.GetExitCode() != 0 || strings.TrimSpace(string(userResult.Msg.GetStdout())) != "overridden-preserved" {
				t.Fatalf("user exec result = %v", userResult.Msg)
			}
			userStream, err := userClient.UserExecStream(ctx, connect.NewRequest(userReq))
			if err != nil {
				t.Fatal(err)
			}
			defer userStream.Close()
			stdout.Reset()
			exitCode = nil
			for userStream.Receive() {
				msg := userStream.Msg()
				if msg.GetStream() == pb.ExecOutput_STDOUT {
					stdout.Write(msg.GetData())
				}
				if msg.ExitCode != nil {
					code := msg.GetExitCode()
					exitCode = &code
				}
			}
			if err := userStream.Err(); err != nil {
				t.Fatal(err)
			}
			if exitCode == nil || *exitCode != 0 || strings.TrimSpace(stdout.String()) != "overridden-preserved" {
				t.Fatalf("user stream stdout = %q, exit code = %v", stdout.String(), exitCode)
			}

		})
	}
}
