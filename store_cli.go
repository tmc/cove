package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tmc/vz-macos/internal/bytefmt"
	"github.com/tmc/vz-macos/internal/store"
	"github.com/tmc/vz-macos/internal/vmconfig"
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
	fs.Usage = func() { printStoreGCUsage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove store gc")
	}
	s := store.New("")
	reachable, err := s.ReachableFromVMs(vmconfig.BaseDir())
	if err != nil {
		return err
	}
	res, err := s.GC(reachable, store.GCGrace)
	if err != nil {
		return err
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
	fmt.Fprintln(w, `Usage: cove store gc

Garbage collect ~/.vz/store. GC takes an exclusive store lock and keeps blobs
modified within the last hour so concurrent or recently interrupted pulls are
not collected.`)
}
