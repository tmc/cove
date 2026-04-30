package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/txtar"
	"rsc.io/script"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestBuildCLIScriptLocalBaseCacheHit(t *testing.T) {
	restoreControl := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restoreControl()
	oldStart := defaultBuildGuestStart
	defer func() { defaultBuildGuestStart = oldStart }()
	starts := 0
	defaultBuildGuestStart = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		starts++
		return func(context.Context) error { return nil }, nil
	}

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
	return &script.Engine{Cmds: cmds}
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
