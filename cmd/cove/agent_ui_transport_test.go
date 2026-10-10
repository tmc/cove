package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
	controlpb "github.com/tmc/cove/proto/controlpb"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type readOnlyUIFixture struct {
	agentpbconnect.UnimplementedUserAgentHandler
	state string
	mu    sync.Mutex
	calls int
}

func (f *readOnlyUIFixture) UIStatus(ctx context.Context, req *connect.Request[pb.UIRequest]) (*connect.Response[pb.UIResponse], error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return connect.NewResponse(&pb.UIResponse{Status: &pb.UIStatus{State: f.state}}), nil
}

func (f *readOnlyUIFixture) InspectUI(ctx context.Context, req *connect.Request[pb.UIRequest]) (*connect.Response[pb.UIResponse], error) {
	return f.UIStatus(ctx, req)
}
func (f *readOnlyUIFixture) FindUI(ctx context.Context, req *connect.Request[pb.UIRequest]) (*connect.Response[pb.UIResponse], error) {
	return f.UIStatus(ctx, req)
}

func TestAgentUIReadOnlyTransport(t *testing.T) {
	for i, state := range []string{"permission_denied", "locked", "stale_generation"} {
		command := []string{"agent-ui-status", "agent-ui-inspect", "agent-ui-find"}[i]
		method := []string{"/UIStatus", "/InspectUI", "/FindUI"}[i]
		t.Run(state, func(t *testing.T) {
			fixture := &readOnlyUIFixture{state: state}
			path, handler := agentpbconnect.NewUserAgentHandler(fixture)
			mux := http.NewServeMux()
			mux.Handle(path, handler)
			var mu sync.Mutex
			var methods []string
			httpServer := httptest.NewServer(h2c.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				methods = append(methods, r.URL.Path)
				mu.Unlock()
				mux.ServeHTTP(w, r)
			}), &http2.Server{}))
			defer httpServer.Close()
			dialCount := 0
			response := (&ControlServer{}).handleAgentUIWithDial(&controlpb.ControlRequest{Type: command}, func(ctx context.Context, port uint32) (net.Conn, error) {
				mu.Lock()
				dialCount++
				mu.Unlock()
				if port != 1025 {
					return nil, fmt.Errorf("dialed port %d", port)
				}
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 12*time.Second {
					return nil, fmt.Errorf("unbounded dial")
				}
				return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(httpServer.URL, "http://"))
			})
			mu.Lock()
			defer mu.Unlock()
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if response.GetAgentUi().GetStatus().GetState() != state || fixture.calls != 1 || dialCount != 1 || len(methods) != 1 || !strings.HasSuffix(methods[0], method) {
				t.Fatalf("response=%v calls=%d dials=%d methods=%v", response, fixture.calls, dialCount, methods)
			}
		})
	}
}

func TestAgentUIBlockedDialCancellation(t *testing.T) {
	server := &ControlServer{}
	server.startLifecycleContext()
	defer server.shutdownLifecycleContext()
	started, exited := make(chan struct{}), make(chan struct{})
	done := make(chan *controlpb.ControlResponse, 1)
	go func() {
		done <- server.handleAgentUIWithDial(&controlpb.ControlRequest{Type: "agent-ui-inspect"}, func(ctx context.Context, port uint32) (net.Conn, error) {
			if port != 1025 {
				t.Errorf("port %d", port)
			}
			close(started)
			defer close(exited)
			<-ctx.Done()
			return nil, ctx.Err()
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("dial not started")
	}
	server.shutdownLifecycleContext()
	select {
	case response := <-done:
		if response.GetAgentUi().GetStatus().GetState() != "unavailable" {
			t.Fatalf("response %v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("UI request retained after cancellation")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("dial worker retained")
	}
}
