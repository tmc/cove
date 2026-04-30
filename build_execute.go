package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/tmc/vz-macos/internal/store"
)

var errBuildExecutionNotImplemented = errors.New("cove build: execution path not yet implemented")

type buildExecutor struct {
	plan        buildPlan
	opts        buildOptions
	store       store.Store
	scratchRoot string
	now         func() time.Time
	pid         int
}

func newBuildExecutor(plan buildPlan, opts buildOptions, s store.Store) *buildExecutor {
	return &buildExecutor{
		plan:        plan,
		opts:        opts,
		store:       s,
		scratchRoot: defaultBuildScratchRoot(),
		now:         time.Now,
		pid:         os.Getpid(),
	}
}

func (e *buildExecutor) Execute(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return errBuildExecutionNotImplemented
}
