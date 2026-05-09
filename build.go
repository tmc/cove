package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/store"
)

type buildOptions struct {
	Base             string
	Scripts          []string
	Tags             []string
	Push             bool
	DryRun           bool
	NoCache          bool
	CacheFrom        []string
	CacheTo          []string
	KeepIntermediate bool
	ChunkSizeMB      int
	Compact          string
	StoreDir         string
}

func handleBuild(args []string) (err error) {
	var scripts, tags, cacheFrom, cacheTo stringList
	var opts buildOptions
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.Base, "base", "", "base OCI image reference")
	fs.Var(&scripts, "script", "vzscript recipe or path (repeatable)")
	fs.Var(&tags, "tag", "output OCI image tag (repeatable)")
	fs.BoolVar(&opts.Push, "push", false, "push output tags after build")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "print plan and cache keys without running VMs")
	fs.BoolVar(&opts.NoCache, "no-cache", false, "run every step even when a cache entry exists")
	fs.Var(&cacheFrom, "cache-from", "reserved for registry cache import (repeatable)")
	fs.Var(&cacheTo, "cache-to", "reserved for registry cache export (repeatable)")
	fs.BoolVar(&opts.KeepIntermediate, "keep-intermediate", false, "leave scratch VMs behind for debugging")
	fs.IntVar(&opts.ChunkSizeMB, "chunk-size", 512, "chunk size in MiB")
	fs.StringVar(&opts.Compact, "compact", "targeted", "compaction mode: fast, targeted, thorough")
	fs.StringVar(&opts.StoreDir, "store-dir", "", "content store directory")
	fs.Usage = func() { printBuildUsage(os.Stderr) }
	flagArgs, posArgs, err := splitBuildArgs(args)
	if err != nil {
		return err
	}
	if err := parseFlagsOrHelp(fs, flagArgs); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if len(posArgs) != 1 {
		return fmt.Errorf("usage: cove build <name> --base <ref> --script <step> [--tag <ref>]")
	}
	if err := validateCompactMode(opts.Compact); err != nil {
		return err
	}
	if opts.ChunkSizeMB <= 0 {
		return fmt.Errorf("cove build: --chunk-size must be positive")
	}
	opts.Scripts = scripts
	opts.Tags = tags
	opts.CacheFrom = cacheFrom
	opts.CacheTo = cacheTo
	if err := validateBuildRegistryCache(opts); err != nil {
		return err
	}
	if len(opts.Scripts) == 0 {
		return fmt.Errorf("cove build: at least one --script is required")
	}
	if opts.Base == "" {
		return fmt.Errorf("cove build: --base is required")
	}
	if opts.Push && len(opts.Tags) == 0 {
		return fmt.Errorf("cove build: --push requires at least one --tag")
	}
	if !opts.DryRun {
		if _, ok := localBuildBaseDir(opts.Base); !ok {
			return fmt.Errorf("cove build: non-dry-run requires local VM base directory")
		}
	}
	ctx := context.Background()
	blobStore := store.New(opts.StoreDir)
	plan, err := buildDryPlanWithStore(ctx, posArgs[0], opts, http.DefaultClient, blobStore)
	if err != nil {
		return err
	}
	printBuildWarnings(os.Stderr, plan)
	if opts.DryRun {
		printBuildPlan(os.Stdout, plan, opts)
		return nil
	}
	metricsRun, metricsErr := beginStandaloneMetricsRun(plan.Name, opts.Base)
	if metricsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics init: %v\n", metricsErr)
	}
	defer finishStandaloneMetricsRun(metricsRun)
	defer func(started time.Time) {
		if metricsRun == nil {
			return
		}
		status := "ok"
		if err != nil {
			status = err.Error()
		}
		emitMetricEvent("run_complete", started, status, map[string]any{"command": "build"})
	}(time.Now())
	exec := newBuildExecutor(plan, opts, blobStore)
	if err := exec.Execute(ctx); err != nil {
		return err
	}
	if err := pushBuildResult(ctx, exec.Result(), opts); err != nil {
		return err
	}
	printBuildResult(os.Stdout, plan, exec.Result(), opts)
	return nil
}

