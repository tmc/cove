// image_cli.go — `cove image build|list|rm` subcommand router.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"
)

// handleImageCommand routes `cove image <subcmd>`.
func handleImageCommand(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printImageUsage(os.Stdout)
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "build":
		return runImageBuild(rest)
	case "list", "ls":
		return runImageList(rest)
	case "inspect":
		return runImageInspect(rest)
	case "verify":
		return runImageVerify(rest)
	case "gc":
		return runImageGC(rest)
	case "prune":
		return runImagePrune(rest)
	case "rm", "remove", "delete":
		return runImageRm(rest)
	case "push":
		return runImagePush(rest)
	case "pull":
		return runImagePull(rest)
	case "load":
		return runImageLoad(rest)
	default:
		printImageUsage(os.Stderr)
		return fmt.Errorf("unknown image subcommand: %s", sub)
	}
}

func printImageUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove image <subcommand> [options]

Subcommands:
  build -from <vm> -tag <name[:tag]>   Snapshot a stopped VM into the image store
  list                                 List local images
  inspect <name[:tag]> [-json]         Show manifest details and downstream forks
  verify <name[:tag]> [-strict] [-json]
                                       Check freshness, provenance, and layout
  gc   [-dry-run] [-yes] [-older-than D]  Sweep images with zero live forks
  prune [-older-than D] [-filter GLOB] [-force] [-dry-run]
                                       Remove local images by age or tag glob
  rm   <name[:tag]>                    Delete a local image (refuses if forks exist)
  push <name[:tag]> <file|-|registry/ref:tag> [-gzip]
                                       Tar to a file/stdout or push to an OCI registry
  pull <registry/ref:tag> [-tag <name[:tag]>] [-force]
                                       Pull an image from an OCI registry
  load <file|-> [-tag <name[:tag]>] [-force]
                                       Extract a tarball into the image store

Examples:
  cove image build -from base -tag cove-runner-macos:14.5
  cove image list
  cove image rm cove-runner-macos:14.5
  cove run -fork-from cove-runner-macos:14.5 -ephemeral`)
}

func runImageBuild(args []string) (err error) {
	fs := flag.NewFlagSet("image build", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	from := fs.String("from", "", "source VM name (must be stopped)")
	tag := fs.String("tag", "", "image ref: name[:tag] (default tag: latest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" || *tag == "" {
		fs.Usage()
		return fmt.Errorf("image build requires -from <vm> and -tag <name[:tag]>")
	}
	ref, err := ParseImageRef(*tag)
	if err != nil {
		return err
	}
	metricsRun, metricsErr := beginStandaloneMetricsRun(*from, ref.String())
	if metricsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics init: %v\n", metricsErr)
	}
	defer finishStandaloneMetricsRun(metricsRun)
	defer func(started time.Time) {
		if metricsRun == nil {
			return
		}
		status := "ok"
		if err != nil {
			status = err.Error()
		}
		emitMetricEvent("run_complete", started, status, map[string]any{"command": "image build"})
	}(time.Now())
	buildStarted := time.Now()
	manifest, err := BuildImage(BuildImageOptions{
		SourceVM:    *from,
		Ref:         ref,
		BuildRecipe: fmt.Sprintf("cove image build -from %s -tag %s", *from, *tag),
	})
	if err != nil {
		emitMetricEvent("vm_create", buildStarted, err.Error(), map[string]any{"source_vm": *from})
		return err
	}
	emitMetricEvent("vm_create", buildStarted, "ok", map[string]any{"source_vm": *from})
	fmt.Printf("Built image %s from %s\n", ref, *from)
	fmt.Printf("  path:   %s\n", ref.Path())
	fmt.Printf("  disk:   %d bytes\n", manifest.DiskSize)
	fmt.Printf("  sha256: %s\n", manifest.DiskSHA256)
	return nil
}

func runImageList(args []string) error {
	fs := flag.NewFlagSet("image list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	entries, err := ListImages()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("No images found.")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTAG\tSIZE\tSOURCE\tCREATED")
	for _, e := range entries {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\n",
			e.Ref.Name,
			e.Ref.Tag,
			e.Manifest.DiskSize,
			e.Manifest.SourceVM,
			e.Manifest.CreatedAt.UTC().Format("2006-01-02 15:04:05"))
	}
	return tw.Flush()
}

func runImageRm(args []string) error {
	fs := flag.NewFlagSet("image rm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: cove image rm <name[:tag]>")
	}
	ref, err := ParseImageRef(fs.Arg(0))
	if err != nil {
		return err
	}
	if err := DeleteImage(ref); err != nil {
		return err
	}
	fmt.Printf("Deleted image %s\n", ref)
	return nil
}
