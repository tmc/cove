package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tmc/vz-macos/internal/storagecensus"
	"github.com/tmc/vz-macos/internal/store"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

func handleStorageCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: cove storage <subcommand> [args]\n  census    Walk ~/.vz/ and report per-category disk usage")
	}
	switch args[0] {
	case "census":
		return runStorageCensus(args[1:], os.Stdout)
	default:
		return fmt.Errorf("storage: unknown subcommand %q", args[0])
	}
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