func splitBuildArgs(args []string) (flagArgs, posArgs []string, err error) {
	valueFlags := map[string]bool{
		"base":       true,
		"script":     true,
		"tag":        true,
		"cache-from": true,
		"cache-to":   true,
		"chunk-size": true,
		"compact":    true,
		"store-dir":  true,
	}
	boolFlags := map[string]bool{
		"push":              true,
		"dry-run":           true,
		"no-cache":          true,
		"keep-intermediate": true,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			posArgs = append(posArgs, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			posArgs = append(posArgs, arg)
			continue
		}
		name := strings.TrimLeft(arg, "-")
		if name == "" {
			posArgs = append(posArgs, arg)
			continue
		}
		if before, _, ok := strings.Cut(name, "="); ok {
			name = before
		}
		flagArgs = append(flagArgs, arg)
		if strings.Contains(arg, "=") || boolFlags[name] {
			continue
		}
		if valueFlags[name] {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("flag needs an argument: -%s", name)
			}
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return flagArgs, posArgs, nil
}

func validateBuildRegistryCache(opts buildOptions) error {
	var flags []string
	if len(opts.CacheFrom) > 0 {
		flags = append(flags, "--cache-from")
	}
	if len(opts.CacheTo) > 0 {
		flags = append(flags, "--cache-to")
	}
	if len(flags) == 0 {
		return nil
	}
	return fmt.Errorf("cove build: %s registry cache is not implemented yet", strings.Join(flags, " and "))
}

func buildDryPlan(ctx context.Context, name string, opts buildOptions, client *http.Client) (buildPlan, error) {
	return buildDryPlanWithStore(ctx, name, opts, client, store.New(opts.StoreDir))
}

func buildDryPlanWithStore(ctx context.Context, name string, opts buildOptions, client *http.Client, blobStore store.Store) (buildPlan, error) {
	_, parentDigest, err := resolveBuildBaseDigest(ctx, opts.Base)
	if err != nil {
		return buildPlan{}, fmt.Errorf("resolve base: %w", err)
	}
	base := opts.Base
	if dir, ok := localBuildBaseDir(opts.Base); ok {
		base = dir
	}
	plan := buildPlan{Name: name, Base: base, ParentDigest: parentDigest, Tags: append([]string(nil), opts.Tags...)}
	currentParent := parentDigest
	now := time.Now().UTC()
	for _, scriptName := range opts.Scripts {
		step, err := loadBuildScript(scriptName)
		if err != nil {
			return plan, err
		}
		if step.Meta.Compact == "targeted" && opts.Compact != "targeted" {
			step.Meta.Compact = opts.Compact
		}
		key, keyInput, err := buildCacheKey(ctx, currentParent, step, client)
		if err != nil {
			return plan, err
		}
		planStep := buildPlanStep{
			Name:                 step.Name,
			Source:               step.Source,
			Data:                 append([]byte(nil), step.Data...),
			Key:                  key,
			ParentDigest:         keyInput.ParentDigest,
			ScriptDigest:         keyInput.ScriptDigest,
			AgentProtocolVersion: keyInput.AgentProtocolVersion,
			Meta:                 step.Meta,
		}
		if !opts.NoCache {
			entry, err := loadBuildCacheEntry(blobStore, key)
			if err == nil {
				if buildCacheEntryFresh(entry, step.Meta.CacheTTL, now) {
					if err := validateBuildCacheEntryForStep(entry, planStep); err != nil {
						return plan, fmt.Errorf("build cache entry %s: %w", key, err)
					}
					planStep.CacheHit = true
					planStep.LayerDigest = entry.LayerDigest
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return plan, err
			}
		}
		plan.Steps = append(plan.Steps, planStep)
		currentParent = key
	}
	return plan, nil
}

func buildCacheEntryFresh(entry buildCacheEntry, ttl time.Duration, now time.Time) bool {
	if ttl <= 0 {
		return true
	}
	if entry.CreatedAt.IsZero() {
		return false
	}
	return now.Before(entry.CreatedAt.Add(ttl))
}

func printBuildPlan(w io.Writer, plan buildPlan, opts buildOptions) {
	fmt.Fprintf(w, "cove build %s\n", plan.Name)
	fmt.Fprintf(w, "  base: %s\n", plan.Base)
	fmt.Fprintf(w, "  parent digest: %s\n", plan.ParentDigest)
	for _, tag := range plan.Tags {
		fmt.Fprintf(w, "  tag: %s\n", tag)
	}
	if opts.NoCache {
		fmt.Fprintln(w, "  cache: disabled")
	}
	for i, step := range plan.Steps {
		fmt.Fprintf(w, "  step %d/%d: %s\n", i+1, len(plan.Steps), step.Name)
		fmt.Fprintf(w, "    key: %s\n", step.Key)
		if step.CacheHit {
			fmt.Fprintf(w, "    cache: hit")
			if step.LayerDigest != "" {
				fmt.Fprintf(w, " (%s)", step.LayerDigest)
			}
			fmt.Fprintln(w)
		} else if opts.NoCache {
			fmt.Fprintln(w, "    cache: disabled")
		} else {
			fmt.Fprintln(w, "    cache: miss")
		}
		fmt.Fprintf(w, "    compact: %s\n", step.Meta.Compact)
		if len(step.Meta.CacheEnv) > 0 {
			fmt.Fprintf(w, "    cache-env: %s\n", strings.Join(step.Meta.CacheEnv, ", "))
		}
		if len(step.Meta.CacheURL) > 0 {
			fmt.Fprintf(w, "    cache-url: %s\n", strings.Join(step.Meta.CacheURL, ", "))
		}
		if len(step.Meta.CacheFile) > 0 {
			fmt.Fprintf(w, "    cache-file: %s\n", strings.Join(step.Meta.CacheFile, ", "))
		}
		if len(step.Meta.Secrets) > 0 {
			fmt.Fprintf(w, "    secret names: %s\n", strings.Join(step.Meta.Secrets, ", "))
		}
		if step.Meta.CacheTTL > 0 {
			fmt.Fprintf(w, "    cache-ttl: %s\n", step.Meta.CacheTTL.Round(time.Second))
		}
	}
}

func printBuildWarnings(w io.Writer, plan buildPlan) {
	for _, warning := range buildPlanWarnings(plan) {
		fmt.Fprintf(w, "warning: %s\n", warning)
	}
}

func buildPlanWarnings(plan buildPlan) []string {
	var warnings []string
	for _, step := range plan.Steps {
		for _, name := range step.Meta.CacheEnv {
			if secretLikeCacheEnvName(name) {
				warnings = append(warnings, fmt.Sprintf("step %q cache-env %s looks secret; use # secret: for tokens, passwords, and keys", step.Name, name))
			}
		}
		if len(step.Meta.Secrets) > 0 && step.Meta.Compact == "fast" {
			warnings = append(warnings, fmt.Sprintf("step %q declares # secret: with # compact: fast; guest swap may retain plaintext", step.Name))
		}
	}
	return warnings
}

func secretLikeCacheEnvName(name string) bool {
	upper := strings.ToUpper(strings.TrimSpace(name))
	if upper == "" {
		return false
	}
	for _, suffix := range []string{"TOKEN", "PASSWORD", "SECRET", "KEY"} {
		if upper == suffix || strings.HasSuffix(upper, "_"+suffix) {
			return true
		}
	}
	return false
}

func printBuildResult(w io.Writer, plan buildPlan, result buildExecutionResult, opts buildOptions) {
	fmt.Fprintf(w, "Build complete\n")
	fmt.Fprintf(w, "  name: %s\n", plan.Name)
	fmt.Fprintf(w, "  base: %s\n", plan.Base)
	if result.VMDir != "" {
		fmt.Fprintf(w, "  vm: %s\n", result.VMDir)
	}
	if result.DiskPath != "" {
		fmt.Fprintf(w, "  disk: %s\n", result.DiskPath)
	}
	for _, tag := range plan.Tags {
		fmt.Fprintf(w, "  tag: %s\n", tag)
	}
	if opts.Push {
		fmt.Fprintf(w, "  pushed: %d\n", len(plan.Tags))
	}
	hits := 0
	for _, step := range result.Steps {
		if step.Step != "" && step.Key != "" {
			for _, planStep := range plan.Steps {
				if planStep.Key == step.Key && planStep.CacheHit {
					hits++
					break
				}
			}
		}
	}
	fmt.Fprintf(w, "  steps: %d\n", len(result.Steps))
	fmt.Fprintf(w, "  cache hits: %d/%d\n", hits, len(result.Steps))
	if opts.KeepIntermediate {
		fmt.Fprintln(w, "  intermediate: kept")
	}
}

func printBuildUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove build <name> --base <ref> --script <step> [flags]

Build a VM image by chaining vzscript steps with content-addressed cache keys.
Use --dry-run to print the resolved plan without running a scratch VM.

Flags:
  --base <ref|dir>          Base OCI image reference or local VM directory.
  --script <name|path>      Built-in vzscript recipe or .vzscript path. Repeat per step.
  --tag <ref>               Output image tag. Repeat for multiple tags.
  --push                    Push output tags after build.
  --dry-run                 Print the resolved build plan and cache keys only.
  --no-cache                Re-run every step instead of restoring cached layers.
  --cache-from <ref>        Reserved for registry cache import. Repeatable.
  --cache-to <ref>          Reserved for registry cache export. Repeatable.
  --keep-intermediate       Keep scratch VMs for debugging.
  --chunk-size <mb>         Chunk size in MiB. Default 512.
  --compact <mode>          fast, targeted, or thorough. Default targeted.
  --store-dir <dir>         Content store directory. Default ~/.vz/store.`)
}
