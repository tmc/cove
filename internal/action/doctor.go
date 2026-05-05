package action

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const gib = 1024 * 1024 * 1024

// Status is the outcome of one preflight check.
type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

type StatfsFunc func(string) (DiskInfo, error)

type HookFunc func(context.Context, DoctorConfig) CheckResult

type DiskInfo struct {
	FreeBytes uint64
}

// Output is captured command output.
type Output struct {
	Stdout string
	Stderr string
}

// Runner runs a host command.
type Runner interface {
	Run(context.Context, string, ...string) (Output, error)
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(context.Context, string, ...string) (Output, error)

// Run calls f.
func (f RunnerFunc) Run(ctx context.Context, name string, args ...string) (Output, error) {
	return f(ctx, name, args...)
}

// ExecRunner runs commands with os/exec.
type ExecRunner struct{}

// Run executes name with args and returns combined output.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (Output, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Output{Stdout: stdout.String(), Stderr: stderr.String()}, err
}

// CheckResult records one action preflight check.
type CheckResult struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

// Report is the machine-readable action preflight result.
type Report struct {
	OK     bool          `json:"ok"`
	Status Status        `json:"status"`
	Checks []CheckResult `json:"checks"`
}

// ExitCode maps report status to process exit code.
func (r Report) ExitCode() int {
	switch r.Status {
	case StatusFail:
		return 1
	case StatusWarn:
		return 2
	default:
		return 0
	}
}

// DoctorConfig configures cove action doctor.
type DoctorConfig struct {
	CoveBin      string
	HomeDir      string
	VZDir        string
	RunsDir      string
	ForkFrom     string
	Runner       Runner
	Statfs       StatfsFunc
	AgentHook    HookFunc
	ForkFromHook HookFunc
	Timeout      time.Duration
}

// DoctorMain runs cove action doctor.
func DoctorMain(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	opts, err := ParseDoctorArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "cove action doctor: %v\n", err)
		return 1
	}
	report := RunDoctor(ctx, opts.Config)
	if err := WriteReport(stdout, report, opts.JSON); err != nil {
		fmt.Fprintf(stderr, "cove action doctor: %v\n", err)
		return 1
	}
	return report.ExitCode()
}

type DoctorOptions struct {
	Config DoctorConfig
	JSON   bool
}

