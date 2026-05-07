// Package agentsandbox runs cove computer-use provider adapters.
package agentsandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrNotSupported = errors.New("agent-sandbox: provider capability not supported")

// Provider names supported by the unified agent-sandbox command.
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderGemini    = "gemini"
	ProviderVertex    = "vertex"
)

type Capabilities struct {
	Screenshot bool
	Click      bool
	Type       bool
	Scroll     bool
	Wait       bool
}

type ProviderInfo struct {
	Name         string
	FirstClass   bool
	EnvVars      []string
	Capabilities Capabilities
	Notes        string
}

type Provider interface {
	Info() ProviderInfo
	Run(context.Context, Options) (Result, error)
}

var allCapabilities = Capabilities{Screenshot: true, Click: true, Type: true, Scroll: true, Wait: true}

var providers = map[string]Provider{
	ProviderOpenAI: pythonProvider{info: ProviderInfo{
		Name:         ProviderOpenAI,
		FirstClass:   true,
		EnvVars:      []string{"OPENAI_API_KEY"},
		Capabilities: allCapabilities,
		Notes:        "implemented by the OpenAI Agents SDK adapter",
	}},
	ProviderAnthropic: providerStub{info: ProviderInfo{
		Name:         ProviderAnthropic,
		FirstClass:   true,
		EnvVars:      []string{"ANTHROPIC_API_KEY"},
		Capabilities: allCapabilities,
		Notes:        "implemented by cove runtime",
	}},
	ProviderGemini: pythonProvider{info: ProviderInfo{
		Name:         ProviderGemini,
		FirstClass:   true,
		EnvVars:      []string{"GEMINI_API_KEY"},
		Capabilities: allCapabilities,
	}},
	ProviderVertex: pythonProvider{info: ProviderInfo{
		Name:         ProviderVertex,
		FirstClass:   true,
		EnvVars:      []string{"GOOGLE_CLOUD_PROJECT or COVE_VERTEX_PROJECT", "COVE_VERTEX_REGION optional"},
		Capabilities: allCapabilities,
	}},
}

func ProviderNames() []string {
	return []string{ProviderOpenAI, ProviderAnthropic, ProviderGemini, ProviderVertex}
}

func ProviderInfos() []ProviderInfo {
	names := ProviderNames()
	out := make([]ProviderInfo, 0, len(names))
	for _, name := range names {
		out = append(out, providers[name].Info())
	}
	return out
}

func LookupProvider(name string) (Provider, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	p, ok := providers[key]
	if !ok {
		return nil, fmt.Errorf("agent-sandbox: unsupported provider %q", name)
	}
	return p, nil
}

// Options configures a provider run against an already-running cove VM.
type Options struct {
	Provider      string
	VMName        string
	Task          string
	MaxSteps      int
	ScreenshotDir string
	ReplayDir     string
	EventsPath    string
	RepoRoot      string
	Stdout        io.Writer
	Stderr        io.Writer
}

// Result is the provider loop result captured by the wrapper.
type Result struct {
	FinalAnswer string
}

// Run executes a provider adapter.
func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 25
	}
	if strings.TrimSpace(opts.VMName) == "" {
		return Result{}, errors.New("agent-sandbox: vm name required")
	}
	if strings.TrimSpace(opts.Task) == "" {
		return Result{}, errors.New("agent-sandbox: task required")
	}
	provider, err := LookupProvider(opts.Provider)
	if err != nil {
		return Result{}, err
	}
	return provider.Run(ctx, opts)
}

type providerStub struct {
	info ProviderInfo
}

func (p providerStub) Info() ProviderInfo { return p.info }

func (p providerStub) Run(context.Context, Options) (Result, error) {
	switch p.info.Name {
	case ProviderAnthropic:
		return Result{}, fmt.Errorf("%w: anthropic provider is implemented by the cove runtime", ErrNotSupported)
	default:
		return Result{}, ErrNotSupported
	}
}

type pythonProvider struct {
	info ProviderInfo
}

func (p pythonProvider) Info() ProviderInfo { return p.info }

func (p pythonProvider) Run(ctx context.Context, opts Options) (Result, error) {
	return runPythonBridge(ctx, p.info.Name, opts)
}

