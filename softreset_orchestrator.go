package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/softreset"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

type softresetRunAllOptions struct {
	VM      string
	JSON    bool
	Filter  []string
	Timeout time.Duration
}

type SoftResetReport struct {
	VM             string                 `json:"vm"`
	Probes         []SoftResetProbeReport `json:"probes"`
	RuntimeSeconds float64                `json:"runtime_s"`
	IsolationScore int                    `json:"isolation_score"`
}

type SoftResetProbeReport struct {
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	RuntimeSeconds float64  `json:"runtime_s"`
	Evidence       []string `json:"evidence,omitempty"`
	Error          string   `json:"error,omitempty"`
}

var softresetRunProbe = runSoftresetProbe

func parseSoftresetRunAllArgs(args []string) (softresetRunAllOptions, error) {
	fs := flag.NewFlagSet("softreset run-all", flag.ContinueOnError)
	fs.SetOutput(flag.CommandLine.Output())
	jsonOut := fs.Bool("json", false, "write JSON report")
	filter := fs.String("filter", "", "comma-separated probes to run: filesystem,process,network,memory")
	timeout := fs.Duration("timeout", 60*time.Second, "whole-run timeout")
	if err := fs.Parse(moveSoftresetRunAllFlagsFirst(args)); err != nil {
		return softresetRunAllOptions{}, err
	}
	if fs.NArg() != 1 {
		return softresetRunAllOptions{}, fmt.Errorf("usage: cove softreset run-all <vm> [--json] [--filter=filesystem,network,memory,process] [--timeout=60s]")
	}
	names := append([]string(nil), softresetProbeNames...)
	if strings.TrimSpace(*filter) != "" {
		var err error
		names, err = parseSoftresetProbeList(*filter)
		if err != nil {
			return softresetRunAllOptions{}, err
		}
		names = orderSoftresetProbes(names)
	}
	if *timeout <= 0 {
		return softresetRunAllOptions{}, fmt.Errorf("--timeout must be positive")
	}
	return softresetRunAllOptions{VM: fs.Arg(0), JSON: *jsonOut, Filter: names, Timeout: *timeout}, nil
}

func moveSoftresetRunAllFlagsFirst(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			flags = append(flags, arg)
		case (arg == "--filter" || arg == "--timeout") && i+1 < len(args):
			flags = append(flags, arg, args[i+1])
			i++
		case strings.HasPrefix(arg, "--filter="), strings.HasPrefix(arg, "--timeout="):
			flags = append(flags, arg)
		default:
			rest = append(rest, arg)
		}
	}
	return append(flags, rest...)
}

func SoftResetRunAll(ctx context.Context, vmRef string, opts softresetRunAllOptions) (*SoftResetReport, error) {
	dir, ok := vmconfig.ExistingPath(vmRef)
	if !ok {
		return nil, fmt.Errorf("no VM named %q under %s", vmRef, vmconfig.BaseDir())
	}
	names := opts.Filter
	if len(names) == 0 {
		names = append([]string(nil), softresetProbeNames...)
	}
	names = orderSoftresetProbes(names)
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	report := &SoftResetReport{VM: vmRef}
	for _, name := range names {
		probeStart := time.Now()
		result, err := softresetRunProbe(ctx, name, dir)
		item := SoftResetProbeReport{
			Name:           name,
			RuntimeSeconds: secondsSince(probeStart),
		}
		if err != nil {
			item.Status = "fail"
			item.Error = err.Error()
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				item.Status = "timeout"
			}
			report.Probes = append(report.Probes, item)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		item.Name = result.Probe
		item.Status = string(result.Status)
		item.Evidence = result.Evidence
		report.Probes = append(report.Probes, item)
		if ctx.Err() != nil {
			break
		}
	}
	report.RuntimeSeconds = secondsSince(start)
	report.IsolationScore = softresetIsolationScore(report.Probes)
	return report, nil
}

func orderSoftresetProbes(names []string) []string {
	want := make(map[string]bool, len(names))
	for _, name := range names {
		want[normalizeSoftresetProbeName(name)] = true
	}
	var out []string
	for _, name := range softresetProbeNames {
		if want[name] {
			out = append(out, name)
		}
	}
	return out
}

func softresetIsolationScore(probes []SoftResetProbeReport) int {
	if len(probes) == 0 {
		return 0
	}
	var total float64
	for _, p := range probes {
		switch p.Status {
		case string(softreset.StatusPass):
			total += 100
		case string(softreset.StatusLimit):
			total += 50
		}
	}
	return int(math.Round(total / float64(len(probes))))
}

func secondsSince(t time.Time) float64 {
	return math.Round(time.Since(t).Seconds()*1000) / 1000
}

func softresetScratchRoot(vmDir, probe string) string {
	return filepath.Join(tempDir(), "cove-softreset-"+filepath.Base(vmDir)+"-"+probe)
}

var tempDir = os.TempDir
