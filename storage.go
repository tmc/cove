package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/storagecensus"
	"github.com/tmc/vz-macos/internal/store"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

func handleStorageCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cove storage <subcommand> [args]\n  census    Walk ~/.vz/ and report per-category disk usage\n  budget    Show or update the storage budget\n  prune     Remove safe-to-delete cruft (build-scratch only for now)")
	}
	switch args[0] {
	case "census":
		return runStorageCensus(args[1:], os.Stdout)
	case "budget":
		return runStorageBudget(args[1:], os.Stdout)
	case "prune":
		return runStoragePrune(args[1:], os.Stdout)
	default:
		return fmt.Errorf("storage: unknown subcommand %q", args[0])
	}
}

func runStoragePrune(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cove storage prune <category> [flags]\n  build-scratch    Remove ~/.vz/build-scratch/<id> dirs older than -older-than")
	}
	switch args[0] {
	case "build-scratch":
		return runStoragePruneBuildScratch(args[1:], out)
	default:
		return fmt.Errorf("storage prune: unknown category %q", args[0])
	}
}

func runStoragePruneBuildScratch(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("storage prune build-scratch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	olderThan := fs.Duration("older-than", 7*24*time.Hour, "delete build-scratch dirs older than this duration")
	apply := fs.Bool("apply", false, "actually delete; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove storage prune build-scratch [-older-than DUR] [-apply]")
	}
	rep, err := pruneBuildScratch(defaultBuildScratchRoot(), *olderThan, *apply, nil, nil)
	if err != nil {
		return fmt.Errorf("storage prune build-scratch: %w", err)
	}
	return writePruneBuildScratchReport(out, rep)
}

func writePruneBuildScratchReport(out io.Writer, rep pruneBuildScratchReport) error {
	mode := "dry-run"
	if rep.Apply {
		mode = "apply"
	}
	fmt.Fprintf(out, "build-scratch prune: root=%s older-than=%s sanity-floor=%s mode=%s\n",
		rep.Root, rep.OlderThan, rep.SanityFloor, mode)
	for _, e := range rep.Entries {
		fmt.Fprintf(out, "  %-15s %12d bytes  age=%s  %s\n", e.Reason, e.Bytes, e.Age.Round(time.Second), e.Dir)
	}
	verb := "would-remove"
	if rep.Apply {
		verb = "removed"
	}
	fmt.Fprintf(out, "%s: %d bytes\nkept: %d bytes\n", verb, rep.BytesRemoved, rep.BytesKept)
	if !rep.Apply && rep.BytesRemoved > 0 {
		fmt.Fprintln(out, "(re-run with -apply to delete)")
	}
	return nil
}

func runStorageBudget(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cove storage budget <get|set|clear> [args]")
	}
	switch args[0] {
	case "get":
		return runStorageBudgetGet(args[1:], out)
	case "set":
		return runStorageBudgetSet(args[1:], out)
	case "clear":
		return runStorageBudgetClear(args[1:], out)
	default:
		return fmt.Errorf("storage budget: unknown action %q", args[0])
	}
}

func runStorageBudgetGet(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("storage budget get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	human := fs.Bool("human", false, "render a fixed-width table instead of JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove storage budget get [-human]")
	}
	b, err := storagecensus.LoadBudget(coveRoot())
	if err != nil {
		return fmt.Errorf("storage budget get: %w", err)
	}
	if *human {
		if !b.IsSet() {
			_, err := fmt.Fprintln(out, "no budget configured")
			return err
		}
		_, err := fmt.Fprintf(out, "target: %s\nwarn:   %d%% (%s)\nhard:   %d%% (%s)\n",
			formatBytes(b.TargetBytes), b.WarnPct, formatBytes(b.WarnBytes()), b.HardPct, formatBytes(b.HardBytes()))
		return err
	}
	return storagecensus.EncodeBudgetJSON(out, b)
}

