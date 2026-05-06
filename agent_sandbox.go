package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ocrx "github.com/tmc/apple/x/vzkit/ocr"
	"github.com/tmc/vz-macos/internal/agentsandbox"
	runmetrics "github.com/tmc/vz-macos/internal/metrics"
)

type agentSandboxRunOptions struct {
	provider      string
	image         string
	task          string
	screenshotDir string
	maxSteps      int
	vmName        string
}

func handleAgentSandboxCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("agent-sandbox requires subcommand: run")
	}
	switch args[0] {
	case "run":
		return handleAgentSandboxRun(args[1:])
	case "-h", "--help", "help":
		printAgentSandboxUsage(os.Stdout)
		return nil
	default:
		return fmt.Errorf("agent-sandbox: unknown subcommand %q", args[0])
	}
}

func handleAgentSandboxRun(args []string) error {
	opts, err := parseAgentSandboxRunArgs(args)
	if err != nil {
		return err
	}
	return runAgentSandbox(context.Background(), opts)
}

func parseAgentSandboxRunArgs(args []string) (agentSandboxRunOptions, error) {
	var opts agentSandboxRunOptions
	opts.maxSteps = 25
	fs := flag.NewFlagSet("agent-sandbox run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&opts.provider, "provider", "", "provider: openai, anthropic, gemini, or vertex")
	fs.StringVar(&opts.image, "image", "", "local image ref to fork")
	fs.StringVar(&opts.task, "task", "", "prompt for the provider agent loop")
	fs.StringVar(&opts.screenshotDir, "screenshot-dir", "", "directory for per-step screenshots")
	fs.IntVar(&opts.maxSteps, "max-steps", 25, "maximum provider agent steps")
	fs.StringVar(&opts.vmName, "vm", "", "ephemeral VM name")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("agent-sandbox run: unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	opts.provider = strings.ToLower(strings.TrimSpace(opts.provider))
	switch opts.provider {
	case agentsandbox.ProviderOpenAI, agentsandbox.ProviderAnthropic, agentsandbox.ProviderGemini, agentsandbox.ProviderVertex:
	case "":
		return opts, errors.New("agent-sandbox run: -provider is required")
	default:
		return opts, fmt.Errorf("agent-sandbox run: unsupported provider %q", opts.provider)
	}
	if strings.TrimSpace(opts.image) == "" {
		return opts, errors.New("agent-sandbox run: -image is required")
	}
	if strings.TrimSpace(opts.task) == "" {
		return opts, errors.New("agent-sandbox run: -task is required")
	}
	if opts.maxSteps <= 0 {
		return opts, errors.New("agent-sandbox run: -max-steps must be positive")
	}
	return opts, nil
}

func runAgentSandbox(ctx context.Context, opts agentSandboxRunOptions) (runErr error) {
	suffix, err := generateRunID()
	if err != nil {
		return err
	}
	vm := strings.TrimSpace(opts.vmName)
	if vm == "" {
		vm = "agent-sandbox-" + suffix
	}
	bundle, err := NewRunBundle(runsDirHook(), vm, opts.image)
	if err != nil {
		return err
	}
	if err := bundle.AppendEvent(map[string]any{
		"event":    "agent_sandbox.start",
		"run_id":   bundle.ID(),
		"provider": opts.provider,
		"image":    opts.image,
	}); err != nil {
		return err
	}
	defer func() {
		finishAgentSandboxBundle(bundle, runErr)
	}()

	runDir := bundle.Dir()
	screenshotDir := opts.screenshotDir
	if screenshotDir == "" {
		screenshotDir = filepath.Join(runDir, "screenshots")
	}
	replayDir := filepath.Join(runDir, "replay")
	replayScreenshots := filepath.Join(replayDir, "screenshots")
	eventsPath := filepath.Join(replayDir, "control-events.jsonl")
	if err := prepareAgentSandboxReplay(replayDir, replayScreenshots, eventsPath); err != nil {
		return err
	}
	finalAnswer := ""
	replayWritten := false
	defer func() {
		if replayWritten {
			return
		}
		if err := writeReplayArtifacts(replayDir, replayScreenshots, screenshotDir, finalAnswer); err != nil && runErr == nil {
			runErr = err
		}
	}()
	if err := bundle.EmitMetric(ctx, runmetrics.Event{
		EventType: "agent_sandbox_start",
		VMName:    vm,
		ImageRef:  opts.image,
		Status:    "start",
		Extra: map[string]any{
			"run_id":   bundle.ID(),
			"provider": opts.provider,
		},
	}); err != nil {
		return fmt.Errorf("agent-sandbox: metrics: %w", err)
	}

	coveBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("agent-sandbox: resolve cove binary: %w", err)
	}
	runCmd := exec.CommandContext(ctx, coveBin, "run", "-fork-from", opts.image, "-fork-name", vm, "-ephemeral", "-auto-upgrade-agent")
	runCmd.Stdout = os.Stdout
	runCmd.Stderr = os.Stderr
	if err := runCmd.Start(); err != nil {
		return fmt.Errorf("agent-sandbox: start fork: %w", err)
	}
	stopped := false
	defer func() {
		if !stopped {
			stopAgentSandboxVM(context.Background(), coveBin, vm)
		}
		waitErr := waitAgentSandboxRun(runCmd)
		if runErr == nil && waitErr != nil {
			runErr = waitErr
		}
	}()

	if err := waitAgentSandboxReady(ctx, coveBin, vm); err != nil {
		return err
	}
	var result agentsandboxResult
	var providerErr error
	if opts.provider == agentsandbox.ProviderAnthropic {
		result, providerErr = runAnthropicAgentSandbox(ctx, opts, vm, replayDir)
	} else {
		providerResult, err := agentsandbox.Run(ctx, agentsandbox.Options{
			Provider:      opts.provider,
			VMName:        vm,
			Task:          opts.task,
			MaxSteps:      opts.maxSteps,
			ScreenshotDir: screenshotDir,
			ReplayDir:     replayDir,
			EventsPath:    eventsPath,
			Stdout:        os.Stdout,
			Stderr:        os.Stderr,
		})
		result = agentsandboxResult{FinalAnswer: providerResult.FinalAnswer}
		providerErr = err
	}
	finalAnswer = result.FinalAnswer
	if writeErr := writeReplayArtifacts(replayDir, replayScreenshots, screenshotDir, result.FinalAnswer); writeErr != nil && providerErr == nil {
		providerErr = writeErr
	} else if writeErr == nil {
		replayWritten = true
	}
	if stopErr := stopAgentSandboxVM(ctx, coveBin, vm); stopErr != nil && providerErr == nil {
		providerErr = stopErr
	}
	stopped = true
	if providerErr != nil {
		return providerErr
	}
	fmt.Printf("agent-sandbox replay: %s\n", replayDir)
	return nil
}

func prepareAgentSandboxReplay(replayDir, replayScreenshots, eventsPath string) error {
	if err := os.MkdirAll(replayScreenshots, 0755); err != nil {
		return fmt.Errorf("agent-sandbox: create replay dir: %w", err)
	}
	f, err := os.OpenFile(eventsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("agent-sandbox: create control events: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("agent-sandbox: create control events: %w", err)
	}
	if err := linkReplayMetrics(replayDir); err != nil {
		return err
	}
	return nil
}

func waitAgentSandboxReady(ctx context.Context, coveBin, vm string) error {
	cmd := exec.CommandContext(ctx, coveBin, "ctl", "-vm", vm, "-wait", "120s", "agent-ping")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("agent-sandbox: wait for guest agent: %w", err)
	}
	return nil
}

func stopAgentSandboxVM(ctx context.Context, coveBin, vm string) error {
	cmd := exec.CommandContext(ctx, coveBin, "ctl", "-vm", vm, "stop")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("agent-sandbox: stop fork: %w", err)
	}
	return nil
}