func runPythonBridge(ctx context.Context, provider string, opts Options) (Result, error) {
	script, err := providerScript(provider, opts.RepoRoot)
	if err != nil {
		return Result{}, err
	}
	args := []string{script, "--vm", opts.VMName, "--task", opts.Task}
	switch provider {
	case ProviderOpenAI:
		args = append(args, "--max-steps", fmt.Sprint(opts.MaxSteps))
		if model := strings.TrimSpace(os.Getenv("COVE_OPENAI_MODEL")); model != "" {
			args = append(args, "--model", model)
		}
	case ProviderAnthropic:
		args = append(args, "--max-iters", fmt.Sprint(opts.MaxSteps))
	case ProviderGemini:
		args = append(args, "--max-iterations", fmt.Sprint(opts.MaxSteps))
		if model := strings.TrimSpace(os.Getenv("COVE_GEMINI_MODEL")); model != "" {
			args = append(args, "--model", model)
		}
	case ProviderVertex:
		project := strings.TrimSpace(os.Getenv("COVE_VERTEX_PROJECT"))
		if project == "" {
			project = strings.TrimSpace(os.Getenv("GOOGLE_CLOUD_PROJECT"))
		}
		if project == "" {
			return Result{}, errors.New("agent-sandbox: vertex provider requires COVE_VERTEX_PROJECT or GOOGLE_CLOUD_PROJECT")
		}
		args = append(args, "--project", project, "--max-iterations", fmt.Sprint(opts.MaxSteps))
		if region := strings.TrimSpace(os.Getenv("COVE_VERTEX_REGION")); region != "" {
			args = append(args, "--region", region)
		}
		if model := strings.TrimSpace(os.Getenv("COVE_VERTEX_MODEL")); model != "" {
			args = append(args, "--model", model)
		}
	}
	if opts.ScreenshotDir != "" {
		args = append(args, "--screenshot-dir", opts.ScreenshotDir)
	}
	if opts.EventsPath != "" {
		args = append(args, "--events-jsonl", opts.EventsPath)
	}
	cmd := exec.CommandContext(ctx, pythonBinary(), args...)
	cmd.Dir = repoRootOrCWD(opts.RepoRoot)
	cmd.Env = os.Environ()
	var out strings.Builder
	if opts.Stdout != nil {
		cmd.Stdout = io.MultiWriter(opts.Stdout, &out)
	} else {
		cmd.Stdout = &out
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	}
	if err := cmd.Run(); err != nil {
		final := strings.TrimSpace(out.String())
		return Result{FinalAnswer: final}, fmt.Errorf("agent-sandbox: %s provider: %w", provider, err)
	}
	return Result{FinalAnswer: strings.TrimSpace(out.String())}, nil
}

func providerScript(provider, root string) (string, error) {
	base := repoRootOrCWD(root)
	var rel string
	switch provider {
	case ProviderOpenAI:
		rel = filepath.Join("adapters", "openai-agents-python", "examples", "computer_use.py")
	case ProviderAnthropic:
		rel = filepath.Join("adapters", "anthropic-bridge", "computer_use.py")
	case ProviderGemini:
		rel = filepath.Join("adapters", "google-bridge", "computer_use.py")
	case ProviderVertex:
		rel = filepath.Join("adapters", "google-bridge", "vertex-ai", "computer_use.py")
	default:
		return "", fmt.Errorf("agent-sandbox: unsupported provider %q", provider)
	}
	path := filepath.Join(base, rel)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("agent-sandbox: provider script %s: %w", rel, err)
	}
	return path, nil
}

func repoRootOrCWD(root string) string {
	if root != "" {
		return root
	}
	if cwd, err := os.Getwd(); err == nil {
		if hasAdapters(cwd) {
			return cwd
		}
		for dir := cwd; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if hasAdapters(dir) {
				return dir
			}
		}
	}
	_, file, _, ok := runtime.Caller(0)
	if ok {
		dir := filepath.Dir(filepath.Dir(filepath.Dir(file)))
		if hasAdapters(dir) {
			return dir
		}
	}
	return "."
}

func hasAdapters(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "adapters"))
	return err == nil
}

func pythonBinary() string {
	if p := strings.TrimSpace(os.Getenv("COVE_AGENT_SANDBOX_PYTHON")); p != "" {
		return p
	}
	return "python3"
}
