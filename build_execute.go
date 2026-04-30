package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/store"
)

var errBuildCacheMissExecutionNotImplemented = errors.New("cove build: cache miss execution not yet implemented")

type buildExecutor struct {
	plan         buildPlan
	opts         buildOptions
	store        store.Store
	scratchRoot  string
	startGuest   buildGuestStarter
	compactGuest buildCompactor
	mountSecrets buildSecretMounter
	now          func() time.Time
	pid          int
	result       buildExecutionResult
}

type buildExecutionResult struct {
	VMDir    string
	DiskPath string
	Steps    []buildApplyResult
}

type buildMissRunner func(context.Context, buildPlanStep, buildScratch) error
type buildCompactor func(context.Context, buildScratch, string) error
type buildSecretMounter func(context.Context, buildPlanStep, buildScratch, string) (buildGuestCleanup, error)

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
	if e.scratchRoot == "" {
		e.scratchRoot = defaultBuildScratchRoot()
	}
	if err := gcBuildScratch(e.scratchRoot, nil); err != nil {
		return err
	}
	if e.plan.Base == "" {
		return errors.New("cove build: base vm dir required")
	}
	info, err := os.Stat(e.plan.Base)
	if err != nil {
		return fmt.Errorf("cove build: base vm dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cove build: base vm dir %s is not a directory", e.plan.Base)
	}
	e.result = buildExecutionResult{}
	result, err := e.executeVMBuild(ctx, e.plan.Base)
	if err != nil {
		return err
	}
	if err := finalizeBuildResult(result); err != nil {
		return err
	}
	e.result = result
	return nil
}

func (e *buildExecutor) Result() buildExecutionResult {
	return e.result
}

func (e *buildExecutor) executeCacheHits(ctx context.Context, parentDisk string) (buildExecutionResult, error) {
	return e.executeWithMissRunner(ctx, parentDisk, nil)
}