func waitAgentSandboxRun(cmd *exec.Cmd) error {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("agent-sandbox: fork run exited: %w", err)
		}
		return nil
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("agent-sandbox: fork did not stop after 30s")
	}
}

func writeReplayArtifacts(replayDir, replayScreenshots, screenshotDir, finalAnswer string) error {
	if err := os.MkdirAll(replayDir, 0755); err != nil {
		return fmt.Errorf("agent-sandbox: create replay dir: %w", err)
	}
	if err := copyScreenshots(screenshotDir, replayScreenshots); err != nil {
		return err
	}
	if err := writeOCRText(filepath.Join(replayDir, "ocr-text.txt"), replayScreenshots); err != nil {
		return err
	}
	if strings.TrimSpace(finalAnswer) == "" {
		finalAnswer = "(no final answer)\n"
	}
	if err := os.WriteFile(filepath.Join(replayDir, "final-answer.md"), []byte(finalAnswer), 0644); err != nil {
		return fmt.Errorf("agent-sandbox: write final answer: %w", err)
	}
	return nil
}

func copyScreenshots(srcDir, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("agent-sandbox: create replay screenshots: %w", err)
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("agent-sandbox: read screenshots: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".png") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("agent-sandbox: read screenshot: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, entry.Name()), data, 0644); err != nil {
			return fmt.Errorf("agent-sandbox: copy screenshot: %w", err)
		}
	}
	return nil
}

