package controlserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	agentstate "github.com/tmc/cove/internal/agent"
	pb "github.com/tmc/cove/proto/agentpb"
	"github.com/tmc/cove/proto/agentpbconnect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func ExampleAgentBridge_WithCachedUserAgent() {
	var bridge AgentBridge
	used, err := bridge.WithCachedUserAgent(context.Background(), func(*agentstate.UserAgentClient) error { return nil })
	fmt.Println(used, err)
	// Output: false <nil>
}

type leaseFixture struct {
	agentpbconnect.UnimplementedUserAgentHandler
	execs atomic.Int32
	reads atomic.Int32
}

func (f *leaseFixture) UserExec(context.Context, *connect.Request[pb.ExecRequest]) (*connect.Response[pb.ExecResponse], error) {
	f.execs.Add(1)
	return connect.NewResponse(&pb.ExecResponse{}), nil
}
func (f *leaseFixture) UIStatus(_ context.Context, req *connect.Request[pb.UIRequest]) (*connect.Response[pb.UIResponse], error) {
	f.reads.Add(1)
	if req.Msg.HostGeneration != "epoch" {
		return nil, fmt.Errorf("epoch changed: %q", req.Msg.HostGeneration)
	}
	return connect.NewResponse(&pb.UIResponse{Status: &pb.UIStatus{State: "locked"}}), nil
}

func TestCachedUserLeaseReusesTransport(t *testing.T) {
	fixture := &leaseFixture{}
	path, handler := agentpbconnect.NewUserAgentHandler(fixture)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	srv := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	defer srv.Close()
	var dials atomic.Int32
	client, err := agentstate.NewUserAgentClientWithDial(func(ctx context.Context) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", strings.TrimPrefix(srv.URL, "http://"))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.UserExec(ctx, []string{"true"}, nil, ""); err != nil {
		t.Fatal(err)
	}
	b := AgentBridge{userAgent: client}
	used, err := b.WithCachedUserAgent(ctx, func(c *agentstate.UserAgentClient) error {
		result, err := c.UIStatus(ctx, &pb.UIRequest{HostGeneration: "epoch"})
		if err != nil {
			return err
		}
		if result.GetStatus().GetState() != "locked" {
			return fmt.Errorf("status changed: %v", result)
		}
		return nil
	})
	if err != nil || !used || dials.Load() != 1 || fixture.execs.Load() != 1 || fixture.reads.Load() != 1 {
		t.Fatalf("used=%v error=%v dials=%d", used, err, dials.Load())
	}
}

func TestCachedUserLeasePinsIdentity(t *testing.T) {
	client, err := agentstate.NewUserAgentClientWithDial(func(context.Context) (net.Conn, error) { return nil, errors.New("unexpected dial") })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	b := AgentBridge{userAgent: client}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := b.WithCachedUserAgent(context.Background(), func(c *agentstate.UserAgentClient) error {
			close(entered)
			<-release
			if c != client {
				return errors.New("identity changed")
			}
			return nil
		})
		done <- err
	}()
	<-entered
	if b.mu.TryLock() {
		b.mu.Unlock()
		close(release)
		t.Fatal("replacement acquired lock during lease")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !b.mu.TryLock() {
		t.Fatal("lease retained lock")
	}
	b.userAgent = nil
	b.mu.Unlock()
}

func TestCachedUserLeaseCanceledDoesNotDispatch(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprint(held), func(t *testing.T) {
			b := AgentBridge{}
			if held {
				b.mu.Lock()
				defer b.mu.Unlock()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if !held {
				cancel()
			}
			var calls int
			used, err := b.WithCachedUserAgent(ctx, func(*agentstate.UserAgentClient) error { calls++; return nil })
			if err == nil || used || calls != 0 {
				t.Fatalf("used=%v error=%v calls=%d", used, err, calls)
			}
		})
	}
}

func TestCachedUserLeaseErrorKeepsClient(t *testing.T) {
	var dials atomic.Int32
	client, err := agentstate.NewUserAgentClientWithDial(func(context.Context) (net.Conn, error) { dials.Add(1); return nil, errors.New("unexpected dial") })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	b := AgentBridge{userAgent: client}
	failure := errors.New("accessibility unavailable")
	used, err := b.WithCachedUserAgent(context.Background(), func(c *agentstate.UserAgentClient) error {
		if c != client {
			return errors.New("identity changed")
		}
		return failure
	})
	if !used || !errors.Is(err, failure) || b.userAgent != client || dials.Load() != 0 {
		t.Fatalf("used=%v error=%v same=%v dials=%d", used, err, b.userAgent == client, dials.Load())
	}
}
