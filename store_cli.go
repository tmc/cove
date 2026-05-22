package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/store"
	"github.com/tmc/cove/internal/vmconfig"
)

func handleStoreCommand(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printStoreUsage(os.Stderr)
		if len(args) == 0 {
			return fmt.Errorf("store command required")
		}
		return nil
	}
	switch args[0] {
	case "gc":
		return handleStoreGC(args[1:])
	default:
		return fmt.Errorf("unknown store command %q", args[0])
	}
}

func handleStoreGC(args []string) error {
	fs := flag.NewFlagSet("store gc", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "print candidate deletion totals without deleting blobs")
	fs.Usage = func() { printStoreGCUsage(os.Stderr) }
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove store gc [-dry-run]")
	}
	s := store.New("")
	reachable, err := s.ReachableFromVMs(vmconfig.BaseDir())
	if err != nil {
		return err
	}
	buildReachable, err := s.ReachableFromBuildCache()
	if err != nil {
		return err
	}
	for digest := range buildReachable {
		reachable[digest] = true
	}
	res, err := s.GCWithOptions(reachable, store.GCOptions{
		Grace:  store.GCGrace,
		DryRun: *dryRun,
	})
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Printf("Store GC dry run: would delete %d blob(s), reclaim %s, keep %d young blob(s)\n", res.Deleted, bytefmt.Size(res.Reclaimed), res.KeptYoung)
		return nil
	}
	fmt.Printf("Store GC complete: deleted %d blob(s), reclaimed %s, kept %d young blob(s)\n", res.Deleted, bytefmt.Size(res.Reclaimed), res.KeptYoung)
	return nil
}

func printStoreUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove store <command>

Manage the local content-addressed OCI blob store.

Commands:
  gc    Remove unreferenced blobs older than the GC grace window`)
}

func printStoreGCUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove store gc [-dry-run]

Garbage collect ~/.vz/store. GC takes an exclusive store lock and keeps blobs
modified within the last hour so concurrent or recently interrupted pulls are
not collected.

Flags:
  -dry-run    Print candidate deletion totals without deleting blobs`)
}
