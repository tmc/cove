package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAgentSandboxRunArgs(t *testing.T) {
	opts, err := parseAgentSandboxRunArgs([]string{
		"--provider", "anthropic",
		"--image", "agentkit/macos:latest",
		"--task", "describe the desktop",
	})
	if err != nil {
		t.Fatalf("parseAgentSandboxRunArgs: %v", err)
	}
	if opts.provider != "anthropic" {
		t.Fatalf("provider = %q", opts.provider)
	}
	if opts.image != "agentkit/macos:latest" {
		t.Fatalf("image = %q", opts.image)
	}
	if opts.task != "describe the desktop" {
		t.Fatalf("task = %q", opts.task)
	}
	if opts.maxSteps != 25 {
		t.Fatalf("maxSteps = %d, want 25", opts.maxSteps)
	}
}

type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

func TestAgentSandboxDoctor(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test")
	old := agentSandboxDoctorDial
	t.Cleanup(func() { agentSandboxDoctorDial = old })
	agentSandboxDoctorDial = func(context.Context, string, string) (net.Conn, error) {
		return fakeConn{}, nil
	}
	var b strings.Builder
	if err := runAgentSandboxDoctor(context.Background(), &b, "anthropic"); err != nil {
		t.Fatalf("doctor: %v\n%s", err, b.String())
	}
	out := b.String()
	for _, want := range []string{"PASS env ANTHROPIC_API_KEY", "PASS network api.anthropic.com:443", "PASS model"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor missing %q:\n%s", want, out)
		}
	}
}

func TestAgentSandboxDoctorFailsMissingEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	old := agentSandboxDoctorDial
	t.Cleanup(func() { agentSandboxDoctorDial = old })
	agentSandboxDoctorDial = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("blocked")
	}
	var b strings.Builder
	err := runAgentSandboxDoctor(context.Background(), &b, "openai")
	if err == nil {
		t.Fatalf("doctor succeeded:\n%s", b.String())
	}
	if !strings.Contains(b.String(), "FAIL env OPENAI_API_KEY") {
		t.Fatalf("doctor output:\n%s", b.String())
	}
}

func TestParseAgentSandboxRunArgsProviderSwitchOnly(t *testing.T) {
	base := []string{"--image", "agentkit/macos-base:latest", "--task", "describe desktop"}
	for _, provider := range []string{"openai", "anthropic", "gemini", "vertex"} {
		args := append([]string{"--provider", provider}, base...)
		got, err := parseAgentSandboxRunArgs(args)
		if err != nil {
			t.Fatalf("%s: %v", provider, err)
		}
		if got.provider != provider {
			t.Fatalf("provider = %q, want %q", got.provider, provider)
		}
		if got.image != "agentkit/macos-base:latest" || got.task != "describe desktop" {
			t.Fatalf("non-provider options changed: %+v", got)
		}
	}
}

func TestAgentSandboxUsageListsProviderEnvVars(t *testing.T) {
	var b strings.Builder
	printAgentSandboxUsage(&b)
	out := b.String()
	for _, want := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_CLOUD_PROJECT", "COVE_VERTEX_PROJECT"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage missing %q:\n%s", want, out)
		}
	}
}

func TestParseAgentSandboxRunArgsRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "provider", args: []string{"--image", "x:1", "--task", "t"}, want: "-provider is required"},
		{name: "image", args: []string{"--provider", "anthropic", "--task", "t"}, want: "-image is required"},
		{name: "task", args: []string{"--provider", "anthropic", "--image", "x:1"}, want: "-task is required"},
		{name: "steps", args: []string{"--provider", "anthropic", "--image", "x:1", "--task", "t", "--max-steps", "0"}, want: "must be positive"},
		{name: "provider value", args: []string{"--provider", "bogus", "--image", "x:1", "--task", "t"}, want: "unsupported provider"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAgentSandboxRunArgs(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWriteReplayArtifacts(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "screens")
	replay := filepath.Join(dir, "replay")
	dst := filepath.Join(replay, "screenshots")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x02\x00\x00\x00\x90wS\xde\x00\x00\x00\x00IEND\xaeB`\x82")
	if err := os.WriteFile(filepath.Join(src, "step-001.png"), png, 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeReplayArtifacts(replay, dst, src, "done"); err != nil {
		t.Fatalf("writeReplayArtifacts: %v", err)
	}
	for _, rel := range []string{
		"final-answer.md",
		"ocr-text.txt",
		filepath.Join("screenshots", "step-001.png"),
	} {
		if _, err := os.Stat(filepath.Join(replay, rel)); err != nil {
			t.Fatalf("%s missing: %v", rel, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(replay, "final-answer.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "done" {
		t.Fatalf("final answer = %q", data)
	}
}

func TestPrepareAgentSandboxReplay(t *testing.T) {
	dir := t.TempDir()
	replay := filepath.Join(dir, "replay")
	screens := filepath.Join(replay, "screenshots")
	events := filepath.Join(replay, "control-events.jsonl")
	if err := prepareAgentSandboxReplay(replay, screens, events); err != nil {
		t.Fatalf("prepareAgentSandboxReplay: %v", err)
	}
	if _, err := os.Stat(screens); err != nil {
		t.Fatalf("screenshots dir missing: %v", err)
	}
	info, err := os.Stat(events)
	if err != nil {
		t.Fatalf("control events missing: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("control events size = %d, want 0", info.Size())
	}
	target, err := os.Readlink(filepath.Join(replay, "metrics.jsonl"))
	if err != nil {
		t.Fatalf("metrics link missing: %v", err)
	}
	if target != filepath.Join("..", "metrics.jsonl") {
		t.Fatalf("metrics link = %q", target)
	}
}
