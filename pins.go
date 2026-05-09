package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/tmc/vz-macos/internal/storagepins"
)

// handlePinCommand implements `cove pin <object>`.
func handlePinCommand(args []string) error {
	fs := flag.NewFlagSet("pin", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
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
	fmt.Printf("pinned %s:%s\n", cat, id)
	return nil
}

// handleUnpinCommand implements `cove unpin <object>`.
func handleUnpinCommand(args []string) error {
	fs := flag.NewFlagSet("unpin", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
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
		fmt.Printf("not pinned: %s:%s\n", cat, id)
		return nil
	}
	if err := storagepins.Save(root, f); err != nil {
		return fmt.Errorf("unpin: %w", err)
	}
	fmt.Printf("unpinned %s:%s\n", cat, id)
	return nil
}

// handlePinsCommand implements `cove pins list` and the `cove pins`
// dispatcher umbrella.
func handlePinsCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cove pins <subcommand>\n  list   List pinned objects")
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprintln(os.Stdout, "Usage: cove pins <subcommand>\n  list   List pinned objects")
		return nil
	case "list":
		return runPinsList(args[1:], os.Stdout)
	default:
		return fmt.Errorf("pins: unknown subcommand %q", args[0])
	}
}

func runPinsList(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("pins list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "render JSON instead of a fixed-width table")
	if err := fs.Parse(args); err != nil {
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
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(pins)
	}
	if len(pins) == 0 {
		_, err := fmt.Fprintln(out, "no pins")
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
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
