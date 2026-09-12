package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
	controlpb "github.com/tmc/cove/proto/controlpb"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type qemuAttachTestGuest struct {
	agentpbconnect.UnimplementedAgentHandler
	start        chan *pb.ExecRequest
	resize       chan *pb.ResizeExecTTYRequest
	signal       chan *pb.SignalExecRequest
	disconnected chan struct{}
}

func (g *qemuAttachTestGuest) Info(context.Context, *connect.Request[pb.InfoRequest]) (*connect.Response[pb.InfoResponse], error) {
	return connect.NewResponse(&pb.InfoResponse{Features: []string{"exec_attach"}}), nil
}
func (g *qemuAttachTestGuest) ExecAttach(ctx context.Context, stream *connect.BidiStream[pb.ExecAttachRequest, pb.ExecAttachOutput]) error {
	defer close(g.disconnected)
	req, err := stream.Receive()
	if err != nil {
		return err
	}
	g.start <- req.GetStart()
	if _, err := stream.Receive(); err != nil {
		return err
	}
	// Remain silent after stdin closes: only RPC cancellation can release this guest.
	<-ctx.Done()
	return ctx.Err()
}
func (g *qemuAttachTestGuest) ResizeExecTTY(ctx context.Context, req *connect.Request[pb.ResizeExecTTYRequest]) (*connect.Response[pb.ResizeExecTTYResponse], error) {
	g.resize <- req.Msg
	return connect.NewResponse(&pb.ResizeExecTTYResponse{}), nil
}
func (g *qemuAttachTestGuest) SignalExec(ctx context.Context, req *connect.Request[pb.SignalExecRequest]) (*connect.Response[pb.SignalExecResponse], error) {
	g.signal <- req.Msg
	return connect.NewResponse(&pb.SignalExecResponse{}), nil
}

func newQEMUAttachTestHandler(t *testing.T) (*windowsQEMUControlHandler, *qemuAttachTestGuest) {
	t.Helper()
	g := &qemuAttachTestGuest{start: make(chan *pb.ExecRequest, 1), resize: make(chan *pb.ResizeExecTTYRequest, 1), signal: make(chan *pb.SignalExecRequest, 1), disconnected: make(chan struct{})}
	_, handler := agentpbconnect.NewAgentHandler(g)
	server := httptest.NewServer(h2c.NewHandler(handler, &http2.Server{}))
	t.Cleanup(server.Close)
	addr := server.Listener.Addr().(*net.TCPAddr)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "qemu"), 0700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(windowsQEMUMetadata{AgentHostAddress: "127.0.0.1", AgentHostPort: addr.Port})
	if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	h := &windowsQEMUControlHandler{vmDir: dir}
	h.attach.startLifecycleContext()
	t.Cleanup(h.Close)
	return h, g
}

func TestQEMUExecAttachTransportDisconnect(t *testing.T) {
	for _, halfClose := range []bool{false, true} {
		t.Run(fmt.Sprintf("close_stdin=%v", halfClose), func(t *testing.T) {
			h, g := newQEMUAttachTestHandler(t)
			server, client := net.Pipe()
			defer client.Close()
			defer server.Close()
			client.SetDeadline(time.Now().Add(5 * time.Second))
			done := make(chan struct{})
			go func() {
				defer close(done)
				h.HandleStream(server, &controlpb.ControlRequest{Type: "agent-exec-attach"}, []byte(`{"exec_id":"terminal","args":["cmd.exe"],"env":{"TERM":"xterm"},"working_dir":"C:\\"}`))
			}()
			var response struct {
				Success bool
				Data    string
			}
			if err := json.NewDecoder(client).Decode(&response); err != nil {
				t.Fatal(err)
			}
			if !response.Success {
				t.Fatalf("attach response: %+v", response)
			}
			select {
			case req := <-g.start:
				if req.GetExecId() != "terminal" || !req.GetTty() || req.Args[0] != "cmd.exe" || req.Env["TERM"] != "xterm" || req.WorkingDir != "C:\\" {
					t.Fatalf("start = %v", req)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("missing start RPC")
			}
			// Closing stdin is distinct from disconnecting the client.
			if halfClose {
				if _, err := fmt.Fprintln(client, `{"type":"close_stdin","exec_id":"terminal"}`); err != nil {
					t.Fatal(err)
				}
			}
			// The reader must continue observing disconnect after the half-close.
			client.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("attach retained disconnected client")
			}
			select {
			case <-g.disconnected:
			case <-time.After(5 * time.Second):
				t.Fatal("guest RPC was not canceled")
			}
		})
	}
}

func TestQEMUExecAttachSidecars(t *testing.T) {
	h, g := newQEMUAttachTestHandler(t)
	for _, tt := range []struct{ name, raw string }{
		{"agent-exec-resize", `{"exec_id":"terminal","rows":40,"cols":120}`},
		{"agent-exec-signal", `{"exec_id":"terminal","signal":9}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resp, handled := h.HandleRaw(&controlpb.ControlRequest{Type: tt.name}, []byte(tt.raw))
			if !handled || !resp.Success {
				t.Fatalf("response = %v, handled = %v", resp, handled)
			}
		})
	}
	if req := <-g.resize; req.ExecId != "terminal" || req.Rows != 40 || req.Cols != 120 {
		t.Fatalf("resize = %v", req)
	}
	if req := <-g.signal; req.ExecId != "terminal" || req.Signal != 9 {
		t.Fatalf("signal = %v", req)
	}
	caps := h.capabilities().GetCapabilities()
	if !caps.Features["agentExecAttach"] {
		t.Fatal("attach capability missing")
	}
}
