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

func handleBuild(args []string) error {
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
	fs.Var(&cacheFrom, "cache-from", "registry cache source (repeatable)")
	fs.Var(&cacheTo, "cache-to", "registry cache destination (repeatable)")
	fs.BoolVar(&opts.KeepIntermediate, "keep-intermediate", false, "leave scratch VMs behind for debugging")
	fs.IntVar(&opts.ChunkSizeMB, "chunk-size", 512, "chunk size in MiB")
	fs.StringVar(&opts.Compact, "compact", "targeted", "compaction mode: fast, targeted, thorough")
	fs.Usage = func() { printBuildUsage(os.Stderr) }
	flagArgs, posArgs, err := splitBuildArgs(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if len(posArgs) != 1 {
		return fmt.Errorf("usage: cove build <name> --base <ref> --script <step> [--tag <ref>]")
	}
	if err := validateCompactMode(opts.Compact); err != nil {
		return err
	}
	opts.Scripts = scripts
	opts.Tags = tags
	opts.CacheFrom = cacheFrom
	opts.CacheTo = cacheTo
	if len(opts.Scripts) == 0 {
		return fmt.Errorf("cove build: at least one --script is required")
	}
	if opts.Base == "" {
		return fmt.Errorf("cove build: --base is required")
	}
	if !opts.DryRun {
		return fmt.Errorf("cove build: only --dry-run is implemented")
	}
	ctx := context.Background()
	plan, err := buildDryPlan(ctx, posArgs[0], opts, http.DefaultClient)
	if err != nil {
		return err
	}
	printBuildPlan(os.Stdout, plan, opts)
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

func buildDryPlan(ctx context.Context, name string, opts buildOptions, client *http.Client) (buildPlan, error) {
	return buildDryPlanWithStore(ctx, name, opts, client, store.New(opts.StoreDir))
}

func buildDryPlanWithStore(ctx context.Context, name string, opts buildOptions, client *http.Client, blobStore store.Store) (buildPlan, error) {
	_, parentDigest, err := resolveBuildBaseDigest(ctx, opts.Base)
	if err != nil {
		return buildPlan{}, fmt.Errorf("resolve base: %w", err)
	}
	plan := buildPlan{Name: name, Base: opts.Base, ParentDigest: parentDigest, Tags: append([]string(nil), opts.Tags...)}
	currentParent := parentDigest
	for _, scriptName := range opts.Scripts {
		step, err := loadBuildScript(scriptName)
		if err != nil {
			return plan, err
		}
		if step.Meta.Compact == "targeted" && opts.Compact != "targeted" {
			step.Meta.Compact = opts.Compact
		}
		key, _, err := buildCacheKey(ctx, currentParent, step, client)
		if err != nil {
			return plan, err
		}
		planStep := buildPlanStep{Name: step.Name, Key: key, Meta: step.Meta}
		if !opts.NoCache {
			entry, err := loadBuildCacheEntry(blobStore, key)
			if err == nil {
				planStep.CacheHit = true
				planStep.LayerDigest = entry.LayerDigest
			} else if !errors.Is(err, os.ErrNotExist) {
				return plan, err
			}
		}
		plan.Steps = append(plan.Steps, planStep)
		currentParent = key
	}
	return plan, nil
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

func printBuildUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove build <name> --base <ref> --script <step> [flags]

Plan a VM image build by chaining vzscript steps with content-addressed cache keys.
Execution is not implemented yet; use --dry-run.

Flags:
  --base <ref>              Base OCI image reference. Digest refs avoid network lookup.
  --script <name|path>      Built-in vzscript recipe or .vzscript path. Repeat per step.
  --tag <ref>               Output image tag. Repeat for multiple tags.
  --push                    Push output tags after build.
  --dry-run                 Print the resolved build plan and cache keys only.
  --no-cache                Re-run every step instead of restoring cached layers.
  --cache-from <ref>        Registry cache source. Repeatable.
  --cache-to <ref>          Registry cache destination. Repeatable.
  --keep-intermediate       Keep scratch VMs for debugging.
  --chunk-size <mb>         Chunk size in MiB. Default 512.
  --compact <mode>          fast, targeted, or thorough. Default targeted.`)
}
