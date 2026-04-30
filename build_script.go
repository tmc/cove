package main

import (
	"context"
	"fmt"
	"time"
)

func (e *buildExecutor) executeVMBuild(ctx context.Context, parentDir string) (buildExecutionResult, error) {
	return e.executeVMWithMissRunner(ctx, parentDir, e.runBuildStepInScratch)
}

func (e *buildExecutor) runBuildStepInScratch(ctx context.Context, step buildPlanStep, sc buildScratch) error {
	if sc.Dir == "" {
		return fmt.Errorf("run build step %q: scratch vm dir required", step.Name)
	}
	return e.runBuildStepScript(ctx, step, GetControlSocketPathForVM(sc.Dir))
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
	}
	if err := runVZScriptContext(ctx, step.Data, name, cfg); err != nil {
		return fmt.Errorf("run build step %q: %w", step.Name, err)
	}
	return nil
}
