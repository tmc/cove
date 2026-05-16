package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunFleetCommandDispatchBranches(t *testing.T) {
	path := writeFleetTestConfig(t)
	runner := &fakeFleetRunner{}

	t.Run("emptyArgs", func(t *testing.T) {
		var errOut bytes.Buffer
		err := runFleetCommandWithRunner(context.Background(), nil, path, runner, &bytes.Buffer{}, &errOut)
		if err == nil || !strings.Contains(err.Error(), "command required") {
			t.Fatalf("err = %v, want command required", err)
		}
		if !strings.Contains(errOut.String(), "Usage: cove fleet") {
			t.Fatalf("stderr = %q, want fleet usage", errOut.String())
		}
	})

	t.Run("topLevelHelp", func(t *testing.T) {
		var out bytes.Buffer
		if err := runFleetCommandWithRunner(context.Background(), []string{"-h"}, path, runner, &out, &bytes.Buffer{}); err != nil {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(out.String(), "Usage: cove fleet") {
			t.Fatalf("out = %q, want top-level usage", out.String())
		}
	})

	t.Run("unknownCommand", func(t *testing.T) {
		err := runFleetCommandWithRunner(context.Background(), []string{"frobnicate"}, path, runner, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("err = %v, want unknown command", err)
		}
	})

	t.Run("unknownCommandWithArg", func(t *testing.T) {
		err := runFleetCommandWithRunner(context.Background(), []string{"frobnicate", "arg"}, path, runner, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("err = %v, want unknown command", err)
		}
	})

	t.Run("unknownCommandWithMissingConfig", func(t *testing.T) {
		err := runFleetCommandWithRunner(context.Background(), []string{"frobnicate", "arg"}, path+"-missing", runner, &bytes.Buffer{}, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("err = %v, want unknown command", err)
		}
	})

	t.Run("subHelp", func(t *testing.T) {
		for _, sub := range []string{"ls", "rm", "vm", "image", "run"} {
			var out bytes.Buffer
			if err := runFleetCommandWithRunner(context.Background(), []string{sub, "-h"}, path, runner, &out, &bytes.Buffer{}); err != nil {
				t.Fatalf("%s -h err = %v", sub, err)
			}
			if !strings.Contains(out.String(), "Usage:") {
				t.Fatalf("%s -h out = %q, want Usage:", sub, out.String())
			}
		}
	})
}
