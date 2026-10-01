package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/tmc/cove/guest"
)

func runUICommand(env commandEnv, _ string, args []string) int {
	return commandError(env, handleUICommand(env.WithDefaultIO(), args))
}

func printUIUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove ui status|inspect|find -vm NAME [flags]

Read guest macOS accessibility through the logged-in user agent.
Values and editable/static text are omitted. No permissions are auto-granted.

Flags:
  -pid N                scope inspection to one guest application process
  -role ROLE            exact find role, for example AXButton
  -identifier ID        exact find accessibility identifier
  -label LABEL          exact find control label
  -depth N              maximum tree depth (default 5, maximum 16)
  -nodes N              maximum nodes (default 256, maximum 1024)
  -bytes N              maximum reply bytes (default 65536, maximum 262144)
  -timeout DURATION     read budget (default 2s, maximum 10s)
  -generation ID        refuse if the observed session generation changed
  -json                 emit structured status/observation

Denied, unavailable, unsupported, stale and ambiguous results are explicit.
Inspection never invokes a coordinate fallback or performs an action.`)
}

func handleUICommand(env commandEnv, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printUIUsage(env.Stdout)
		return nil
	}
	action := args[0]
	if action != "status" && action != "inspect" && action != "find" {
		return fmt.Errorf("unknown ui command: %s", action)
	}
	fs := flag.NewFlagSet("ui "+action, flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { printUIUsage(fs.Output()) }
	name := fs.String("vm", vmName, "guest VM name")
	pid := fs.Int("pid", 0, "guest application pid")
	role := fs.String("role", "", "exact role")
	identifier := fs.String("identifier", "", "exact identifier")
	label := fs.String("label", "", "exact label")
	depth := fs.Uint("depth", 5, "maximum tree depth")
	nodes := fs.Uint("nodes", 256, "maximum nodes")
	bytes := fs.Uint("bytes", 65536, "maximum reply bytes")
	timeout := fs.Duration("timeout", 2*time.Second, "read budget")
	generation := fs.String("generation", "", "expected session generation")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected ui arguments")
	}
	if *name == "" {
		return fmt.Errorf("vm name is required")
	}
	if *pid < 0 || int64(*pid) > int64(1<<31-1) || action != "status" && *pid == 0 {
		return fmt.Errorf("positive application pid is required")
	}
	if *depth > 16 || *nodes > 1024 || *bytes < 4096 || *bytes > 262144 || *timeout < 100*time.Millisecond || *timeout > 10*time.Second {
		return fmt.Errorf("ui bounds exceeded")
	}
	if action == "find" && *role == "" && *identifier == "" && *label == "" {
		return fmt.Errorf("find requires a role, identifier or label")
	}
	directory, err := requireExistingVMDir("ui", *name)
	if err != nil {
		return err
	}
	session, err := guest.NewSession(GetControlSocketPathForVM(directory))
	if err != nil {
		return err
	}
	defer session.Close()
	ctx, cancel := context.WithTimeout(context.Background(), *timeout+time.Second)
	defer cancel()
	query := guest.Query{PID: int32(*pid), MaxDepth: uint32(*depth), MaxNodes: uint32(*nodes), MaxBytes: uint32(*bytes), Timeout: *timeout, Role: *role, Identifier: *identifier, Label: *label, ExpectedGeneration: *generation}
	var observation guest.Observation
	if action == "status" {
		observation.Status, err = session.Ready(ctx)
	} else if action == "inspect" {
		observation, err = session.Inspect(ctx, query)
	} else {
		observation, err = session.Find(ctx, query)
	}
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(env.Stdout).Encode(observation)
	}
	status := observation.Status
	fmt.Fprintf(env.Stdout, "Guest UI: %s backend=%s session=%s permission=%s\n", safeUIText(status.State), safeUIText(status.Backend), safeUIText(status.SessionState), safeUIText(status.PermissionState))
	fmt.Fprintf(env.Stdout, "Generation: %s\n", safeUIText(status.Generation))
	if status.Reason != "" {
		fmt.Fprintln(env.Stdout, safeUIText(status.Reason))
	}
	if observation.ID != "" {
		fmt.Fprintf(env.Stdout, "Observation: %s truncated=%t %s matches=%s\n", safeUIText(observation.ID), observation.Truncated, safeUIText(observation.TruncationReason), safeUIText(observation.MatchState))
	}
	for _, node := range observation.Nodes {
		fmt.Fprintf(env.Stdout, "  %s %s id=%s label=%s\n", safeUIText(node.Handle), safeUIText(node.Role), safeUIText(node.Identifier), safeUIText(node.Label))
	}
	return nil
}

func safeUIText(text string) string { quoted := strconv.Quote(text); return quoted[1 : len(quoted)-1] }
