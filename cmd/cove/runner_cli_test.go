package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunnerWorkflowSelfHosted(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRunnerCommand(commandEnv{Stdout: &stdout, Stderr: &stderr}, "runner", []string{
		"workflow",
		"--image", "macos-runner:14.5",
		"--script", "make test",
		"--job", "macos-ci",
	})
	if code != 0 {
		t.Fatalf("runRunnerCommand = %d, want 0; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"name: cove",
		"macos-ci:",
		`runs-on: ["self-hosted", macOS, ARM64, cove]`,
		"cove action doctor",
		"cove action prepare-image macos-runner:14.5 --ttl 24h",
		`uses: "./.github/actions/cove-action"`,
		"script: \"make test\"",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("workflow missing %q\n%s", want, out)
		}
	}
}

func TestRunnerWorkflowGitHubHosted(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRunnerCommand(commandEnv{Stdout: &stdout, Stderr: &stderr}, "runner", []string{
		"workflow",
		"--mode", "github-hosted",
		"--image", "macos-runner:latest",
		"--remote", "ci@example.local",
		"--script", "./ci/test.sh",
	})
	if code != 0 {
		t.Fatalf("runRunnerCommand = %d, want 0; stderr=%q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"runs-on: ubuntu-latest",
		`COVE_HOST: "ci@example.local"`,
		`ssh "$COVE_HOST" cove action doctor`,
		`rsync -az --delete ./ "$COVE_HOST:~/work/cove/"`,
		`go run ./cmd/cove-action -cove-bin 'cove' -image "$COVE_IMAGE" -script './ci/test.sh'`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("workflow missing %q\n%s", want, out)
		}
	}
}

func TestRunnerWorkflowRequiresImage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRunnerCommand(commandEnv{Stdout: &stdout, Stderr: &stderr}, "runner", []string{"workflow"})
	if code != 1 {
		t.Fatalf("runRunnerCommand = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "image is required") {
		t.Fatalf("stderr = %q, want image error", stderr.String())
	}
}

func TestRunnerWorkflowHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRunnerCommand(commandEnv{Stdout: &stdout, Stderr: &stderr}, "runner", []string{"workflow", "-h"})
	if code != 0 {
		t.Fatalf("runRunnerCommand = %d, want 0", code)
	}
	if !strings.Contains(stderr.String(), "Usage: cove runner workflow") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}
