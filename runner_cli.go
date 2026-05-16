package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type runnerWorkflowConfig struct {
	Mode       string
	Image      string
	Script     string
	Workflow   string
	Job        string
	Labels     string
	ActionPath string
	Remote     string
	CoveBin    string
}

func runRunnerCommand(env commandEnv, _ string, args []string) int {
	if env.Stdout == nil {
		env.Stdout = os.Stdout
	}
	if env.Stderr == nil {
		env.Stderr = os.Stderr
	}
	if len(args) == 0 || isHelpArg(args[0]) {
		printRunnerUsage(env.Stderr)
		return usageExitCode(args)
	}
	switch args[0] {
	case "workflow":
		return runRunnerWorkflowCommand(env, args[1:])
	default:
		fmt.Fprintf(env.Stderr, "unknown runner command: %s\n", args[0])
		return 1
	}
}

func runRunnerWorkflowCommand(env commandEnv, args []string) int {
	cfg := runnerWorkflowConfig{
		Mode:       "self-hosted",
		Script:     "./ci/test.sh",
		Workflow:   "cove",
		Job:        "cove",
		Labels:     "self-hosted,macOS,ARM64,cove",
		ActionPath: "./.github/actions/cove-action",
		Remote:     "${{ secrets.COVE_HOST }}",
		CoveBin:    "cove",
	}
	fs := flag.NewFlagSet("runner workflow", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { printRunnerWorkflowUsage(fs.Output()) }
	fs.StringVar(&cfg.Mode, "mode", cfg.Mode, "workflow mode: self-hosted or github-hosted")
	fs.StringVar(&cfg.Image, "image", cfg.Image, "local cove image ref on the runner host")
	fs.StringVar(&cfg.Script, "script", cfg.Script, "guest shell script or command")
	fs.StringVar(&cfg.Workflow, "name", cfg.Workflow, "workflow name")
	fs.StringVar(&cfg.Job, "job", cfg.Job, "job name")
	fs.StringVar(&cfg.Labels, "labels", cfg.Labels, "comma-separated self-hosted runner labels")
	fs.StringVar(&cfg.ActionPath, "action-path", cfg.ActionPath, "path to cove-action in the checkout")
	fs.StringVar(&cfg.Remote, "remote", cfg.Remote, "ssh target for github-hosted mode")
	fs.StringVar(&cfg.CoveBin, "cove-bin", cfg.CoveBin, "cove binary name or path")
	if len(args) == 1 && isHelpArg(args[0]) {
		printRunnerWorkflowUsage(env.Stderr)
		return 0
	}
	if err := fs.Parse(moveRunnerWorkflowFlagsFirst(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		printRunnerWorkflowUsage(env.Stderr)
		return 1
	}
	if err := cfg.validate(); err != nil {
		fmt.Fprintf(env.Stderr, "cove runner workflow: %v\n", err)
		return 1
	}
	if err := cfg.write(env.Stdout); err != nil {
		fmt.Fprintf(env.Stderr, "cove runner workflow: %v\n", err)
		return 1
	}
	return 0
}

func (cfg runnerWorkflowConfig) validate() error {
	cfg.Mode = strings.TrimSpace(cfg.Mode)
	if cfg.Mode != "self-hosted" && cfg.Mode != "github-hosted" {
		return fmt.Errorf("invalid mode %q", cfg.Mode)
	}
	if strings.TrimSpace(cfg.Image) == "" {
		return errors.New("image is required")
	}
	if strings.TrimSpace(cfg.Script) == "" {
		return errors.New("script is required")
	}
	if strings.TrimSpace(cfg.Workflow) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(cfg.Job) == "" {
		return errors.New("job is required")
	}
	if cfg.Mode == "self-hosted" && len(runnerLabels(cfg.Labels)) == 0 {
		return errors.New("labels are required for self-hosted mode")
	}
	if cfg.Mode == "github-hosted" && strings.TrimSpace(cfg.Remote) == "" {
		return errors.New("remote is required for github-hosted mode")
	}
	return nil
}

func (cfg runnerWorkflowConfig) write(w io.Writer) error {
	switch cfg.Mode {
	case "self-hosted":
		return cfg.writeSelfHosted(w)
	case "github-hosted":
		return cfg.writeGitHubHosted(w)
	default:
		return fmt.Errorf("invalid mode %q", cfg.Mode)
	}
}

func (cfg runnerWorkflowConfig) writeSelfHosted(w io.Writer) error {
	_, err := fmt.Fprintf(w, `name: %s
on:
  workflow_dispatch:
  push:

jobs:
  %s:
    runs-on: [%s]
    steps:
      - uses: actions/checkout@v4
      - run: %s action doctor
      - run: %s action prepare-image %s --ttl 24h
      - uses: %s
        with:
          image: %s
          script: %s
`, yamlScalar(cfg.Workflow), yamlKey(cfg.Job), strings.Join(runnerLabels(cfg.Labels), ", "), shellWord(cfg.CoveBin), shellWord(cfg.CoveBin), shellWord(cfg.Image), yamlScalar(cfg.ActionPath), yamlScalar(cfg.Image), yamlBlock(cfg.Script, 10))
	return err
}

func (cfg runnerWorkflowConfig) writeGitHubHosted(w io.Writer) error {
	_, err := fmt.Fprintf(w, `name: %s
on:
  workflow_dispatch:
  push:

jobs:
  %s:
    runs-on: ubuntu-latest
    env:
      COVE_HOST: %s
      COVE_IMAGE: %s
    steps:
      - uses: actions/checkout@v4
      - name: Preflight remote cove host
        run: |
          ssh "$COVE_HOST" %s action doctor
          ssh "$COVE_HOST" %s action prepare-image "$COVE_IMAGE" --ttl 24h
      - name: Run in cove VM
        run: |
          rsync -az --delete ./ "$COVE_HOST:~/work/%s/"
          ssh "$COVE_HOST" COVE_IMAGE="$COVE_IMAGE" 'bash -s' <<'COVE_REMOTE'
          cd ~/work/%s
          go run ./cmd/cove-action -cove-bin %s -image "$COVE_IMAGE" -script %s
          COVE_REMOTE
`, yamlScalar(cfg.Workflow), yamlKey(cfg.Job), yamlScalar(cfg.Remote), yamlScalar(cfg.Image), shellWord(cfg.CoveBin), shellWord(cfg.CoveBin), shellPath(cfg.Job), shellPath(cfg.Job), shellQuote(cfg.CoveBin), shellQuote(cfg.Script))
	return err
}

func runnerLabels(s string) []string {
	var labels []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			labels = append(labels, yamlScalar(part))
		}
	}
	return labels
}

func yamlKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "job"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "job"
	}
	return out
}

func yamlScalar(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t:#{}[],'\"&*?|>-!%@`") {
		return strconvQuote(s)
	}
	return s
}

func yamlBlock(s string, indent int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	pad := strings.Repeat(" ", indent)
	if len(lines) == 1 {
		return yamlScalar(lines[0])
	}
	var b strings.Builder
	b.WriteString("|\n")
	for _, line := range lines {
		b.WriteString(pad)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func shellWord(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "cove"
	}
	if strings.ContainsAny(s, " \t\n'\"\\$`") {
		return shellQuote(s)
	}
	return s
}

func shellPath(s string) string {
	s = yamlKey(s)
	if s == "" {
		return "cove"
	}
	return s
}

func strconvQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func moveRunnerWorkflowFlagsFirst(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--mode", "-mode", "--image", "-image", "--script", "-script", "--name", "-name", "--job", "-job", "--labels", "-labels", "--action-path", "-action-path", "--remote", "-remote", "--cove-bin", "-cove-bin":
			flags = append(flags, arg)
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		default:
			rest = append(rest, arg)
		}
	}
	return append(flags, rest...)
}

func printRunnerUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove runner <command> [options]

Hosted-runner integration helpers.

Commands:
  workflow   Print a GitHub Actions workflow for cove-backed runner jobs`)
}

func printRunnerWorkflowUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove runner workflow --image <ref> [options]

Print a GitHub Actions workflow that consumes cove runner images.

Modes:
  self-hosted     Run on a trusted macOS self-hosted runner with cove installed
  github-hosted   Run on GitHub-hosted Linux and SSH into a trusted cove Mac

Flags:
  --mode <mode>          self-hosted or github-hosted (default self-hosted)
  --image <ref>          local cove image ref on the runner host
  --script <command>     guest command or script (default ./ci/test.sh)
  --name <name>          workflow name (default cove)
  --job <name>           job name (default cove)
  --labels <labels>      comma-separated self-hosted labels
  --action-path <path>   path to cove-action in the checkout
  --remote <target>      ssh target for github-hosted mode
  --cove-bin <path>      cove binary name or path`)
}
