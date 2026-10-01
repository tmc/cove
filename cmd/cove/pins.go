package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/tmc/cove/internal/storagepins"
)

// handlePinCommand implements `cove pin <object>`.
func handlePinCommand(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("pin", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() {
		printPinUsage(fs.Output())
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
	if err := storagepins.Update(root, func(f *storagepins.File) (bool, error) {
		if err := f.Add(cat, id, time.Now().UTC()); err != nil {
			return false, err
		}
		return true, nil
	}); err != nil {
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
		printUnpinUsage(fs.Output())
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
	removed := false
	protected := false
	if err := storagepins.Update(root, func(f *storagepins.File) (bool, error) {
		var err error
		removed, err = f.Remove(cat, id)
		for _, pin := range f.TaskPins() {
			if pin.Category == cat && pin.ID == id {
				protected = true
			}
		}
		return removed, err
	}); err != nil {
		return fmt.Errorf("unpin: %w", err)
	}
	if protected {
		if removed {
			fmt.Fprintf(env.Stdout, "removed operator pin %s:%s; task protection remains (cove pins list)\n", cat, id)
		} else {
			fmt.Fprintf(env.Stdout, "no operator pin: %s:%s; task protection remains (cove pins list)\n", cat, id)
		}
		return nil
	}
	if !removed {
		fmt.Fprintf(env.Stdout, "not pinned: %s:%s\n", cat, id)
		return nil
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
		printPinsUsage(env.Stdout)
		return nil
	case "list":
		return runPinsList(env, args[1:])
	default:
		return fmt.Errorf("pins: unknown subcommand %q", args[0])
	}
}

func printPinUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: cove pin <object>\n  object is one of vm:<name>, image:<ref>, run:<id>, cache:<sha>")
}

func printUnpinUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: cove unpin <object>\n  object is one of vm:<name>, image:<ref>, run:<id>, cache:<sha>")
}

func printPinsUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: cove pins <subcommand>\n  list   List pinned objects")
}

func runPinsList(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("pins list", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `Usage: cove pins list [-json]

List pinned objects that storage budget eviction skips.

Flags:`)
		fs.PrintDefaults()
	}
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
	pins := []pinsListRow{}
	for _, pin := range f.List() {
		pins = append(pins, pinsListRow{Pin: pin, Source: "operator"})
	}
	for _, pin := range f.TaskPins() {
		owner, identity := pin.Owner, pin.Identity
		pins = append(pins, pinsListRow{Pin: storagepins.Pin{Category: pin.Category, ID: pin.ID, AddedAt: pin.AddedAt}, Source: "task", Owner: &owner, Identity: &identity})
	}
	sort.Slice(pins, func(i, j int) bool {
		if pins[i].Ref() != pins[j].Ref() {
			return pins[i].Ref() < pins[j].Ref()
		}
		if pins[i].Source != pins[j].Source {
			return pins[i].Source < pins[j].Source
		}
		if pins[i].Owner == nil || pins[j].Owner == nil {
			return false
		}
		return pins[i].Owner.Generation < pins[j].Owner.Generation
	})
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
	if _, err := fmt.Fprintln(tw, "REF\tSOURCE\tADDED\tRUN\tGENERATION\tPID"); err != nil {
		return err
	}
	for _, p := range pins {
		run, generation, pid := "-", "-", "-"
		if p.Owner != nil {
			run = p.Owner.RunID
			generation = p.Owner.Generation
			pid = fmt.Sprint(p.Owner.PID)
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", p.Ref(), p.Source, p.AddedAt.Format(time.RFC3339), run, generation, pid); err != nil {
			return err
		}
	}
	return tw.Flush()
}

type pinsListRow struct {
	storagepins.Pin
	Source   string                         `json:"source"`
	Owner    *storagepins.TaskOwner         `json:"owner,omitempty"`
	Identity *storagepins.DirectoryIdentity `json:"identity,omitempty"`
}
