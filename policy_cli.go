package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmpolicy"
)

func handlePolicyCommand(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printPolicyUsage(os.Stdout)
		return nil
	}
	vmName := strings.TrimSpace(args[0])
	if vmName == "" {
		return fmt.Errorf("policy requires a vm name")
	}
	vmDir := vmconfig.Path(vmName)
	if len(args) == 1 {
		printPolicyUsage(os.Stderr)
		return fmt.Errorf("policy requires a command")
	}
	switch args[1] {
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("usage: cove policy <vm> show")
		}
		return printPolicy(vmName, vmDir)
	case "clear":
		if len(args) != 2 {
			return fmt.Errorf("usage: cove policy <vm> clear")
		}
		if err := vmpolicy.Clear(vmDir); err != nil {
			return err
		}
		fmt.Printf("Cleared policy for %s\n", vmName)
		return nil
	case "set":
		return setPolicyField(vmName, vmDir, args[2:])
	case "idle", "max-age", "run-budget":
		return setPolicyField(vmName, vmDir, args[1:])
	default:
		printPolicyUsage(os.Stderr)
		return fmt.Errorf("unknown policy command: %s", args[1])
	}
}

func printPolicyUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove policy <vm> <command> [args]

Commands:
  show                     Show the saved policy for <vm>
  clear                    Remove policy.json for <vm>
  idle <duration>          Set idle timeout (for example 30m)
  max-age <duration>       Set maximum VM age (for example 24h)
  run-budget <count>       Set allowed guest exec count
  set <field> <value>      Alias for the forms above

Examples:
  cove policy work-vm show
  cove policy work-vm idle 30m
  cove policy work-vm max-age 24h
  cove policy work-vm run-budget 100
  cove policy work-vm clear`)
}

func printPolicy(vmName, vmDir string) error {
	p, err := vmpolicy.Load(vmDir)
	if err != nil {
		return err
	}
	fmt.Printf("VM: %s\n", vmName)
	fmt.Printf("Policy file: %s\n", vmpolicy.Path(vmDir))
	if p.Empty() {
		fmt.Println("Policy: none")
		return nil
	}
	fmt.Printf("Idle timeout: %s\n", durationOrDash(p.IdleTimeout))
	fmt.Printf("Max age:      %s\n", durationOrDash(p.MaxAge))
	if p.RunBudget > 0 {
		fmt.Printf("Run budget:   %d\n", p.RunBudget)
	} else {
		fmt.Printf("Run budget:   -\n")
	}
	return nil
}

func setPolicyField(vmName, vmDir string, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: cove policy <vm> [set] idle <duration>|max-age <duration>|run-budget <count>")
	}
	field, raw := strings.TrimSpace(args[0]), strings.TrimSpace(args[1])
	cur, err := vmpolicy.Load(vmDir)
	if err != nil {
		return err
	}
	var update vmpolicy.Policy
	switch field {
	case "idle":
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("parse idle timeout: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("idle timeout must be greater than zero")
		}
		update.IdleTimeout = d
	case "max-age":
		d, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("parse max age: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("max age must be greater than zero")
		}
		update.MaxAge = d
	case "run-budget":
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("parse run budget: %w", err)
		}
		if n <= 0 {
			return fmt.Errorf("run budget must be greater than zero")
		}
		update.RunBudget = n
	default:
		return fmt.Errorf("unknown policy field %q", field)
	}

	next := cur.Merge(update)
	if next.Empty() {
		return fmt.Errorf("policy update produced an empty policy")
	}
	if err := vmpolicy.Save(vmDir, next); err != nil {
		return err
	}
	fmt.Printf("Saved policy for %s\n", vmName)
	fmt.Printf("Policy file: %s\n", vmpolicy.Path(vmDir))
	return nil
}

func durationOrDash(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.String()
}
