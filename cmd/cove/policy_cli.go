package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmpolicy"
)

func handlePolicyCommand(env commandEnv, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printPolicyUsage(env.Stdout)
		return nil
	}
	vmName := strings.TrimSpace(args[0])
	if vmName == "" {
		return fmt.Errorf("policy requires a vm name")
	}
	vmDir := vmconfig.Path(vmName)
	if len(args) == 1 {
		printPolicyUsage(env.Stderr)
		return fmt.Errorf("policy requires a command")
	}
	switch args[1] {
	case "show":
		if len(args) > 2 && isHelpArg(args[2]) {
			fmt.Fprintln(env.Stdout, "Usage: cove policy <vm> show")
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("usage: cove policy <vm> show")
		}
		return printPolicy(env.Stdout, vmName, vmDir)
	case "clear":
		if len(args) > 2 && isHelpArg(args[2]) {
			fmt.Fprintln(env.Stdout, "Usage: cove policy <vm> clear")
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("usage: cove policy <vm> clear")
		}
		if err := vmpolicy.Clear(vmDir); err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "Cleared policy for %s\n", vmName)
		return nil
	case "set":
		if len(args) > 2 && isHelpArg(args[2]) {
			fmt.Fprintln(env.Stdout, "Usage: cove policy <vm> set idle=<duration> max-age=<duration> run-budget=<count>")
			return nil
		}
		return setPolicyField(env.Stdout, vmName, vmDir, args[2:])
	case "idle", "max-age", "run-budget":
		if len(args) > 2 && isHelpArg(args[2]) {
			fmt.Fprintf(env.Stdout, "Usage: cove policy <vm> %s <value>\n", args[1])
			return nil
		}
		return setPolicyField(env.Stdout, vmName, vmDir, args[1:])
	default:
		printPolicyUsage(env.Stderr)
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
  set <field>=<value>...   Set one or more policy fields

Examples:
  cove policy work-vm show
  cove policy work-vm idle 30m
  cove policy work-vm max-age 24h
  cove policy work-vm run-budget 100
  cove policy work-vm set idle=30m max-age=24h run-budget=100
  cove policy work-vm clear`)
}

func printPolicy(w io.Writer, vmName, vmDir string) error {
	p, err := vmpolicy.Load(vmDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "VM: %s\n", vmName)
	fmt.Fprintf(w, "Policy file: %s\n", vmpolicy.Path(vmDir))
	if p.Empty() {
		fmt.Fprintln(w, "Policy: none")
		return nil
	}
	fmt.Fprintf(w, "Idle timeout: %s\n", durationOrDash(p.IdleTimeout))
	fmt.Fprintf(w, "Max age:      %s\n", durationOrDash(p.MaxAge))
	if p.RunBudget > 0 {
		fmt.Fprintf(w, "Run budget:   %d\n", p.RunBudget)
	} else {
		fmt.Fprintf(w, "Run budget:   -\n")
	}
	return nil
}

func setPolicyField(w io.Writer, vmName, vmDir string, args []string) error {
	cur, err := vmpolicy.Load(vmDir)
	if err != nil {
		return err
	}
	update, err := parsePolicyUpdate(args)
	if err != nil {
		return err
	}

	next := cur.Merge(update)
	if next.Empty() {
		return fmt.Errorf("policy update produced an empty policy")
	}
	if err := vmpolicy.Save(vmDir, next); err != nil {
		return err
	}
	fmt.Fprintf(w, "Saved policy for %s\n", vmName)
	fmt.Fprintf(w, "Policy file: %s\n", vmpolicy.Path(vmDir))
	return nil
}

func parsePolicyUpdate(args []string) (vmpolicy.Policy, error) {
	fields, err := policyUpdateFields(args)
	if err != nil {
		return vmpolicy.Policy{}, err
	}
	var update vmpolicy.Policy
	for _, field := range fields {
		if err := applyPolicyField(&update, field.name, field.value); err != nil {
			return vmpolicy.Policy{}, err
		}
	}
	return update, nil
}

type policyUpdateField struct {
	name  string
	value string
}

func policyUpdateFields(args []string) ([]policyUpdateField, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("usage: cove policy <vm> [set] idle <duration>|max-age <duration>|run-budget <count>")
	}
	var fields []policyUpdateField
	if policyArgsUseEquals(args) {
		for _, arg := range args {
			name, value, ok := strings.Cut(arg, "=")
			if !ok {
				return nil, fmt.Errorf("usage: cove policy <vm> set idle=<duration> [max-age=<duration>] [run-budget=<count>]")
			}
			fields = append(fields, policyUpdateField{name: strings.TrimSpace(name), value: strings.TrimSpace(value)})
		}
		return fields, nil
	}
	if len(args)%2 != 0 {
		return nil, fmt.Errorf("usage: cove policy <vm> [set] idle <duration>|max-age <duration>|run-budget <count>")
	}
	for i := 0; i < len(args); i += 2 {
		fields = append(fields, policyUpdateField{name: strings.TrimSpace(args[i]), value: strings.TrimSpace(args[i+1])})
	}
	return fields, nil
}

func policyArgsUseEquals(args []string) bool {
	for _, arg := range args {
		if strings.Contains(arg, "=") {
			return true
		}
	}
	return false
}

func applyPolicyField(update *vmpolicy.Policy, field, raw string) error {
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
	return nil
}

func durationOrDash(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	return d.String()
}
