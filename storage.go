package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tmc/vz-macos/internal/storagecensus"
	"github.com/tmc/vz-macos/internal/store"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

func handleStorageCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cove storage <subcommand> [args]\n  census    Walk ~/.vz/ and report per-category disk usage\n  prune     Remove safe-to-delete cruft (build-scratch only for now)")
	}
	switch args[0] {
	case "census":
		return runStorageCensus(args[1:], os.Stdout)
	case "prune":
		return runStoragePrune(args[1:], os.Stdout)
	default:
		return fmt.Errorf("storage: unknown subcommand %q", args[0])
	}
}

func runStoragePrune(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cove storage prune <category> [flags]\n  build-scratch    Remove ~/.vz/build-scratch/<id> dirs older than -older-than")
	}
	switch args[0] {
	case "build-scratch":
		return runStoragePruneBuildScratch(args[1:], out)
	default:
		return fmt.Errorf("storage prune: unknown category %q", args[0])
	}
}

func runStoragePruneBuildScratch(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("storage prune build-scratch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	olderThan := fs.Duration("older-than", 7*24*time.Hour, "delete build-scratch dirs older than this duration")
	apply := fs.Bool("apply", false, "actually delete; default is dry-run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove storage prune build-scratch [-older-than DUR] [-apply]")
	}
	rep, err := pruneBuildScratch(defaultBuildScratchRoot(), *olderThan, *apply, nil, nil)
	if err != nil {
		return fmt.Errorf("storage prune build-scratch: %w", err)
	}
	return writePruneBuildScratchReport(out, rep)
}

func writePruneBuildScratchReport(out io.Writer, rep pruneBuildScratchReport) error {
	mode := "dry-run"
	if rep.Apply {
		mode = "apply"
	}
	fmt.Fprintf(out, "build-scratch prune: root=%s older-than=%s sanity-floor=%s mode=%s\n",
		rep.Root, rep.OlderThan, rep.SanityFloor, mode)
	for _, e := range rep.Entries {
		fmt.Fprintf(out, "  %-15s %12d bytes  age=%s  %s\n", e.Reason, e.Bytes, e.Age.Round(time.Second), e.Dir)
	}
	verb := "would-remove"
	if rep.Apply {
		verb = "removed"
	}
	fmt.Fprintf(out, "%s: %d bytes\nkept: %d bytes\n", verb, rep.BytesRemoved, rep.BytesKept)
	if !rep.Apply && rep.BytesRemoved > 0 {
		fmt.Fprintln(out, "(re-run with -apply to delete)")
	}
	return nil
}

func runStorageCensus(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("storage census", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	human := fs.Bool("human", false, "render a fixed-width table instead of JSON")
	topN := fs.Int("top", 10, "number of newest items to surface per category (0 = all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove storage census [-human] [-top N]")
	}

	root := coveRoot()
	cats := []storagecensus.Descriptor{
		{Name: "vms", Path: vmconfig.BaseDir()},
		{Name: "images", Path: ImagesBaseDir()},
		{Name: "runs", Path: vmconfig.RunsDir()},
		{Name: "cache", Path: vmconfig.CacheDir()},
		{Name: "build-scratch", Path: defaultBuildScratchRoot()},
		{Name: "store", Path: store.DefaultDir()},
	}

	rep, err := storagecensus.Walk(root, cats, storagecensus.Options{TopN: *topN})
	if err != nil {
		return fmt.Errorf("storage census: %w", err)
	}
	if *human {
		return storagecensus.RenderHuman(out, rep)
	}
	return storagecensus.EncodeJSON(out, rep)
}

// coveRoot returns the parent of vmconfig.BaseDir(), i.e. ~/.vz/.
func coveRoot() string {
	return filepath.Dir(vmconfig.BaseDir())
}
