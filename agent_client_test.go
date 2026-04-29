package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	pb "github.com/tmc/vz-macos/proto/agentpb"
	"github.com/tmc/vz-macos/proto/agentpbconnect"
)

func TestAgentClientCloseClosesUnderlyingConn(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	c, err := NewAgentClient(client)
	if err != nil {
		t.Fatalf("NewAgentClient() error = %v", err)
	}
	c.Close()

	buf := make([]byte, 1)
	_, err = server.Read(buf)
	if err == nil || (!errors.Is(err, io.EOF) && !strings.Contains(err.Error(), "closed pipe")) {
		t.Fatalf("server.Read() error = %v, want EOF/closed pipe", err)
	}
}

func TestAgentClientExecSupportsParallelRPCs(t *testing.T) {
	t.Parallel()

	handler := &testAgentHandler{
		started: make(chan string, 2),
		release: make(chan struct{}),
	}
	client := newTestAgentClient(t, handler)
	defer client.Close()

	assertParallelExecs(t,
		func(ctx context.Context, arg string) (string, error) {
			resp, err := client.Exec(ctx, []string{arg}, nil, "")
			if err != nil {
				return "", err
			}
			return string(resp.Stdout), nil
		},
		handler.started,
		handler.release,
	)
}

func TestUserAgentClientExecSupportsParallelRPCs(t *testing.T) {
	t.Parallel()

	handler := &testUserAgentHandler{
		started: make(chan string, 2),
		release: make(chan struct{}),
	}
	client := newTestUserAgentClient(t, handler)
	defer client.Close()

	assertParallelExecs(t,
		func(ctx context.Context, arg string) (string, error) {
			resp, err := client.UserExec(ctx, []string{arg}, nil, "")
			if err != nil {
				return "", err
			}
			return string(resp.Stdout), nil
		},
		handler.started,
		handler.release,
	)
}

type execFunc func(context.Context, string) (string, error)

func assertParallelExecs(t *testing.T, exec execFunc, started <-chan string, release chan struct{}) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type result struct {
		arg string
		out string
		err error
	}
	results := make(chan result, 2)
	for _, arg := range []string{"first", "second"} {
		go func(arg string) {
			out, err := exec(ctx, arg)
			results <- result{arg: arg, out: out, err: err}
		}(arg)
	}

	got := make(map[string]bool, 2)
	deadline := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case arg := <-started:
			got[arg] = true
		case <-deadline:
			t.Fatalf("parallel execs did not overlap, got starts for %v", got)
		}
	}
	close(release)

	for range 2 {
		res := <-results
		if res.err != nil {
			t.Fatalf("%s exec: %v", res.arg, res.err)
		}
		if res.out != res.arg+"\n" {
			t.Fatalf("%s stdout = %q, want %q", res.arg, res.out, res.arg+"\n")
		}
	}
}

func TestAgentClientExecControlRPCs(t *testing.T) {
	h := &controlAgentHandler{}
	client := newTestAgentClient(t, h)

	ctx := context.Background()
	if err := client.ResizeExec(ctx, "exec-1", 24, 80); err != nil {
		t.Fatalf("ResizeExec: %v", err)
	}
	if err := client.SignalExec(ctx, "exec-1", 2); err != nil {
		t.Fatalf("SignalExec: %v", err)
	}
	wantTime := time.Unix(1700000000, 123000000).UTC()
	if err := client.SetTime(ctx, wantTime); err != nil {
		t.Fatalf("SetTime: %v", err)
	}

	if h.resizeExecID != "exec-1" || h.resizeRows != 24 || h.resizeCols != 80 {
		t.Fatalf("resize = (%q, %d, %d), want (exec-1, 24, 80)", h.resizeExecID, h.resizeRows, h.resizeCols)
	}
	if h.signalExecID != "exec-1" || h.signal != 2 {
		t.Fatalf("signal = (%q, %d), want (exec-1, 2)", h.signalExecID, h.signal)
	}
	if !h.setTime.Equal(wantTime) {
		t.Fatalf("setTime = %v, want %v", h.setTime, wantTime)
	}
}

type testAgentHandler struct {
	agentpbconnect.UnimplementedAgentHandler
	started chan string
	release chan struct{}
}

