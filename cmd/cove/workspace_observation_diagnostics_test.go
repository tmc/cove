package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentstate "github.com/tmc/cove/internal/agent"
	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
	controlpb "github.com/tmc/cove/proto/controlpb"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type observationRPCFixture struct {
	agentpbconnect.UnimplementedUserAgentHandler
}

func (observationRPCFixture) UserExec(context.Context, *connect.Request[pb.ExecRequest]) (*connect.Response[pb.ExecResponse], error) {
	return connect.NewResponse(&pb.ExecResponse{}), nil
}

func TestWorkspaceObservationHTTPPhases(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "stalled"}[stalled], func(t *testing.T) {
			path, handler := agentpbconnect.NewUserAgentHandler(observationRPCFixture{})
			stop := make(chan struct{})
			mux := http.NewServeMux()
			if stalled {
				mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
					select {
					case <-stop:
					case <-r.Context().Done():
					}
				})
			} else {
				mux.Handle(path, handler)
			}
			server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
			defer server.Close()
			defer close(stop)
			req := &controlpb.ControlRequest{Command: &controlpb.ControlRequest_AgentExec{AgentExec: &controlpb.AgentExecCommand{Args: []string{"/usr/bin/true"}, DeadlineUnixNano: time.Now().Add(500 * time.Millisecond).UnixNano()}}}
			resp := (&ControlServer{}).handleWorkspaceUserObserveWithDial(req, func(ctx context.Context, _ uint32) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(server.URL, "http://"))
			})
			if stalled {
				if resp.Success || !strings.Contains(resp.Error, "http-request-written") || !strings.Contains(resp.Error, "outcome unverified") {
					t.Fatalf("stalled %v", resp)
				}
			} else if !resp.Success {
				t.Fatalf("normal %v", resp)
			}
		})
	}
}

func TestAgentDialObservationRetainsSlotAndClosesLateConnection(t *testing.T) {
	var gate agentDialGate
	var mu sync.Mutex
	phases := []string{}
	lateClosed := make(chan struct{}, 1)
	observer := func(phase string) {
		mu.Lock()
		phases = append(phases, phase)
		mu.Unlock()
		if phase == "native-late-connection-closed" {
			lateClosed <- struct{}{}
		}
	}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), agentDialObserverKey{}, observer))
	release, started := make(chan struct{}), make(chan struct{})
	host, peer := net.Pipe()
	defer peer.Close()
	defer host.Close()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := gate.dial(ctx, func() (net.Conn, error) { close(started); <-release; return host, nil })
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("native worker not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller not canceled")
	}
	second, stop := context.WithTimeout(context.WithValue(context.Background(), agentDialObserverKey{}, observer), 100*time.Millisecond)
	defer stop()
	if _, err := gate.dial(second, func() (net.Conn, error) { t.Error("held slot started another operation"); return nil, nil }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held slot %v", err)
	}
	unblock()
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("late connection open: %v", err)
	}
	select {
	case <-lateClosed:
	case <-time.After(time.Second):
		t.Fatal("late close phase missing")
	}
	mu.Lock()
	defer mu.Unlock()
	counts := map[string]int{}
	for _, phase := range phases {
		counts[phase]++
	}
	if counts["native-slot-wait"] != 2 || counts["native-slot-admitted"] != 1 || counts["native-connect-start"] != 1 || counts["native-connect-return"] != 1 {
		t.Fatalf("native phases %v", counts)
	}

}

func TestWorkspaceObservationHTTPTraceResponse(t *testing.T) {
	path, handler := agentpbconnect.NewUserAgentHandler(observationRPCFixture{})
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer server.Close()
	client, err := agentstate.NewUserAgentClientWithDial(func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(server.URL, "http://"))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	trace := &workspaceObservationTrace{id: 1, started: time.Now(), phase: "created"}
	if _, err := client.UserExec(trace.context(ctx), []string{"/usr/bin/true"}, nil, ""); err != nil {
		t.Fatal(err)
	}
	if got := trace.current(); got != "http-response-started" {
		t.Fatalf("response phase %s", got)
	}
}
