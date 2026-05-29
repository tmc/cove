package main

import (
	"context"
	"fmt"
)

type buildResultPusher func(context.Context, string, string, pushOptions) error

var defaultBuildResultPusher buildResultPusher = pushBuildResultTag

func pushBuildResult(ctx context.Context, result buildExecutionResult, opts buildOptions) error {
	if !opts.Push {
		return nil
	}
	if result.VMDir == "" {
		return fmt.Errorf("cove build: push: final vm dir required")
	}
	pusher := defaultBuildResultPusher
	if pusher == nil {
		return fmt.Errorf("cove build: push: pusher unavailable")
	}
	pushOpts := buildPushOptions(opts)
	for _, tag := range opts.Tags {
		if err := pusher(ctx, result.VMDir, tag, pushOpts); err != nil {
			return fmt.Errorf("cove build: push %s: %w", tag, err)
		}
	}
	return nil
}

func pushBuildResultTag(ctx context.Context, vmDir, ref string, opts pushOptions) error {
	plan, err := buildPushPlan(vmDir, ref, opts)
	if err != nil {
		return err
	}
	return pushImage(ctx, plan, opts)
}

func buildPushOptions(opts buildOptions) pushOptions {
	chunkSize := int64(opts.ChunkSizeMB) << 20
	return pushOptions{ChunkSize: chunkSize}
}