func runStorageBudgetSet(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("storage budget set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	target := fs.String("target", "", "soft watermark, e.g. 500GB or 2TB or 1234567 (bytes)")
	warn := fs.Int("warn", 80, "warn tripwire as percent of target (0-100)")
	hard := fs.Int("hard", 95, "hard tripwire as percent of target (0-100)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove storage budget set -target SIZE [-warn PCT] [-hard PCT]")
	}
	if *target == "" {
		return fmt.Errorf("storage budget set: -target is required")
	}
	bytes, err := parseSize(*target)
	if err != nil {
		return fmt.Errorf("storage budget set: %w", err)
	}
	b := storagecensus.Budget{TargetBytes: bytes, WarnPct: *warn, HardPct: *hard}
	if err := storagecensus.SaveBudget(coveRoot(), b); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "storage budget: target=%s warn=%d%% hard=%d%%\n", formatBytes(b.TargetBytes), b.WarnPct, b.HardPct)
	return err
}

func runStorageBudgetClear(args []string, _ io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: cove storage budget clear")
	}
	return storagecensus.ClearBudget(coveRoot())
}

// parseSize accepts a decimal byte count or a SI-suffixed shorthand
// (KB, MB, GB, TB; KiB/MiB/GiB/TiB also accepted for clarity). The
// suffixes are case-insensitive and binary-based (1 KB = 1024 B), since
// every other size in cove is binary-quoted by APFS.
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	var unit int64 = 1
	upper := strings.ToUpper(s)
	switch {
	case strings.HasSuffix(upper, "TIB"), strings.HasSuffix(upper, "TB"):
		unit = 1024 * 1024 * 1024 * 1024
	case strings.HasSuffix(upper, "GIB"), strings.HasSuffix(upper, "GB"):
		unit = 1024 * 1024 * 1024
	case strings.HasSuffix(upper, "MIB"), strings.HasSuffix(upper, "MB"):
		unit = 1024 * 1024
	case strings.HasSuffix(upper, "KIB"), strings.HasSuffix(upper, "KB"):
		unit = 1024
	}
	digits := s
	if unit > 1 {
		// Strip the alphabetic suffix.
		i := len(s)
		for i > 0 && (s[i-1] < '0' || s[i-1] > '9') {
			i--
		}
		digits = strings.TrimSpace(s[:i])
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	if n < 0 {
		return 0, fmt.Errorf("size must be non-negative: %q", s)
	}
	return n * unit, nil
}

func formatBytes(n int64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
		tib = gib * 1024
	)
	switch {
	case n >= tib:
		return fmt.Sprintf("%.1f TB", float64(n)/float64(tib))
	case n >= gib:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func runStorageCensus(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("storage census", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	human := fs.Bool("human", false, "render a fixed-width table instead of JSON")
	topN := fs.Int("top", 10, "number of newest items to surface per category (0 = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove storage census [-human] [-top N]")
	}

	root := coveRoot()
	cats := []storagecensus.Descriptor{
		{Name: "vms", Path: vmconfig.BaseDir()},
		{Name: "images", Path: ImagesBaseDir()},
		{Name: "runs", Path: vmconfig.RunsDir()},
		{Name: "cache", Path: vmconfig.CacheDir()},
		{Name: "build-scratch", Path: defaultBuildScratchRoot()},
		{Name: "store", Path: store.DefaultDir()},
	}

	rep, err := storagecensus.Walk(root, cats, storagecensus.Options{TopN: *topN})
	if err != nil {
		return fmt.Errorf("storage census: %w", err)
	}
	if b, berr := storagecensus.LoadBudget(root); berr == nil && b.IsSet() {
		bb := b
		rep.Budget = &bb
	}
	if *human {
		return storagecensus.RenderHuman(out, rep)
	}
	return storagecensus.EncodeJSON(out, rep)
}

// coveRoot returns the parent of vmconfig.BaseDir(), i.e. ~/.vz/.
func coveRoot() string {
	return filepath.Dir(vmconfig.BaseDir())
}
