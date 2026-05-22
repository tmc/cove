package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tmc/cove/internal/softreset"
	"github.com/tmc/cove/internal/vmconfig"
)

var softresetProbeNames = []string{"filesystem", "process", "network", "memory"}

type softresetProbeOptions struct {
	VM     string
	All    bool
	Probes []string
}

func softresetCommand(args []string) error {
	if len(args) == 0 {
		printSoftresetUsage(os.Stderr)
		return fmt.Errorf("softreset: command required")
	}
	switch args[0] {
	case "-h", "--help", "help":
		printSoftresetUsage(os.Stdout)
		return nil
	case "probe":
		if len(args) > 1 && isHelpArg(args[1]) {
			fmt.Fprintln(os.Stdout, "Usage: cove softreset probe <vm> [--all|--probes filesystem,process,network,memory]")
			return nil
		}
		opts, err := parseSoftresetProbeArgs(args[1:])
		if err != nil {
			return err
		}
		results, err := runSoftresetProbes(context.Background(), opts)
		if err != nil {
			return err
		}
		return writeSoftresetProbeSummary(os.Stdout, opts, results)
	case "run-all":
		if len(args) > 1 && isHelpArg(args[1]) {
			fmt.Fprintln(os.Stdout, "Usage: cove softreset run-all <vm> [--probes filesystem,process,network,memory]")
			return nil
		}
		opts, err := parseSoftresetRunAllArgs(args[1:])
		if err != nil {
			return err
		}
		report, err := SoftResetRunAll(context.Background(), opts.VM, opts)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	default:
		return fmt.Errorf("unknown softreset command %q", args[0])
	}
}

func printSoftresetUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove softreset <command> <vm> [options]

Commands:
  probe      Run soft-reset probes and print a summary
  run-all    Run probes and apply the soft-reset workflow`)
}

func parseSoftresetProbeArgs(args []string) (softresetProbeOptions, error) {
	fs := flag.NewFlagSet("softreset probe", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	all := fs.Bool("all", false, "run all probes")
	probes := fs.String("probes", "", "comma-separated probes to run")
	if err := fs.Parse(moveSoftresetProbeFlagsFirst(args)); err != nil {
		return softresetProbeOptions{}, err
	}
	if fs.NArg() != 1 {
		return softresetProbeOptions{}, fmt.Errorf("usage: cove softreset probe <vm> [--all|--probes filesystem,process,network,memory]")
	}
	if *all && strings.TrimSpace(*probes) != "" {
		return softresetProbeOptions{}, fmt.Errorf("--all and --probes are mutually exclusive")
	}
	names := append([]string(nil), softresetProbeNames...)
	if strings.TrimSpace(*probes) != "" {
		var err error
		names, err = parseSoftresetProbeList(*probes)
		if err != nil {
			return softresetProbeOptions{}, err
		}
	}
	return softresetProbeOptions{VM: fs.Arg(0), All: *all || strings.TrimSpace(*probes) == "", Probes: names}, nil
}

func moveSoftresetProbeFlagsFirst(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--all":
			flags = append(flags, arg)
		case arg == "--probes" && i+1 < len(args):
			flags = append(flags, arg, args[i+1])
			i++
		case strings.HasPrefix(arg, "--probes="):
			flags = append(flags, arg)
		default:
			rest = append(rest, arg)
		}
	}
	return append(flags, rest...)
}

func parseSoftresetProbeList(value string) ([]string, error) {
	valid := make(map[string]bool, len(softresetProbeNames))
	for _, name := range softresetProbeNames {
		valid[name] = true
	}
	seen := make(map[string]bool)
	var out []string
	for _, part := range strings.Split(value, ",") {
		name := normalizeSoftresetProbeName(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if !valid[name] {
			return nil, fmt.Errorf("unknown softreset probe %q", name)
		}
		if !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--probes must name at least one probe")
	}
	return out, nil
}

func normalizeSoftresetProbeName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "fs":
		return "filesystem"
	case "proc":
		return "process"
	case "net":
		return "network"
	case "mem":
		return "memory"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

func runSoftresetProbes(ctx context.Context, opts softresetProbeOptions) ([]softreset.Result, error) {
	dir, ok := vmconfig.ExistingPath(opts.VM)
	if !ok {
		return nil, fmt.Errorf("no VM named %q under %s", opts.VM, vmconfig.BaseDir())
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	results := make([]softreset.Result, 0, len(opts.Probes))
	for _, name := range opts.Probes {
		result, err := runSoftresetProbe(ctx, name, dir)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func runSoftresetProbe(ctx context.Context, name, vmDir string) (softreset.Result, error) {
	switch name {
	case "filesystem":
		root := softresetScratchRoot(vmDir, "filesystem")
		return (softreset.FilesystemAttributeProbe{Root: root}).Run(ctx)
	case "process":
		return (softreset.ProcessTableProbe{}).Run(ctx)
	case "network":
		return (softreset.NetworkProbe{}).Run(ctx)
	case "memory":
		return (softreset.MemoryProbe{}).Run(ctx)
	default:
		return softreset.Result{}, fmt.Errorf("unknown softreset probe %q", name)
	}
}

func writeSoftresetProbeSummary(w io.Writer, opts softresetProbeOptions, results []softreset.Result) error {
	counts := map[softreset.Status]int{
		softreset.StatusPass:  0,
		softreset.StatusFail:  0,
		softreset.StatusLimit: 0,
	}
	for _, r := range results {
		counts[r.Status]++
	}
	fmt.Fprintf(w, "Soft reset probe summary for %s\n\n", opts.VM)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "Probe\tStatus\tEvidence")
	fmt.Fprintln(tw, "-----\t------\t--------")
	for _, r := range results {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Probe, r.Status, strings.Join(r.Evidence, "; "))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nPass: %d\nFail: %d\nLimit: %d\n", counts[softreset.StatusPass], counts[softreset.StatusFail], counts[softreset.StatusLimit])
	return nil
}
