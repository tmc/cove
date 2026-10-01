package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tmc/cove/internal/runs"
	"github.com/tmc/cove/internal/vmconfig"
)

func runRunsInspect(env commandEnv, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		fmt.Fprintln(env.Stdout, "Usage: cove runs inspect <run-id-prefix> [--json|--html]\nReads recorded history; does not replay or execute a task.")
		return nil
	}
	format := "text"
	prefix := ""
	for _, arg := range args {
		switch arg {
		case "--json", "--html":
			if format != "text" {
				return fmt.Errorf("runs inspect: choose one output format")
			}
			format = strings.TrimPrefix(arg, "--")
		default:
			if prefix != "" || strings.HasPrefix(arg, "-") {
				return fmt.Errorf("usage: cove runs inspect <run-id-prefix> [--json|--html]")
			}
			prefix = arg
		}
	}
	if prefix == "" {
		return fmt.Errorf("runs inspect: run prefix is required")
	}
	show, err := runs.LoadShow(vmconfig.RunsDir(), prefix)
	if err != nil {
		return err
	}
	if show.TaskRecord == nil {
		return fmt.Errorf("runs inspect: task evidence unavailable; use runs show for legacy metrics")
	}
	return runs.RenderInspection(env.Stdout, runs.InspectRecord(*show.TaskRecord), format)
}

func runRunsCompare(env commandEnv, args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		fmt.Fprintln(env.Stdout, "Usage: cove runs compare <left-run-prefix> <right-run-prefix> [--json]\nCompares recorded verified task inputs; never ranks incomparable runs.")
		return nil
	}
	jsonOut := false
	var prefixes []string
	for _, arg := range args {
		if arg == "--json" {
			jsonOut = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return fmt.Errorf("runs compare: unknown option %q", arg)
		}
		prefixes = append(prefixes, arg)
	}
	if len(prefixes) != 2 {
		return fmt.Errorf("usage: cove runs compare <left-run-prefix> <right-run-prefix> [--json]")
	}
	var records [2]runs.Record
	for i, prefix := range prefixes {
		show, err := runs.LoadShow(vmconfig.RunsDir(), prefix)
		if err != nil {
			return err
		}
		if show.TaskRecord == nil {
			return fmt.Errorf("runs compare: task provenance unavailable for %s", prefix)
		}
		records[i] = *show.TaskRecord
	}
	comparison := runs.CompareRecords(records[0], records[1])
	if jsonOut {
		encoder := json.NewEncoder(env.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(comparison)
	}
	if _, err := fmt.Fprintf(env.Stdout, "Comparable: %t\n", comparison.Comparable); err != nil {
		return err
	}
	for _, reason := range comparison.Reasons {
		if _, err := fmt.Fprintln(env.Stdout, "  "+reason); err != nil {
			return err
		}
	}
	if err := runs.RenderInspection(env.Stdout, comparison.Left, "text"); err != nil {
		return err
	}
	return runs.RenderInspection(env.Stdout, comparison.Right, "text")
}
