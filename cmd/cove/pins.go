package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/tmc/cove/internal/storagepins"
)

// handlePinCommand implements `cove pin <object>`.
func handlePinCommand(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("pin", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: cove pin <object>\n  object is one of vm:<name>, image:<ref>, run:<id>, cache:<sha>")
	}
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cove pin <object>\n  object is one of vm:<name>, image:<ref>, run:<id>, cache:<sha>")
	}
	cat, id, err := storagepins.ParseRef(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("pin: %w", err)
	}
	root := coveRoot()
	f, err := storagepins.Load(root)
	if err != nil {
		return fmt.Errorf("pin: %w", err)
	}
	if err := f.Add(cat, id, time.Now().UTC()); err != nil {
		return fmt.Errorf("pin: %w", err)
	}
	if err := storagepins.Save(root, f); err != nil {
		return fmt.Errorf("pin: %w", err)
	}
	fmt.Fprintf(env.Stdout, "pinned %s:%s\n", cat, id)
	return nil
}

// handleUnpinCommand implements `cove unpin <object>`.
func handleUnpinCommand(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("unpin", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: cove unpin <object>\n  object is one of vm:<name>, image:<ref>, run:<id>, cache:<sha>")
	}
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: cove unpin <object>\n  object is one of vm:<name>, image:<ref>, run:<id>, cache:<sha>")
	}
	cat, id, err := storagepins.ParseRef(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("unpin: %w", err)
	}
	root := coveRoot()
	f, err := storagepins.Load(root)
	if err != nil {
		return fmt.Errorf("unpin: %w", err)
	}
	removed, err := f.Remove(cat, id)
	if err != nil {
		return fmt.Errorf("unpin: %w", err)
	}
	if !removed {
		fmt.Fprintf(env.Stdout, "not pinned: %s:%s\n", cat, id)
		return nil
	}
	if err := storagepins.Save(root, f); err != nil {
		return fmt.Errorf("unpin: %w", err)
	}
	fmt.Fprintf(env.Stdout, "unpinned %s:%s\n", cat, id)
	return nil
}

// handlePinsCommand implements `cove pins list` and the `cove pins`
// dispatcher umbrella.
func handlePinsCommand(env commandEnv, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cove pins <subcommand>\n  list   List pinned objects")
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprintln(env.Stdout, "Usage: cove pins <subcommand>\n  list   List pinned objects")
		return nil
	case "list":
		return runPinsList(env, args[1:])
	default:
		return fmt.Errorf("pins: unknown subcommand %q", args[0])
	}
}

func runPinsList(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("pins list", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	asJSON := fs.Bool("json", false, "render JSON instead of a fixed-width table")
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove pins list [-json]")
	}
	f, err := storagepins.Load(coveRoot())
	if err != nil {
		return fmt.Errorf("pins list: %w", err)
	}
	pins := f.List()
	if *asJSON {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(pins)
	}
	if len(pins) == 0 {
		_, err := fmt.Fprintln(env.Stdout, "no pins")
		return err
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "REF\tADDED"); err != nil {
		return err
	}
	for _, p := range pins {
		if _, err := fmt.Fprintf(tw, "%s\t%s\n", p.Ref(), p.AddedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tw.Flush()
}