func (e *buildExecutor) executeWithMissRunner(ctx context.Context, parentDisk string, runMiss buildMissRunner) (buildExecutionResult, error) {
	var result buildExecutionResult
	if ctx == nil {
		ctx = context.Background()
	}
	if parentDisk == "" {
		return result, errors.New("cove build: parent disk path required")
	}
	currentDisk := parentDisk
	for _, step := range e.plan.Steps {
		applied, err := e.executeStep(ctx, step, currentDisk, runMiss)
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

func (e *buildExecutor) executeVMWithMissRunner(ctx context.Context, parentDir string, runMiss buildMissRunner) (buildExecutionResult, error) {
	var result buildExecutionResult
	if ctx == nil {
		ctx = context.Background()
	}
	if parentDir == "" {
		return result, errors.New("cove build: parent vm dir required")
	}
	currentDir := parentDir
	currentDisk, err := pushDiskPath(currentDir)
	if err != nil {
		return result, err
	}
	for _, step := range e.plan.Steps {
		applied, err := e.executeVMStep(ctx, step, currentDir, runMiss)
		if err != nil {
			e.cleanupIntermediate(result)
			return result, err
		}
		if len(result.Steps) > 0 && !e.opts.KeepIntermediate {
			_ = e.cleanupScratch(result.Steps[len(result.Steps)-1].Scratch)
		}
		result.Steps = append(result.Steps, applied)
		result.VMDir = applied.Scratch.Dir
		result.DiskPath = applied.DiskPath
		currentDir = applied.Scratch.Dir
		currentDisk = applied.DiskPath
	}
	result.DiskPath = currentDisk
	return result, nil
}

func (e *buildExecutor) executeStep(ctx context.Context, step buildPlanStep, parentDisk string, runMiss buildMissRunner) (buildApplyResult, error) {
	if step.CacheHit {
		return e.applyCacheHit(ctx, step, parentDisk)
	}
	if runMiss == nil {
		return buildApplyResult{}, errBuildCacheMissExecutionNotImplemented
	}
	if err := validateBuildStepSecrets(step); err != nil {
		return buildApplyResult{}, err
	}
	sc, err := e.createScratch(parentDisk)
	if err != nil {
		return buildApplyResult{}, err
	}
	if err := runMiss(ctx, step, sc); err != nil {
		if e.opts.KeepIntermediate {
			return buildApplyResult{Step: step.Name, Key: step.Key, Scratch: sc, DiskPath: sc.DiskPath}, buildStepFailureError(step, sc, err, true)
		}
		err = buildStepFailureError(step, sc, err, false)
		if cleanupErr := e.cleanupScratch(sc); cleanupErr != nil {
			return buildApplyResult{}, errors.Join(err, cleanupErr)
		}
		return buildApplyResult{}, err
	}
	applied, err := e.recordCacheMissLayer(ctx, step, parentDisk, sc.DiskPath)
	if err != nil {
		if e.opts.KeepIntermediate {
			applied.Scratch = sc
			return applied, err
		}
		if cleanupErr := e.cleanupScratch(sc); cleanupErr != nil {
			return buildApplyResult{}, errors.Join(err, cleanupErr)
		}
		return buildApplyResult{}, err
	}
	applied.Scratch = sc
	return applied, nil
}

func (e *buildExecutor) executeVMStep(ctx context.Context, step buildPlanStep, parentDir string, runMiss buildMissRunner) (buildApplyResult, error) {
	if step.CacheHit {
		return e.applyCacheHitVM(ctx, step, parentDir)
	}
	if runMiss == nil {
		return buildApplyResult{}, errBuildCacheMissExecutionNotImplemented
	}
	if err := validateBuildStepSecrets(step); err != nil {
		return buildApplyResult{}, err
	}
	parentDisk, err := pushDiskPath(parentDir)
	if err != nil {
		return buildApplyResult{}, err
	}
	sc, err := e.createScratchVM(parentDir)
	if err != nil {
		return buildApplyResult{}, err
	}
	if err := runMiss(ctx, step, sc); err != nil {
		if e.opts.KeepIntermediate {
			return buildApplyResult{Step: step.Name, Key: step.Key, Scratch: sc, DiskPath: sc.DiskPath}, buildStepFailureError(step, sc, err, true)
		}
		err = buildStepFailureError(step, sc, err, false)
		if cleanupErr := e.cleanupScratch(sc); cleanupErr != nil {
			return buildApplyResult{}, errors.Join(err, cleanupErr)
		}
		return buildApplyResult{}, err
	}
	applied, err := e.recordCacheMissLayer(ctx, step, parentDisk, sc.DiskPath)
	if err != nil {
		if e.opts.KeepIntermediate {
			applied.Scratch = sc
			return applied, err
		}
		if cleanupErr := e.cleanupScratch(sc); cleanupErr != nil {
			return buildApplyResult{}, errors.Join(err, cleanupErr)
		}
		return buildApplyResult{}, err
	}
	applied.Scratch = sc
	return applied, nil
}

func (e *buildExecutor) cleanupIntermediate(result buildExecutionResult) {
	if e.opts.KeepIntermediate {
		return
	}
	for _, step := range result.Steps {
		_ = e.cleanupScratch(step.Scratch)
	}
}

func buildStepFailureError(step buildPlanStep, sc buildScratch, err error, kept bool) error {
	if kept && sc.Dir != "" {
		return fmt.Errorf("cove build: step %q failed; scratch kept at %s: %w", step.Name, sc.Dir, err)
	}
	return fmt.Errorf("cove build: step %q failed: %w", step.Name, err)
}

func validateBuildStepSecrets(step buildPlanStep) error {
	var missing []string
	for _, name := range step.Meta.Secrets {
		if !validBuildSecretName(name) {
			return fmt.Errorf("cove build: step %q invalid secret name %q", step.Name, name)
		}
		if _, ok := os.LookupEnv(name); !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("cove build: step %q missing secret environment variable(s): %s", step.Name, strings.Join(missing, ", "))
}

func validBuildSecretName(name string) bool {
	return name != "" && !strings.Contains(name, "/")
}

func finalizeBuildResult(result buildExecutionResult) error {
	if result.VMDir == "" {
		return nil
	}
	pidPath := filepath.Join(result.VMDir, "build.pid")
	if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("finalize build result: %w", err)
	}
	return nil
}
