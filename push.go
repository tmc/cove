package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/vz-macos/internal/ociimage"
)

type pushOptions struct {
	BaseRef        string
	ChunkSize      int64
	DryRun         bool
	LumeCompat     bool
	AdditionalTags stringList
}

type pushPlan struct {
	VMName     string
	VMDir      string
	DiskPath   string
	Ref        string
	BaseRef    string
	ChunkSize  int64
	DiskSize   int64
	Chunks     []ociimage.Chunk
	ZeroChunks int
	ZeroBytes  int64
	LumeCompat bool
	ExtraTags  []string
}

type stringList []string

func (l *stringList) String() string {
	return strings.Join(*l, ",")
}

func (l *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty value")
	}
	*l = append(*l, value)
	return nil
}

func handlePush(args []string) error {
	opts, pos, err := parsePushArgs(args, os.Stderr)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("usage: cove push <vm> <ref> [flags]")
	}
	if !opts.DryRun {
		return fmt.Errorf("cove push: registry upload is not implemented yet; use --dry-run to inspect the chunk plan")
	}

	plan, err := buildPushPlan(pos[0], pos[1], opts)
	if err != nil {
		return err
	}
	printPushDryRun(os.Stdout, plan)
	return nil
}

func parsePushArgs(args []string, w io.Writer) (pushOptions, []string, error) {
	opts := pushOptions{ChunkSize: ociimage.DefaultChunkSize}
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(w)
	fs.StringVar(&opts.BaseRef, "base", "", "base image for delta push")
	chunkSizeMB := fs.Int64("chunk-size", opts.ChunkSize>>20, "chunk size in megabytes")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "print the plan without uploading")
	fs.BoolVar(&opts.LumeCompat, "lume-compat", false, "emit dual cove and lume annotations")
	fs.Var(&opts.AdditionalTags, "additional-tag", "additional tag to publish")
	fs.Usage = func() { printPushUsage(w) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, nil, nil
		}
		return opts, nil, err
	}
	if *chunkSizeMB <= 0 {
		return opts, nil, fmt.Errorf("invalid chunk size %d MB", *chunkSizeMB)
	}
	opts.ChunkSize = *chunkSizeMB << 20
	return opts, fs.Args(), nil
}

func buildPushPlan(vmName, ref string, opts pushOptions) (*pushPlan, error) {
	if opts.ChunkSize <= 0 {
		return nil, fmt.Errorf("invalid chunk size %d", opts.ChunkSize)
	}
	vmDirectory := GetVMPath(vmName)
	if !ValidateVM(vmDirectory) {
		return nil, fmt.Errorf("vm not found or invalid: %s", vmDirectory)
	}
	diskPath, err := pushDiskPath(vmDirectory)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(diskPath)
	if err != nil {
		return nil, fmt.Errorf("open disk: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat disk: %w", err)
	}
	chunks, err := ociimage.DescribeChunks(f, opts.ChunkSize)
	if err != nil {
		return nil, err
	}
	plan := &pushPlan{
		VMName:     vmName,
		VMDir:      vmDirectory,
		DiskPath:   diskPath,
		Ref:        ref,
		BaseRef:    opts.BaseRef,
		ChunkSize:  opts.ChunkSize,
		DiskSize:   info.Size(),
		Chunks:     chunks,
		LumeCompat: opts.LumeCompat,
		ExtraTags:  append([]string(nil), opts.AdditionalTags...),
	}
	for _, c := range chunks {
		if c.Zero {
			plan.ZeroChunks++
			plan.ZeroBytes += c.Size
		}
	}
	return plan, nil
}

func pushDiskPath(vmDirectory string) (string, error) {
	for _, name := range []string{"disk.img", "linux-disk.img"} {
		path := filepath.Join(vmDirectory, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat disk: %w", err)
		}
	}
	return "", fmt.Errorf("disk image not found in %s", vmDirectory)
}

func printPushDryRun(w io.Writer, plan *pushPlan) {
	fmt.Fprintln(w, "Push dry run")
	fmt.Fprintf(w, "  vm: %s\n", plan.VMName)
	fmt.Fprintf(w, "  ref: %s\n", plan.Ref)
	fmt.Fprintf(w, "  disk: %s\n", plan.DiskPath)
	fmt.Fprintf(w, "  disk size: %s\n", FormatSize(plan.DiskSize))
	fmt.Fprintf(w, "  chunk size: %s\n", FormatSize(plan.ChunkSize))
	fmt.Fprintf(w, "  chunks: %d\n", len(plan.Chunks))
	fmt.Fprintf(w, "  zero chunks: %d (%s)\n", plan.ZeroChunks, FormatSize(plan.ZeroBytes))
	fmt.Fprintf(w, "  non-zero bytes: %s\n", FormatSize(plan.DiskSize-plan.ZeroBytes))
	if plan.BaseRef != "" {
		fmt.Fprintf(w, "  base: %s (not fetched in dry-run)\n", plan.BaseRef)
	}
	if len(plan.ExtraTags) > 0 {
		fmt.Fprintf(w, "  additional tags: %s\n", strings.Join(plan.ExtraTags, ", "))
	}
	if plan.LumeCompat {
		fmt.Fprintln(w, "  lume compat: yes")
	}
}

func printPushUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove push <vm> <ref> [flags]

Plan or push a VM disk as an OCI image.

Current implementation supports --dry-run only. Registry upload, compression,
base-manifest fetch, and tag publication are wired in later OCI slices.

Flags:
  --base <ref>              Base image for delta push
  --chunk-size <mb>         Chunk size in megabytes (default 512)
  --dry-run                 Print the chunk plan without uploading
  --lume-compat             Plan dual cove and lume annotations
  --additional-tag <tag>    Additional tag to publish (repeatable)`)
}
