// fork-bench measures cove fork latency against existing local VMs.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

type config struct {
	Cove       string
	Parents    string
	Runs       int
	Prefix     string
	Out        string
	Timeout    time.Duration
	Boot       bool
	Cleanup    bool
	MaxFork    time.Duration
	VMRoot     string
	CommitNote string
}

type diskInfo struct {
	Logical   int64
	Allocated int64
	Inode     uint64
}

type result struct {
	Parent        string
	Child         string
	ForkDuration  time.Duration
	AgentDuration time.Duration
	ParentDisk    diskInfo
	ChildDisk     diskInfo
	Error         string
}

func main() {
	cfg := parseFlags()
	if err := run(cfg, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "fork-bench: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.Cove, "cove", "./cove", "path to cove binary")
	flag.StringVar(&cfg.Parents, "parents", "", "comma-separated parent VM names")
	flag.IntVar(&cfg.Runs, "runs", 3, "forks per parent")
	flag.StringVar(&cfg.Prefix, "prefix", "fork-bench", "child VM name prefix")
	flag.StringVar(&cfg.Out, "out", "", "optional markdown output path")
	flag.DurationVar(&cfg.Timeout, "timeout", 2*time.Minute, "per-command timeout")
	flag.BoolVar(&cfg.Boot, "boot", false, "boot child and wait for agent-ping")
	flag.BoolVar(&cfg.Cleanup, "cleanup", true, "delete child VMs after each run")
	flag.DurationVar(&cfg.MaxFork, "max-fork", 0, "fail if any successful fork exceeds this duration")
	flag.StringVar(&cfg.VMRoot, "vm-root", "", "VM root directory (default: ~/.vz/vms)")
	flag.StringVar(&cfg.CommitNote, "note", "", "free-form note included in markdown")
	flag.Parse()
	return cfg
}

func run(cfg config, stdout io.Writer) error {
	parents := splitCSV(cfg.Parents)
	if len(parents) == 0 {
		return errors.New("-parents is required")
	}
	if cfg.Runs <= 0 {
		return errors.New("-runs must be positive")
	}
	root, err := vmRoot(cfg.VMRoot)
	if err != nil {
		return err
	}
	coveVersion := commandText(cfg.Timeout, cfg.Cove, "version")
	host := hostSummary()

	var results []result
	for _, parent := range parents {
		for i := 0; i < cfg.Runs; i++ {
			child := fmt.Sprintf("%s-%s-%d-%d", cfg.Prefix, sanitizeName(parent), time.Now().Unix(), i+1)
			r := runOne(cfg, root, parent, child)
			results = append(results, r)
			if cfg.Cleanup {
				cleanupChild(cfg, child)
			}
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Parent != results[j].Parent {
			return results[i].Parent < results[j].Parent
		}
		return results[i].Child < results[j].Child
	})

	doc := formatMarkdown(cfg, host, coveVersion, results)
	if cfg.Out != "" {
		if err := os.WriteFile(cfg.Out, []byte(doc), 0644); err != nil {
			return fmt.Errorf("write %s: %w", cfg.Out, err)
		}
	}
	if _, err := io.WriteString(stdout, doc); err != nil {
		return err
	}
	if cfg.MaxFork > 0 {
		for _, r := range results {
			if r.Error == "" && r.ForkDuration > cfg.MaxFork {
				return fmt.Errorf("%s fork took %s, exceeds %s", r.Child, r.ForkDuration.Round(time.Millisecond), cfg.MaxFork)
			}
		}
	}
	return nil
}

func runOne(cfg config, root, parent, child string) result {
	r := result{Parent: parent, Child: child}
	parentDisk := filepath.Join(root, parent, "disk.img")
	if info, err := statDisk(parentDisk); err == nil {
		r.ParentDisk = info
	} else {
		r.Error = fmt.Sprintf("stat parent disk: %v", err)
		return r
	}

	start := time.Now()
	if out, err := runCommand(cfg.Timeout, cfg.Cove, "fork", parent, child); err != nil {
		r.ForkDuration = time.Since(start)
		r.Error = fmt.Sprintf("fork: %v: %s", err, strings.TrimSpace(out))
		return r
	}
	r.ForkDuration = time.Since(start)

	childDisk := filepath.Join(root, child, "disk.img")
	if info, err := statDisk(childDisk); err == nil {
		r.ChildDisk = info
	} else {
		r.Error = fmt.Sprintf("stat child disk: %v", err)
		return r
	}
	if cfg.Boot {
		d, err := bootUntilAgent(cfg, child)
		r.AgentDuration = d
		if err != nil {
			r.Error = fmt.Sprintf("agent reachable: %v", err)
		}
	}
	return r
}

func bootUntilAgent(cfg config, child string) (time.Duration, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, cfg.Cove, "-vm", child, "run", "-headless", "-no-resume")
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start run: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	start := time.Now()
	deadline := time.NewTimer(cfg.Timeout)
	defer deadline.Stop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case err := <-done:
			if err != nil {
				return time.Since(start), fmt.Errorf("run exited: %w: %s", err, strings.TrimSpace(stderr.String()))
			}
			return time.Since(start), fmt.Errorf("run exited before agent-ping: %s", strings.TrimSpace(stderr.String()))
		case <-deadline.C:
			_ = stopVM(cfg, child)
			cancel()
			return time.Since(start), fmt.Errorf("timeout after %s", cfg.Timeout)
		case <-tick.C:
			if out, err := runCommand(5*time.Second, cfg.Cove, "-vm", child, "ctl", "agent-ping"); err == nil && strings.TrimSpace(out) != "" {
				_ = stopVM(cfg, child)
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					cancel()
				}
				return time.Since(start), nil
			}
		}
	}
}

