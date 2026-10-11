package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"

	agentstate "github.com/tmc/cove/internal/agent"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

var workspaceObservationSequence atomic.Uint64

type workspaceObservationTrace struct {
	mu      sync.Mutex
	id      uint64
	started time.Time
	rank    int
	phase   string
}

func (t *workspaceObservationTrace) event(phase string, rank int) {
	t.mu.Lock()
	if rank > t.rank {
		t.rank, t.phase = rank, phase
	}
	t.mu.Unlock()
	slog.Info("workspace-user-observation", "request", t.id, "port", agentstate.UserPort, "phase", phase, "elapsed_ms", time.Since(t.started).Milliseconds())
}

func (t *workspaceObservationTrace) current() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.phase
}

func (t *workspaceObservationTrace) context(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, agentDialObserverKey{}, func(phase string) {
		rank := map[string]int{"native-slot-wait": 2, "native-slot-admitted": 3, "native-connect-start": 4, "native-connect-return": 5}[phase]
		t.event(phase, rank)
	})
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			phase := "http-connection-new"
			if info.Reused {
				phase = "http-connection-reused"
			}
			t.event(phase, 6)
		},
		WroteHeaders: func() { t.event("http-headers-written", 7) },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err != nil {
				t.event("http-request-write-failed", 7)
			} else {
				t.event("http-request-written", 8)
			}
		},
		GotFirstResponseByte: func() { t.event("http-response-started", 9) },
	})
}

// Observe bypasses acquisition repair; the supplied guest command still executes.
func (s *ControlServer) handleWorkspaceUserObserve(req *controlpb.ControlRequest) *controlpb.ControlResponse {
	return s.handleWorkspaceUserObserveWithDial(req, s.DialAgent)
}

func (s *ControlServer) handleWorkspaceUserObserveWithDial(req *controlpb.ControlRequest, dial func(context.Context, uint32) (net.Conn, error)) *controlpb.ControlResponse {
	cmd := req.GetAgentExec()
	if cmd == nil || len(cmd.Args) == 0 {
		return &controlpb.ControlResponse{Error: "missing observation command"}
	}
	ctx, cancel := agentExecBudget(s.lifecycleContext(), cmd.DeadlineUnixNano)
	defer cancel()
	trace := &workspaceObservationTrace{id: workspaceObservationSequence.Add(1), started: time.Now(), phase: "created"}
	ctx = trace.context(ctx)
	if err := ctx.Err(); err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	user, err := agentstate.NewUserAgentClientWithDial(func(ctx context.Context) (net.Conn, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		trace.event("dial-begin", 1)
		conn, err := dial(ctx, agentstate.UserPort)
		if err != nil {
			trace.event("dial-failed", 0)
		} else {
			trace.event("dial-returned", 5)
		}
		return conn, err
	})
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	defer user.Close()
	result, err := user.UserExec(ctx, cmd.Args, cmd.Env, cmd.WorkingDir)
	if err != nil {
		trace.event("request-failed", 0)
		return &controlpb.ControlResponse{Error: fmt.Sprintf("user observation failed after %s; guest command outcome unverified: %v", trace.current(), err)}
	}
	trace.event("rpc-completed", 10)
	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: result.ExitCode, Stdout: string(result.Stdout), Stderr: string(result.Stderr), DurationSeconds: result.DurationSeconds}}}
}
