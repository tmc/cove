package action

import (
	"context"
	"encoding/json"
	"errors"
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
	Force   bool
	TTL     time.Duration
	Now     time.Time
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
	if cfg.TTL == 0 {
		cfg.TTL = 24 * time.Hour
	}
	if cfg.Now.IsZero() {
		cfg.Now = time.Now()
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
	if prepareRefLooksUnsupportedRegistry(ref) {
		return makeReport([]CheckResult{{
			Name:    "image-ref",
			Status:  StatusFail,
			Message: "registry image refs are not supported here; use a local cove image ref or VM name",
		}})
	}

	if !cfg.Force {
		fresh := prepareFreshness(ctx, cfg, ref)
		if fresh.Status == StatusPass {
			return makeReport([]CheckResult{fresh})
		}
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

func prepareRefLooksUnsupportedRegistry(ref string) bool {
	host, _, ok := strings.Cut(ref, "/")
	return ok && (strings.ContainsAny(host, ".:") || host == "localhost")
}

// RunDoctorCommand parses and runs cove action doctor.
func RunDoctorCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("action doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { printDoctorUsage(fs.Output()) }
	asJSON := fs.Bool("json", false, "emit JSON")
	coveBin := fs.String("cove-bin", "", "cove binary path")
	if len(args) == 1 && actionHelpArg(args[0]) {
		printDoctorUsage(stderr)
		return 0
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		printDoctorUsage(stderr)
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
	fs.Usage = func() { printPrepareUsage(fs.Output()) }
	asJSON := fs.Bool("json", false, "emit JSON")
	force := fs.Bool("force", false, "run all checks even when image is fresh")
	ttl := fs.Duration("ttl", 24*time.Hour, "freshness TTL")
	coveBin := fs.String("cove-bin", "", "cove binary path")
	registryRef := fs.String("registry-ref", "", "unsupported remote registry ref")
	if len(args) == 1 && actionHelpArg(args[0]) {
		printPrepareUsage(stderr)
		return 0
	}
	if err := fs.Parse(movePrepareFlagsFirst(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() > 1 || (fs.NArg() == 1 && *registryRef != "") || (fs.NArg() == 0 && *registryRef == "") {
		printPrepareUsage(stderr)
		return 1
	}
	ref := strings.TrimSpace(*registryRef)
	if ref == "" {
		ref = fs.Arg(0)
	}
	report := RunPrepare(ctx, PrepareConfig{CoveBin: *coveBin, Ref: ref, Force: *force, TTL: *ttl})
	if err := WriteReport(stdout, report, *asJSON); err != nil {
		fmt.Fprintf(stderr, "cove action prepare-image: %v\n", err)
		return 1
	}
	return report.ExitCode()
}

func printDoctorUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove action doctor [--json] [--cove-bin <path>]

Check host-side prerequisites for private cove GitHub Actions runners.

Flags:
  --json             emit machine-readable JSON
  --cove-bin <path>  cove binary to inspect`)
}

func printPrepareUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove action prepare-image <ref> [--json] [--force] [--ttl <duration>]
       cove action prepare-image --registry-ref <ref> [--json] [--force] [--ttl <duration>]

Validate a local cove image or running VM for runner use.
Remote registry refs are accepted for diagnostics and currently report that
registry image refs are not supported by this command.

Flags:
  --json             emit machine-readable JSON
  --force            run all checks even when image freshness passes
  --ttl <duration>   freshness window, for example 24h
  --cove-bin <path>  cove binary to inspect
  --registry-ref <ref>
                     remote registry ref to diagnose as unsupported`)
}

func actionHelpArg(s string) bool {
	switch s {
	case "help", "-h", "-help", "--help":
		return true
	default:
		return false
	}
}

func movePrepareFlagsFirst(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--json", "-json", "--force", "-force":
			flags = append(flags, arg)
		case "--ttl", "-ttl":
			flags = append(flags, arg)
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		case "--cove-bin", "-cove-bin":
			flags = append(flags, arg)
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		case "--registry-ref", "-registry-ref":
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

func prepareFreshness(ctx context.Context, cfg PrepareConfig, ref string) CheckResult {
	out, err := cfg.Runner.Run(ctx, cfg.CoveBin, "image", "inspect", "-json", ref)
	if err != nil {
		return CheckResult{Name: "image-fresh", Status: StatusWarn, Message: "inspect failed: " + trimOutput(out, err)}
	}
	var payload struct {
		BuiltAt string `json:"built_at"`
		Created string `json:"created"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &payload); err != nil {
		return CheckResult{Name: "image-fresh", Status: StatusWarn, Message: "parse inspect JSON: " + err.Error()}
	}
	timestamp := strings.TrimSpace(payload.BuiltAt)
	if timestamp == "" {
		timestamp = strings.TrimSpace(payload.Created)
	}
	if timestamp == "" {
		return CheckResult{Name: "image-fresh", Status: StatusWarn, Message: "image timestamp unavailable"}
	}
	built, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return CheckResult{Name: "image-fresh", Status: StatusWarn, Message: "parse timestamp: " + err.Error()}
	}
	age := cfg.Now.Sub(built)
	if age < 0 {
		age = 0
	}
	if age <= cfg.TTL {
		return CheckResult{Name: "image-fresh", Status: StatusPass, Message: fmt.Sprintf("image already prepared, age=%s", age.Round(time.Second))}
	}
	return CheckResult{Name: "image-fresh", Status: StatusWarn, Message: fmt.Sprintf("image stale, age=%s exceeds ttl=%s", age.Round(time.Second), cfg.TTL)}
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