func stopVM(cfg config, child string) error {
	_, err := runCommand(10*time.Second, cfg.Cove, "-vm", child, "ctl", "stop")
	return err
}

func cleanupChild(cfg config, child string) {
	_, _ = runCommand(cfg.Timeout, cfg.Cove, "vm", "delete", child)
}

func runCommand(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), ctx.Err()
	}
	return string(out), err
}

func commandText(timeout time.Duration, name string, args ...string) string {
	out, err := runCommand(timeout, name, args...)
	if err != nil {
		return fmt.Sprintf("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(out)
}

func statDisk(path string) (diskInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return diskInfo{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return diskInfo{Logical: info.Size()}, nil
	}
	return diskInfo{
		Logical:   info.Size(),
		Allocated: st.Blocks * 512,
		Inode:     st.Ino,
	}, nil
}

func vmRoot(value string) (string, error) {
	if value != "" {
		return value, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".vz", "vms"), nil
}

func splitCSV(value string) []string {
	var out []string
	for _, field := range strings.Split(value, ",") {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func sanitizeName(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-_")
	if s == "" {
		return "vm"
	}
	return s
}

func hostSummary() string {
	parts := []string{runtime.GOOS + "/" + runtime.GOARCH}
	if out := commandText(2*time.Second, "sw_vers", "-productVersion"); out != "" && !strings.Contains(out, "executable file not found") {
		parts = append(parts, "macOS "+out)
	}
	if out := commandText(2*time.Second, "sysctl", "-n", "machdep.cpu.brand_string"); out != "" && !strings.Contains(out, "executable file not found") {
		parts = append(parts, out)
	}
	return strings.Join(parts, ", ")
}

func formatMarkdown(cfg config, host, coveVersion string, results []result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# cove fork benchmark\n\n")
	fmt.Fprintf(&b, "- Date: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Host: %s\n", host)
	fmt.Fprintf(&b, "- Cove: `%s`\n", strings.ReplaceAll(coveVersion, "\n", " "))
	fmt.Fprintf(&b, "- Runs per parent: %d\n", cfg.Runs)
	fmt.Fprintf(&b, "- Boot to agent: %t\n", cfg.Boot)
	if cfg.MaxFork > 0 {
		fmt.Fprintf(&b, "- Max fork threshold: %s\n", cfg.MaxFork)
	}
	if cfg.CommitNote != "" {
		fmt.Fprintf(&b, "- Note: %s\n", cfg.CommitNote)
	}
	fmt.Fprintf(&b, "\n| Parent | Child | Disk logical | Parent stat blocks | Child stat blocks | Inodes differ | Fork wall | Agent reachable | Result |\n")
	fmt.Fprintf(&b, "|---|---|---:|---:|---:|---|---:|---:|---|\n")
	for _, r := range results {
		agent := ""
		if cfg.Boot {
			agent = r.AgentDuration.Round(time.Millisecond).String()
		}
		status := "ok"
		if r.Error != "" {
			status = r.Error
		}
		inodesDiffer := ""
		if r.ParentDisk.Inode != 0 && r.ChildDisk.Inode != 0 {
			inodesDiffer = fmt.Sprintf("%t", r.ParentDisk.Inode != r.ChildDisk.Inode)
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | %s | %s | %s | %s | %s |\n",
			r.Parent,
			r.Child,
			bytesText(r.ParentDisk.Logical),
			bytesText(r.ParentDisk.Allocated),
			bytesText(r.ChildDisk.Allocated),
			inodesDiffer,
			r.ForkDuration.Round(time.Millisecond),
			agent,
			escapePipes(status),
		)
	}
	return b.String()
}

func bytesText(n int64) string {
	if n == 0 {
		return ""
	}
	const gib = 1024 * 1024 * 1024
	const mib = 1024 * 1024
	switch {
	case n%gib == 0:
		return fmt.Sprintf("%d GiB", n/gib)
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	case n%mib == 0:
		return fmt.Sprintf("%d MiB", n/mib)
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/mib)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func escapePipes(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
