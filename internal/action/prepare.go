package action

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// PrepareConfig configures cove action prepare-image.
type PrepareConfig struct {
	CoveBin string
	Ref     string
	Runner  Runner
	Timeout time.Duration
}

// RunPrepare validates that ref can serve as a cove-action runner image.
func RunPrepare(ctx context.Context, cfg PrepareConfig) Report {
	if cfg.Runner == nil {
		cfg.Runner = ExecRunner{}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.CoveBin == "" {
		if exe, err := os.Executable(); err == nil {
			cfg.CoveBin = exe
		} else {
			cfg.CoveBin = "cove"
		}
	}
	ref := strings.TrimSpace(cfg.Ref)
	if ref == "" {
		return makeReport([]CheckResult{{Name: "image-ref", Status: StatusFail, Message: "image ref required"}})
	}

	checks := []CheckResult{
		prepareCommand(ctx, cfg, "image-inspect", []string{"image", "inspect", "-json", ref}, "image inspect ok"),
		prepareAgentVersion(ctx, cfg, ref),
	}
	for _, dep := range []string{"bash", "curl", "git", "docker"} {
		checks = append(checks, prepareCommand(ctx, cfg, "runner-dep-"+dep, []string{"shell", ref, "--", "which", dep}, dep+" present"))
	}
	checks = append(checks,
		prepareCommand(ctx, cfg, "shell-readiness", []string{"shell", ref, "--", "echo", "OK"}, "shell returned OK"),
		prepareTree(ctx, cfg, ref),
	)
	return makeReport(checks)
}

// RunDoctorCommand parses and runs cove action doctor.
func RunDoctorCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("action doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit JSON")
	coveBin := fs.String("cove-bin", "", "cove binary path")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: cove action doctor [--json]")
		return 1
	}
	report := RunDoctor(ctx, DoctorConfig{CoveBin: *coveBin})
	if err := WriteReport(stdout, report, *asJSON); err != nil {
		fmt.Fprintf(stderr, "cove action doctor: %v\n", err)
		return 1
	}
	return report.ExitCode()
}

// RunPrepareCommand parses and runs cove action prepare-image.
func RunPrepareCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("action prepare-image", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "emit JSON")
	coveBin := fs.String("cove-bin", "", "cove binary path")
	if err := fs.Parse(movePrepareFlagsFirst(args)); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: cove action prepare-image <ref> [--json]")
		return 1
	}
	report := RunPrepare(ctx, PrepareConfig{CoveBin: *coveBin, Ref: fs.Arg(0)})
	if err := WriteReport(stdout, report, *asJSON); err != nil {
		fmt.Fprintf(stderr, "cove action prepare-image: %v\n", err)
		return 1
	}
	return report.ExitCode()
}

func movePrepareFlagsFirst(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json", "-json":
			flags = append(flags, arg)
		case "--cove-bin", "-cove-bin":
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

func prepareAgentVersion(ctx context.Context, cfg PrepareConfig, ref string) CheckResult {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	agentOut, err := cfg.Runner.Run(ctx, cfg.CoveBin, "shell", ref, "--", "vz-agent", "-version")
	if err != nil {
		return CheckResult{Name: "agent-version", Status: StatusFail, Message: "agent version not current or unreadable: " + trimOutput(agentOut, err)}
	}
	hostOut, err := cfg.Runner.Run(ctx, cfg.CoveBin, "version")
	if err != nil {
		return CheckResult{Name: "agent-version", Status: StatusFail, Message: "host version unreadable: " + trimOutput(hostOut, err)}
	}
	agentVer := parseVersionToken(agentOut.Stdout + agentOut.Stderr)
	hostVer := parseVersionToken(hostOut.Stdout + hostOut.Stderr)
	if agentVer == "" || hostVer == "" {
		return CheckResult{Name: "agent-version", Status: StatusFail, Message: fmt.Sprintf("could not parse versions: agent %q host %q", strings.TrimSpace(agentOut.Stdout+agentOut.Stderr), strings.TrimSpace(hostOut.Stdout+hostOut.Stderr))}
	}
	if agentVer != hostVer {
		return CheckResult{Name: "agent-version", Status: StatusFail, Message: fmt.Sprintf("image agent %s, host %s", agentVer, hostVer)}
	}
	return CheckResult{Name: "agent-version", Status: StatusPass, Message: "agent version " + agentVer}
}

func prepareTree(ctx context.Context, cfg PrepareConfig, ref string) CheckResult {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	out, err := cfg.Runner.Run(ctx, cfg.CoveBin, "vm", "tree", "--reachable-from", ref)
	if err != nil {
		return CheckResult{Name: "reachable-forks", Status: StatusFail, Message: trimOutput(out, err)}
	}
	text := strings.ToLower(out.Stdout + out.Stderr)
	if strings.Contains(text, "orphan") && !strings.Contains(text, "0 orphan") && !strings.Contains(text, "zero orphan") {
		return CheckResult{Name: "reachable-forks", Status: StatusFail, Message: strings.TrimSpace(out.Stdout + out.Stderr)}
	}
	return CheckResult{Name: "reachable-forks", Status: StatusPass, Message: "zero orphan forks"}
}

func prepareCommand(ctx context.Context, cfg PrepareConfig, name string, args []string, ok string) CheckResult {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	out, err := cfg.Runner.Run(ctx, cfg.CoveBin, args...)
	if err != nil {
		return CheckResult{Name: name, Status: StatusFail, Message: trimOutput(out, err)}
	}
	if name == "shell-readiness" && !strings.Contains(out.Stdout+out.Stderr, "OK") {
		return CheckResult{Name: name, Status: StatusFail, Message: fmt.Sprintf("unexpected shell output: %q", strings.TrimSpace(out.Stdout+out.Stderr))}
	}
	return CheckResult{Name: name, Status: StatusPass, Message: ok}
}

var versionTokenRE = regexp.MustCompile(`\b(?:cove|vz-agent)\s+(\S+)`)

func parseVersionToken(s string) string {
	m := versionTokenRE.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) == 2 {
		return m[1]
	}
	return ""
}
