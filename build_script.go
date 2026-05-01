package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (e *buildExecutor) executeVMBuild(ctx context.Context, parentDir string) (buildExecutionResult, error) {
	return e.executeVMWithMissRunner(ctx, parentDir, e.runBuildStepInScratch)
}

func (e *buildExecutor) runBuildStepInScratch(ctx context.Context, step buildPlanStep, sc buildScratch) (err error) {
	if sc.Dir == "" {
		return fmt.Errorf("run build step %q: scratch vm dir required", step.Name)
	}
	cleanup, err := e.startBuildGuest(ctx, sc)
	if err != nil {
		return fmt.Errorf("run build step %q: %w", step.Name, err)
	}
	defer func() {
		if cleanup != nil {
			err = errors.Join(err, cleanup(ctx))
		}
	}()
	socketPath := GetControlSocketPathForVM(sc.Dir)
	if err := waitBuildAgent(ctx, socketPath, 2*time.Minute); err != nil {
		return fmt.Errorf("run build step %q: %w", step.Name, err)
	}
	secretCleanup, secretErr := e.mountBuildStepSecrets(ctx, step, sc, socketPath)
	if secretErr != nil {
		return fmt.Errorf("run build step %q: %w", step.Name, secretErr)
	}
	err = e.runBuildStepScript(ctx, step, socketPath)
	if secretCleanup != nil {
		err = errors.Join(err, secretCleanup(ctx))
	}
	if err == nil {
		err = e.compactBuildGuest(ctx, step, sc)
	}
	if shutdownErr := shutdownBuildGuest(ctx, socketPath); shutdownErr != nil {
		err = errors.Join(err, shutdownErr)
	}
	return err
}

func (e *buildExecutor) compactBuildGuest(ctx context.Context, step buildPlanStep, sc buildScratch) error {
	mode := step.Meta.Compact
	if mode == "" {
		mode = "targeted"
	}
	if err := validateCompactMode(mode); err != nil {
		return fmt.Errorf("run build step %q: %w", step.Name, err)
	}
	if mode == "fast" {
		return nil
	}
	compact := e.compactGuest
	if compact == nil {
		compact = defaultBuildCompact
	}
	if compact == nil {
		return nil
	}
	if err := compact(ctx, sc, mode); err != nil {
		return fmt.Errorf("run build step %q: compact %s: %w", step.Name, mode, err)
	}
	return nil
}

func (e *buildExecutor) mountBuildStepSecrets(ctx context.Context, step buildPlanStep, sc buildScratch, socketPath string) (buildGuestCleanup, error) {
	if len(step.Meta.Secrets) == 0 {
		return nil, nil
	}
	mount := e.mountSecrets
	if mount == nil {
		mount = defaultBuildSecretMount
	}
	if mount == nil {
		return nil, fmt.Errorf("secret mount unavailable")
	}
	cleanup, err := mount(ctx, step, sc, socketPath)
	if err != nil {
		return cleanup, fmt.Errorf("mount secrets: %w", err)
	}
	return cleanup, nil
}

func (e *buildExecutor) runBuildStepScript(ctx context.Context, step buildPlanStep, socketPath string) error {
	if len(step.Data) == 0 {
		return fmt.Errorf("run build step %q: empty script data", step.Name)
	}
	name := step.Source
	if name == "" {
		name = step.Name
	}
	cfg := vzscriptConfig{
		socketPath:  socketPath,
		execTimeout: 10 * time.Minute,
		// Build runs against a headless scratch VM with no logged-in user,
		// so the user agent (port 1025) cannot bootstrap. Route guest
		// commands through the daemon agent (root, port 1024) by default.
		daemon: true,
	}
	if err := runVZScriptContext(ctx, step.Data, name, cfg); err != nil {
		return fmt.Errorf("run build step %q: %w", step.Name, err)
	}
	return nil
}
