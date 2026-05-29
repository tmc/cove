package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/cove/internal/anthropicadapter"
	"github.com/tmc/cove/internal/vmconfig"
)

func runAnthropicAgentSandbox(ctx context.Context, opts agentSandboxRunOptions, vm, replayDir string) (agentsandboxResult, error) {
	dir, ok := vmconfig.ExistingPath(vm)
	if !ok {
		return agentsandboxResult{}, fmt.Errorf("agent-sandbox: vm not found: %s", vm)
	}
	transcriptPath := filepath.Join(replayDir, "anthropic-transcript.jsonl")
	transcript, err := os.OpenFile(transcriptPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return agentsandboxResult{}, fmt.Errorf("agent-sandbox: create anthropic transcript: %w", err)
	}
	control := &anthropicControl{client: NewControlClient(GetControlSocketPathForVM(dir))}
	entries, err := (&anthropicadapter.Adapter{
		Control:  control,
		VMID:     vm,
		Model:    anthropicModel(),
		MaxSteps: opts.maxSteps,
		Log:      transcript,
	}).Run(ctx, opts.task)
	if err != nil {
		_ = transcript.Close()
		return agentsandboxResult{}, err
	}
	if err := transcript.Close(); err != nil {
		return agentsandboxResult{}, fmt.Errorf("agent-sandbox: close anthropic transcript: %w", err)
	}
	return agentsandboxResult{FinalAnswer: finalAnthropicAnswer(entries)}, nil
}

type agentsandboxResult struct {
	FinalAnswer string
}

type anthropicControl struct {
	client *ControlClient
}

func (c *anthropicControl) ScreenSize(context.Context) (int, int, error) {
	data, _, err := c.client.ScreenshotData()
	if err != nil {
		return 0, 0, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

func (c *anthropicControl) ScreenshotPNG(context.Context) ([]byte, error) {
	data, _, err := c.client.ScreenshotData()
	return data, err
}

func (c *anthropicControl) Click(_ context.Context, x, y int) error {
	return c.client.MouseClickAbsolute(float64(x), float64(y))
}

func (c *anthropicControl) TypeText(_ context.Context, text string) error {
	return c.client.TypeText(text)
}

func (c *anthropicControl) Key(_ context.Context, keyCode uint16, modifiers uint) error {
	return c.client.KeyPressWithModifiers(keyCode, modifiers)
}

func (c *anthropicControl) Scroll(ctx context.Context, delta int) error {
	key := uint16(121)
	if delta < 0 {
		key = 116
	}
	return c.Key(ctx, key, 0)
}

func (c *anthropicControl) CursorPosition(context.Context) (int, int, error) {
	return 0, 0, nil
}

func anthropicModel() string {
	if model := strings.TrimSpace(os.Getenv("COVE_ANTHROPIC_MODEL")); model != "" {
		return model
	}
	return "claude-opus-4-7"
}

func finalAnthropicAnswer(entries []anthropicadapter.TranscriptEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "response" {
			return entries[i].Text
		}
	}
	return ""
}
