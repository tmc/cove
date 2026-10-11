package controlclient

import (
	"context"
	"errors"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestTypedExecBudget(t *testing.T) {
	tests := []struct {
		name, route string
		timeout     time.Duration
		run         func(*Client, time.Duration) (*controlpb.AgentExecResponse, error)
	}{
		{"auto", "agent-exec-auto", time.Second, func(c *Client, d time.Duration) (*controlpb.AgentExecResponse, error) {
			return c.AgentExecTypedTimeout([]string{"true"}, nil, "", d)
		}},
		{"root", "agent-exec", time.Second, func(c *Client, d time.Duration) (*controlpb.AgentExecResponse, error) {
			return c.AgentDaemonExecTypedTimeout([]string{"true"}, nil, "", d)
		}},
		{"user", "agent-user-exec", time.Second, func(c *Client, d time.Duration) (*controlpb.AgentExecResponse, error) {
			return c.AgentUserExecTypedTimeout([]string{"true"}, nil, "", d)
		}},
		{"default", "agent-exec-auto", 0, func(c *Client, d time.Duration) (*controlpb.AgentExecResponse, error) {
			return c.AgentExecTyped([]string{"true"}, nil, "")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				requests <- req
				return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{}}}
			})
			start := time.Now()
			if _, err := tt.run(New(sock), tt.timeout); err != nil {
				t.Fatal(err)
			}
			end := time.Now()
			req := <-requests
			if req.Type != tt.route {
				t.Fatalf("route = %q, want %q", req.Type, tt.route)
			}
			budget := tt.timeout
			if budget <= 0 {
				budget = 10 * time.Minute
			}
			deadline := time.Unix(0, req.GetAgentExec().GetDeadlineUnixNano())
			if deadline.Before(start.Add(budget)) || deadline.After(end.Add(budget)) {
				t.Fatalf("deadline = %v, want between %v and %v", deadline, start.Add(budget), end.Add(budget))
			}
		})
	}
}

func TestTypedExecEarlierContextBudget(t *testing.T) {
	requests := make(chan *controlpb.ControlRequest, 1)
	sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
		requests <- req
		return &controlpb.ControlResponse{Success: true, Data: "{}"}
	})
	deadline := time.Now().Add(time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if _, err := New(sock).agentExecTypedContext(ctx, "agent-user-exec", []string{"true"}, nil, "", time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := (<-requests).GetAgentExec().GetDeadlineUnixNano(); got != deadline.UnixNano() {
		t.Fatalf("deadline = %d, want %d", got, deadline.UnixNano())
	}
}

func TestTypedExecCanceledBudgetDoesNotDispatch(t *testing.T) {
	tests := []struct {
		name    string
		expired bool
		want    error
	}{
		{"canceled", false, context.Canceled},
		{"expired", true, context.DeadlineExceeded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				requests <- req
				return &controlpb.ControlResponse{Success: true, Data: "{}"}
			})
			ctx, cancel := context.WithCancel(context.Background())
			if tt.expired {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			cancel()
			_, err := New(sock).agentExecTypedContext(ctx, "agent-exec", []string{"true"}, nil, "", time.Second)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			select {
			case req := <-requests:
				t.Fatalf("unexpected request: %v", req)
			default:
			}
		})
	}
}

func TestTypedExecTimeoutCarriesBudget(t *testing.T) {
	requests := make(chan *controlpb.ControlRequest, 1)
	completed := make(chan bool, 1)
	sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
		requests <- req
		deadline := time.Unix(0, req.GetAgentExec().GetDeadlineUnixNano())
		time.Sleep(time.Until(deadline) + 10*time.Millisecond)
		completed <- !time.Now().Before(deadline)
		return &controlpb.ControlResponse{Success: false, Error: "execution deadline exceeded before dispatch"}
	})
	start := time.Now()
	_, err := New(sock).AgentDaemonExecTypedTimeout([]string{"true"}, nil, "", 50*time.Millisecond)
	if err == nil {
		t.Fatal("timeout succeeded")
	}
	var req *controlpb.ControlRequest
	select {
	case req = <-requests:
	case <-time.After(time.Second):
		t.Fatal("request not received within fixture budget")
	}
	deadline := time.Unix(0, req.GetAgentExec().GetDeadlineUnixNano())
	if deadline.Before(start.Add(50*time.Millisecond)) || deadline.After(time.Now().Add(50*time.Millisecond)) {
		t.Fatalf("unexpected deadline: %v", deadline)
	}
	select {
	case expired := <-completed:
		if !expired {
			t.Fatal("fixture observed request before expiry")
		}
	case <-time.After(time.Second):
		t.Fatal("fixture did not finish within budget")
	}
}
