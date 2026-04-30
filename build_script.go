package main

import (
	"context"
	"fmt"
	"time"
)

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
