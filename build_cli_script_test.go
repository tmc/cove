package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/txtar"
	"rsc.io/script"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestBuildCLIScriptLocalBaseCacheHit(t *testing.T) {
	restoreControl := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restoreControl()
	oldStart := defaultBuildGuestStart
	oldCompact := defaultBuildCompact
	defer func() {
		defaultBuildGuestStart = oldStart
		defaultBuildCompact = oldCompact
	}()
	starts := 0
	defaultBuildGuestStart = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		starts++
		return func(context.Context) error { return nil }, nil
	}
	defaultBuildCompact = func(context.Context, buildScratch, string) error { return nil }

	root := t.TempDir()
	t.Setenv("HOME", root)
	state, err := script.NewState(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	ar := txtar.Parse([]byte(`
cove-build
stdout 'cache hits: 0/1'
guest-starts
stdout '^1$'
cove-build
stdout 'cache hits: 1/1'
guest-starts
stdout '^1$'

-- parent/disk.img --
base image
-- parent/aux.img --
aux
-- parent/hw.model --
hw
-- parent/machine.id --
machine
-- recipe.vzscript --
echo hello
`))
	if err := state.ExtractFiles(ar); err != nil {
		t.Fatal(err)
	}
	engine := buildCLIScriptEngine(t, &starts)
	var log bytes.Buffer
	if err := engine.Execute(state, "build-cli.txtar", bufio.NewReader(bytes.NewReader(ar.Comment)), &log); err != nil {
		t.Fatalf("execute: %v\nlog:\n%s", err, log.String())
	}
}

func TestBuildCLIScriptFailureScratchPolicy(t *testing.T) {
	restoreControl := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restoreControl()
	oldStart := defaultBuildGuestStart
	oldCompact := defaultBuildCompact
	defer func() {
		defaultBuildGuestStart = oldStart
		defaultBuildCompact = oldCompact
	}()
	starts := 0
	defaultBuildGuestStart = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		starts++
		return func(context.Context) error { return nil }, nil
	}
	defaultBuildCompact = func(context.Context, buildScratch, string) error { return nil }

	root := t.TempDir()
	t.Setenv("HOME", root)
	state, err := script.NewState(context.Background(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	ar := txtar.Parse([]byte(`
cove-build-error bad.vzscript
stdout 'step "bad" failed'
scratch-count
stdout '^0$'
cache-entry-count
stdout '^0$'
cove-build-error bad.vzscript --keep-intermediate
stdout 'scratch kept at'
scratch-count
stdout '^1$'
cache-entry-count
stdout '^0$'
guest-starts
stdout '^2$'

-- parent/disk.img --
base image
-- parent/aux.img --
aux
-- parent/hw.model --
hw
-- parent/machine.id --
machine
-- bad.vzscript --
unknown-command
`))
	if err := state.ExtractFiles(ar); err != nil {
		t.Fatal(err)
	}
	engine := buildCLIScriptEngine(t, &starts)
	var log bytes.Buffer
	if err := engine.Execute(state, "build-cli-failure.txtar", bufio.NewReader(bytes.NewReader(ar.Comment)), &log); err != nil {
		t.Fatalf("execute: %v\nlog:\n%s", err, log.String())
	}
}

func buildCLIScriptEngine(t *testing.T, starts *int) *script.Engine {
	t.Helper()
	cmds := script.DefaultCmds()
	cmds["cove-build"] = script.Command(script.CmdUsage{
		Summary: "run cove build against the txtar local base",
	}, func(s *script.State, args ...string) (script.WaitFunc, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("usage: cove-build")
		}
		return func(s *script.State) (string, string, error) {
			out, err := captureStdoutResult(t, func() error {
				return handleBuild([]string{
					"test-image",
					"--base", s.Path("parent"),
					"--script", s.Path("recipe.vzscript"),
					"--store-dir", s.Path("store"),
				})
			})
			return out, "", err
		}, nil
	})
	cmds["cove-build-error"] = script.Command(script.CmdUsage{
		Summary: "run cove build and print the returned error",
		Args:    "<script> [--keep-intermediate]",
	}, func(s *script.State, args ...string) (script.WaitFunc, error) {
		if len(args) < 1 || len(args) > 2 {
			return nil, fmt.Errorf("usage: cove-build-error <script> [--keep-intermediate]")
		}
		scriptName := args[0]
		keep := false
		if len(args) == 2 {
			if args[1] != "--keep-intermediate" {
				return nil, fmt.Errorf("usage: cove-build-error <script> [--keep-intermediate]")
			}
			keep = true
		}
		return func(s *script.State) (string, string, error) {
			buildArgs := []string{
				"test-image",
				"--base", s.Path("parent"),
				"--script", s.Path(scriptName),
				"--store-dir", s.Path("store"),
			}
			if keep {
				buildArgs = append(buildArgs, "--keep-intermediate")
			}
			err := handleBuild(buildArgs)
			if err == nil {
				return "ok\n", "", nil
			}
			return err.Error() + "\n", "", nil
		}, nil
	})
	cmds["guest-starts"] = script.Command(script.CmdUsage{
		Summary: "print scratch guest start count",
	}, func(s *script.State, args ...string) (script.WaitFunc, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("usage: guest-starts")
		}
		return func(*script.State) (string, string, error) {
			return strings.TrimSpace(fmt.Sprint(*starts)) + "\n", "", nil
		}, nil
	})
	cmds["scratch-count"] = script.Command(script.CmdUsage{
		Summary: "print build scratch directory count",
	}, func(s *script.State, args ...string) (script.WaitFunc, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("usage: scratch-count")
		}
		return func(s *script.State) (string, string, error) {
			n, err := countDirs(filepath.Join(s.Getwd(), ".vz", "build-scratch"))
			if err != nil {
				return "", "", err
			}
			return fmt.Sprintf("%d\n", n), "", nil
		}, nil
	})
	cmds["cache-entry-count"] = script.Command(script.CmdUsage{
		Summary: "print build cache entry count",
	}, func(s *script.State, args ...string) (script.WaitFunc, error) {
		if len(args) != 0 {
			return nil, fmt.Errorf("usage: cache-entry-count")
		}
		return func(s *script.State) (string, string, error) {
			n, err := countFiles(filepath.Join(s.Getwd(), "store", "build-cache", "keys"))
			if err != nil {
				return "", "", err
			}
			return fmt.Sprintf("%d\n", n), "", nil
		}, nil
	})
	return &script.Engine{Cmds: cmds}
}

func countDirs(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, entry := range entries {
		if entry.IsDir() {
			n++
		}
	}
	return n, nil
}

func countFiles(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			n++
		}
	}
	return n, nil
}

func TestPrintBuildResultReportsCacheHits(t *testing.T) {
	var out bytes.Buffer
	plan := buildPlan{
		Name: "vm",
		Steps: []buildPlanStep{{
			Key:      "sha256:" + strings.Repeat("1", 64),
			CacheHit: true,
		}},
	}
	result := buildExecutionResult{Steps: []buildApplyResult{{
		Step: "one",
		Key:  plan.Steps[0].Key,
	}}}
	printBuildResult(&out, plan, result, buildOptions{})
	if !strings.Contains(out.String(), "cache hits: 1/1") {
		t.Fatalf("output missing cache hits:\n%s", out.String())
	}
}