func (h *testAgentHandler) Ping(context.Context, *connect.Request[pb.PingRequest]) (*connect.Response[pb.PingResponse], error) {
	return connect.NewResponse(&pb.PingResponse{Version: "test"}), nil
}

func (h *testAgentHandler) Exec(ctx context.Context, req *connect.Request[pb.ExecRequest]) (*connect.Response[pb.ExecResponse], error) {
	return connect.NewResponse(&pb.ExecResponse{
		ExitCode: 0,
		Stdout:   []byte(h.wait(req.Msg.Args)),
	}), nil
}

type testUserAgentHandler struct {
	agentpbconnect.UnimplementedUserAgentHandler
	started chan string
	release chan struct{}
}

type controlAgentHandler struct {
	agentpbconnect.UnimplementedAgentHandler
	resizeExecID string
	resizeRows   uint32
	resizeCols   uint32
	signalExecID string
	signal       int32
	setTime      time.Time
}

func (h *controlAgentHandler) ResizeExecTTY(ctx context.Context, req *connect.Request[pb.ResizeExecTTYRequest]) (*connect.Response[pb.ResizeExecTTYResponse], error) {
	h.resizeExecID = req.Msg.GetExecId()
	h.resizeRows = req.Msg.GetRows()
	h.resizeCols = req.Msg.GetCols()
	return connect.NewResponse(&pb.ResizeExecTTYResponse{}), nil
}

func (h *controlAgentHandler) SignalExec(ctx context.Context, req *connect.Request[pb.SignalExecRequest]) (*connect.Response[pb.SignalExecResponse], error) {
	h.signalExecID = req.Msg.GetExecId()
	h.signal = req.Msg.GetSignal()
	return connect.NewResponse(&pb.SignalExecResponse{}), nil
}

func (h *controlAgentHandler) SetTime(ctx context.Context, req *connect.Request[pb.SetTimeRequest]) (*connect.Response[pb.SetTimeResponse], error) {
	h.setTime = req.Msg.GetTime().AsTime()
	return connect.NewResponse(&pb.SetTimeResponse{}), nil
}

func (h *testUserAgentHandler) UserExec(ctx context.Context, req *connect.Request[pb.ExecRequest]) (*connect.Response[pb.ExecResponse], error) {
	return connect.NewResponse(&pb.ExecResponse{
		ExitCode: 0,
		Stdout:   []byte(h.wait(req.Msg.Args)),
	}), nil
}

func (h *testAgentHandler) wait(args []string) string {
	return waitForRelease(h.started, h.release, args)
}

func (h *testUserAgentHandler) wait(args []string) string {
	return waitForRelease(h.started, h.release, args)
}

func waitForRelease(started chan<- string, release <-chan struct{}, args []string) string {
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	started <- arg
	<-release
	return arg + "\n"
}

func newTestAgentClient(t *testing.T, handler agentpbconnect.AgentHandler) *AgentClient {
	t.Helper()
	addr := startAgentHTTP2Server(t, func(mux *http.ServeMux) {
		path, h := agentpbconnect.NewAgentHandler(handler)
		mux.Handle(path, h)
	})
	client, err := NewAgentClientWithDial(func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	})
	if err != nil {
		t.Fatalf("NewAgentClientWithDial() error = %v", err)
	}
	return client
}

func newTestUserAgentClient(t *testing.T, handler agentpbconnect.UserAgentHandler) *UserAgentClient {
	t.Helper()
	addr := startAgentHTTP2Server(t, func(mux *http.ServeMux) {
		path, h := agentpbconnect.NewUserAgentHandler(handler)
		mux.Handle(path, h)
	})
	client, err := NewUserAgentClientWithDial(func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	})
	if err != nil {
		t.Fatalf("NewUserAgentClientWithDial() error = %v", err)
	}
	return client
}

func startAgentHTTP2Server(t *testing.T, register func(*http.ServeMux)) string {
	t.Helper()

	mux := http.NewServeMux()
	register(mux)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}

	srv := &http.Server{
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}
	var serveWG sync.WaitGroup
	serveWG.Add(1)
	go func() {
		defer serveWG.Done()
		_ = srv.Serve(ln)
	}()

	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		serveWG.Wait()
	})
	return ln.Addr().String()
}