func ParseDoctorArgs(args []string) (DoctorOptions, error) {
	var opts DoctorOptions
	fs := flag.NewFlagSet("action doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&opts.JSON, "json", false, "emit JSON")
	fs.StringVar(&opts.Config.CoveBin, "cove-bin", "", "cove binary")
	fs.StringVar(&opts.Config.HomeDir, "home", "", "home directory")
	fs.StringVar(&opts.Config.VZDir, "vz-dir", "", "cove state directory")
	fs.StringVar(&opts.Config.RunsDir, "runs-dir", "", "cove runs directory")
	fs.StringVar(&opts.Config.ForkFrom, "fork-from", "", "optional image ref to inspect")
	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if fs.NArg() != 0 {
		return opts, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return opts, nil
}

// RunDoctor executes the action host preflight.
func RunDoctor(ctx context.Context, cfg DoctorConfig) Report {
	cfg = cfg.withDefaults()
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	var checks []CheckResult
	checks = append(checks, checkCodesign(ctx, cfg)...)
	checks = append(checks, checkDisk(cfg))
	checks = append(checks, checkRunsWritable(cfg))
	checks = append(checks, checkNetwork(ctx, cfg))
	if cfg.AgentHook != nil {
		checks = append(checks, cfg.AgentHook(ctx, cfg))
	}
	if cfg.ForkFromHook != nil {
		checks = append(checks, cfg.ForkFromHook(ctx, cfg))
	} else if cfg.ForkFrom != "" {
		checks = append(checks, checkForkImage(ctx, cfg, cfg.ForkFrom))
	}
	return makeReport(checks)
}

func (cfg DoctorConfig) withDefaults() DoctorConfig {
	if cfg.Runner == nil {
		cfg.Runner = ExecRunner{}
	}
	if cfg.Statfs == nil {
		cfg.Statfs = statfs
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.CoveBin == "" {
		if exe, err := os.Executable(); err == nil {
			cfg.CoveBin = exe
		} else {
			cfg.CoveBin = "cove"
		}
	}
	if cfg.HomeDir == "" {
		cfg.HomeDir, _ = os.UserHomeDir()
	}
	if cfg.VZDir == "" {
		cfg.VZDir = filepath.Join(cfg.HomeDir, ".vz")
	}
	if cfg.RunsDir == "" {
		cfg.RunsDir = filepath.Join(cfg.VZDir, "runs")
	}
	if cfg.ForkFrom == "" {
		cfg.ForkFrom = strings.TrimSpace(os.Getenv("COVE_ACTION_FORK_FROM"))
	}
	return cfg
}

func checkCodesign(ctx context.Context, cfg DoctorConfig) []CheckResult {
	verifyOut, verifyErr := cfg.Runner.Run(ctx, "codesign", "-dv", cfg.CoveBin)
	sign := CheckResult{Name: "codesign", Status: StatusPass, Message: "signed cove binary verified"}
	if verifyErr != nil {
		sign.Status = StatusFail
		sign.Message = fmt.Sprintf("codesign failed: %s", trimOutput(verifyOut, verifyErr))
	} else if !strings.Contains(verifyOut.Stdout+verifyOut.Stderr, "Signature=") && !strings.Contains(verifyOut.Stdout+verifyOut.Stderr, "Authority=") {
		sign.Status = StatusFail
		sign.Message = "codesign output did not describe a signature"
	}

	entOut, entErr := cfg.Runner.Run(ctx, "codesign", "-d", "--entitlements", "-", cfg.CoveBin)
	ent := CheckResult{Name: "virtualization-entitlement", Status: StatusPass, Message: "com.apple.security.virtualization present"}
	if entErr != nil {
		ent.Status = StatusFail
		ent.Message = fmt.Sprintf("read entitlements failed: %s", trimOutput(entOut, entErr))
	} else if !strings.Contains(entOut.Stdout+entOut.Stderr, "com.apple.security.virtualization") {
		ent.Status = StatusFail
		ent.Message = "com.apple.security.virtualization entitlement missing"
	}
	return []CheckResult{sign, ent}
}

func checkDisk(cfg DoctorConfig) CheckResult {
	info, err := cfg.Statfs(cfg.VZDir)
	if err != nil {
		info, err = cfg.Statfs(filepath.Dir(cfg.VZDir))
		if err != nil {
			return CheckResult{Name: "disk-capacity", Status: StatusFail, Message: fmt.Sprintf("statfs %s: %v", cfg.VZDir, err)}
		}
	}
	free := info.FreeBytes
	switch {
	case free < 20*gib:
		return CheckResult{Name: "disk-capacity", Status: StatusFail, Message: fmt.Sprintf("%s free on %s; need at least 20 GiB", formatGiB(free), cfg.VZDir)}
	case free < 30*gib:
		return CheckResult{Name: "disk-capacity", Status: StatusWarn, Message: fmt.Sprintf("%s free on %s; 30 GiB recommended", formatGiB(free), cfg.VZDir)}
	default:
		return CheckResult{Name: "disk-capacity", Status: StatusPass, Message: fmt.Sprintf("%s free on %s", formatGiB(free), cfg.VZDir)}
	}
}

func checkRunsWritable(cfg DoctorConfig) CheckResult {
	if err := os.MkdirAll(cfg.RunsDir, 0o755); err != nil {
		return CheckResult{Name: "runs-writable", Status: StatusFail, Message: fmt.Sprintf("create %s: %v", cfg.RunsDir, err)}
	}
	f, err := os.CreateTemp(cfg.RunsDir, ".doctor-*")
	if err != nil {
		return CheckResult{Name: "runs-writable", Status: StatusFail, Message: fmt.Sprintf("write %s: %v", cfg.RunsDir, err)}
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return CheckResult{Name: "runs-writable", Status: StatusFail, Message: fmt.Sprintf("close %s: %v", name, err)}
	}
	if err := os.Remove(name); err != nil {
		return CheckResult{Name: "runs-writable", Status: StatusFail, Message: fmt.Sprintf("remove %s: %v", name, err)}
	}
	return CheckResult{Name: "runs-writable", Status: StatusPass, Message: cfg.RunsDir + " writable"}
}

func checkNetwork(ctx context.Context, cfg DoctorConfig) CheckResult {
	out, err := cfg.Runner.Run(ctx, cfg.CoveBin, "ctl", "network", "list")
	if err == nil {
		return CheckResult{Name: "network", Status: StatusPass, Message: firstLine(out)}
	}
	out, err = cfg.Runner.Run(ctx, cfg.CoveBin, "network", "list")
	if err != nil {
		return CheckResult{Name: "network", Status: StatusFail, Message: fmt.Sprintf("network list failed: %s", trimOutput(out, err))}
	}
	return CheckResult{Name: "network", Status: StatusPass, Message: firstLine(out)}
}

func checkForkImage(ctx context.Context, cfg DoctorConfig, ref string) CheckResult {
	out, err := cfg.Runner.Run(ctx, cfg.CoveBin, "image", "inspect", "-json", ref)
	if err != nil {
		return CheckResult{Name: "fork-from", Status: StatusFail, Message: fmt.Sprintf("inspect %s: %s", ref, trimOutput(out, err))}
	}
	if !strings.Contains(out.Stdout+out.Stderr, "agent") {
		return CheckResult{Name: "fork-from", Status: StatusWarn, Message: fmt.Sprintf("%s manifest has no agent feature metadata", ref)}
	}
	return CheckResult{Name: "fork-from", Status: StatusPass, Message: fmt.Sprintf("%s manifest includes agent metadata", ref)}
}

func makeReport(checks []CheckResult) Report {
	status := StatusPass
	for _, c := range checks {
		if c.Status == StatusFail {
			status = StatusFail
			break
		}
		if c.Status == StatusWarn {
			status = StatusWarn
		}
	}
	return Report{OK: status == StatusPass, Status: status, Checks: checks}
}

// WriteReport writes report as JSON or human-readable text.
func WriteReport(w io.Writer, report Report, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	for _, c := range report.Checks {
		fmt.Fprintf(w, "[%s] %s", c.Status, c.Name)
		if c.Message != "" {
			fmt.Fprintf(w, ": %s", c.Message)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "status: %s\n", report.Status)
	return nil
}

func statfs(path string) (DiskInfo, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return DiskInfo{}, err
	}
	return DiskInfo{FreeBytes: uint64(st.Bavail) * uint64(st.Bsize)}, nil
}

func trimOutput(out Output, err error) string {
	s := strings.TrimSpace(out.Stdout + out.Stderr)
	if s == "" && err != nil {
		s = err.Error()
	}
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func firstLine(out Output) string {
	s := strings.TrimSpace(out.Stdout + out.Stderr)
	if s == "" {
		return "ok"
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func formatGiB(n uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(n)/gib)
}
