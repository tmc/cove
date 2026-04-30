package main

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/tmc/vz-macos/internal/store"
)

var (
	errBuildExecutionNotImplemented          = errors.New("cove build: execution path not yet implemented")
	errBuildCacheMissExecutionNotImplemented = errors.New("cove build: cache miss execution not yet implemented")
)

type buildExecutor struct {
	plan        buildPlan
	opts        buildOptions
	store       store.Store
	scratchRoot string
	now         func() time.Time
	pid         int
}

type buildExecutionResult struct {
	DiskPath string
	Steps    []buildApplyResult
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

func (e *buildExecutor) executeCacheHits(ctx context.Context, parentDisk string) (buildExecutionResult, error) {
	var result buildExecutionResult
	if ctx == nil {
		ctx = context.Background()
	}
	if parentDisk == "" {
		return result, errors.New("cove build: parent disk path required")
	}
	currentDisk := parentDisk
	for _, step := range e.plan.Steps {
		if !step.CacheHit {
			e.cleanupIntermediate(result)
			return result, errBuildCacheMissExecutionNotImplemented
		}
		applied, err := e.applyCacheHit(ctx, step, currentDisk)
		if err != nil {
			e.cleanupIntermediate(result)
			return result, err
		}
		if len(result.Steps) > 0 && !e.opts.KeepIntermediate {
			_ = e.cleanupScratch(result.Steps[len(result.Steps)-1].Scratch)
		}
		result.Steps = append(result.Steps, applied)
		result.DiskPath = applied.DiskPath
		currentDisk = applied.DiskPath
	}
	return result, nil
}

func (e *buildExecutor) cleanupIntermediate(result buildExecutionResult) {
	if e.opts.KeepIntermediate {
		return
	}
	for _, step := range result.Steps {
		_ = e.cleanupScratch(step.Scratch)
	}
}