func writeOCRText(path, screenshotsDir string) error {
	entries, err := os.ReadDir(screenshotsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(path, []byte("(no screenshots)\n"), 0644)
		}
		return fmt.Errorf("agent-sandbox: read replay screenshots: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var out strings.Builder
	ocr := ocrx.NewService(false)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".png") {
			continue
		}
		name := entry.Name()
		out.WriteString("=== " + name + " ===\n")
		f, err := os.Open(filepath.Join(screenshotsDir, name))
		if err != nil {
			out.WriteString("error: " + err.Error() + "\n\n")
			continue
		}
		img, _, err := image.Decode(f)
		_ = f.Close()
		if err != nil {
			out.WriteString("error: " + err.Error() + "\n\n")
			continue
		}
		observations, err := ocr.RecognizeText(img)
		if err != nil {
			out.WriteString("error: " + err.Error() + "\n\n")
			continue
		}
		if len(observations) == 0 {
			out.WriteString("(no text detected)\n\n")
			continue
		}
		for _, obs := range observations {
			out.WriteString(obs.Text + "\n")
		}
		out.WriteString("\n")
	}
	if out.Len() == 0 {
		out.WriteString("(no screenshots)\n")
	}
	if err := os.WriteFile(path, []byte(out.String()), 0644); err != nil {
		return fmt.Errorf("agent-sandbox: write ocr text: %w", err)
	}
	return nil
}

func linkReplayMetrics(replayDir string) error {
	link := filepath.Join(replayDir, "metrics.jsonl")
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("agent-sandbox: remove metrics link: %w", err)
	}
	if err := os.Symlink(filepath.Join("..", "metrics.jsonl"), link); err != nil {
		return fmt.Errorf("agent-sandbox: link metrics: %w", err)
	}
	return nil
}

func finishAgentSandboxBundle(bundle *RunBundle, runErr error) {
	status := "ok"
	if runErr != nil {
		status = runErr.Error()
	}
	_ = bundle.AppendEvent(map[string]any{
		"event":       "agent_sandbox.exit",
		"run_id":      bundle.ID(),
		"exit_status": status,
	})
	_ = bundle.EmitMetric(context.Background(), runmetrics.Event{
		EventType: "agent_sandbox_complete",
		VMName:    bundle.vmName,
		ImageRef:  bundle.forkFrom,
		Status:    status,
		Extra: map[string]any{
			"run_id": bundle.ID(),
		},
	})
	if err := bundle.Finalize(runErr); err != nil {
		fmt.Fprintf(os.Stderr, "warning: agent-sandbox bundle finalize: %v\n", err)
	}
}

func printAgentSandboxUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage:
  cove agent-sandbox run --provider openai|anthropic|gemini|vertex --image <ref> --task <prompt> [options]

Options:
  --provider <name>       provider: openai, anthropic, gemini, vertex
                          env: OPENAI_API_KEY, ANTHROPIC_API_KEY, GEMINI_API_KEY,
                               GOOGLE_CLOUD_PROJECT or COVE_VERTEX_PROJECT
  --image <ref>           local image ref to fork, for example agentkit/macos-base:latest
  --task <prompt>         provider task prompt
  --screenshot-dir <dir>  screenshot output directory (default: ~/.vz/runs/<run-id>/screenshots)
  --max-steps N           maximum provider agent steps (default: 25)
  --vm <name>             ephemeral VM name (default: agent-sandbox-<id>)

Replay:
  writes ~/.vz/runs/<run-id>/replay/ with screenshots, OCR text, control events,
  final-answer.md, and a metrics.jsonl symlink.
`)
}
