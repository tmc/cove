package main

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"time"

	agentpb "github.com/tmc/cove/proto/agentpb"
)

var agentExecSequence atomic.Uint64

func acquireAgentExec(ctx context.Context, acquire func(context.Context) (func(context.Context) (*agentpb.ExecResponse, error), error)) (*agentpb.ExecResponse, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	exec, err := acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	result, err := exec(ctx)
	return result, true, err
}

func agentExecBudget(parent context.Context, deadline int64) (context.Context, context.CancelFunc) {
	limit := time.Now().Add(10 * time.Minute)
	if deadline != 0 && time.Unix(0, deadline).Before(limit) {
		limit = time.Unix(0, deadline)
	}
	return context.WithDeadline(parent, limit)
}

func logAgentExecPhase(id uint64, phase string, started time.Time, err error) {
	result := "ok"
	if errors.Is(err, context.DeadlineExceeded) {
		result = "deadline-exceeded"
	} else if errors.Is(err, context.Canceled) {
		result = "canceled"
	} else if err != nil {
		result = "rpc-error"
	}
	slog.Info("agent-user-exec", "request", id, "phase", phase, "elapsed_ms", time.Since(started).Milliseconds(), "result", result)
}
