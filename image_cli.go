// image_cli.go — `cove image build|list|rm` subcommand router.

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/tmc/cove/internal/imagestore"
)

// handleImageCommand routes `cove image <subcmd>`.
func handleImageCommand(env commandEnv, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printImageUsage(env.Stdout)
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "build":
		return runImageBuild(env, rest)
	case "list", "ls":
		return runImageList(env, rest)
	case "inspect":
		return runImageInspect(env, rest)
	case "verify":
		return runImageVerify(env, rest)
	case "gc":
		return runImageGC(env, rest)
	case "prune":
		return runImagePrune(env, rest)
	case "tag":
		return runImageTag(env, rest)
	case "history":
		return runImageHistory(env, rest)
	case "search":
		return runImageSearch(env, rest)
	case "rm", "remove", "delete":
		return runImageRm(env, rest)
	case "push":
		return runImagePush(env, rest)
	case "pull":
		return runImagePull(env, rest)
	case "load":
		return runImageLoad(env, rest)
	default:
		printImageUsage(env.Stderr)
		return fmt.Errorf("unknown image subcommand: %s", sub)
	}
}

func printImageUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove image <subcommand> [options]

Subcommands:
  build -from <vm> -tag <name[:tag]>   Snapshot a stopped VM into the image store
  list [-json]                         List local images
  inspect [-json] <name[:tag]>         Show manifest details and downstream forks
  verify <name[:tag]> [-strict] [-json]
                                       Check freshness, provenance, and layout
  gc   [-dry-run] [-yes] [-older-than D]  Sweep images with zero live forks
  prune [-older-than D] [-filter GLOB] [-force] [-dry-run]
                                       Remove local images by age or tag glob
  tag <src-ref> <dst-ref>              Add a local tag without rebuilding
  history <name[:tag]> [-json]         Show layer and provenance lineage
  search [-json] [query]               Fuzzy-search local images
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

func runImageBuild(env commandEnv, args []string) (err error) {
	fs := flag.NewFlagSet("image build", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	from := fs.String("from", "", "source VM name (must be stopped)")
	tag := fs.String("tag", "", "image ref: name[:tag] (default tag: latest)")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
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
		fmt.Fprintf(env.Stderr, "warning: metrics init: %v\n", metricsErr)
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
	fmt.Fprintf(env.Stdout, "Built image %s from %s\n", ref, *from)
	fmt.Fprintf(env.Stdout, "  path:   %s\n", ref.Path())
	fmt.Fprintf(env.Stdout, "  disk:   %d bytes\n", manifest.DiskSize)
	fmt.Fprintf(env.Stdout, "  sha256: %s\n", manifest.DiskSHA256)
	return nil
}

func runImageList(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("image list", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { printImageListUsage(fs.Output()) }
	asJSON := fs.Bool("json", false, "emit machine-readable JSON")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	entries, err := ListImages()
	if err != nil {
		return err
	}
	if *asJSON {
		return writeImageListJSON(env.Stdout, entries)
	}
	if len(entries) == 0 {
		fmt.Fprintln(env.Stdout, "No images found.")
		fmt.Fprintln(env.Stdout, "  Images are optional. Create a VM first with: cove up -user <name>")
		fmt.Fprintln(env.Stdout, "  Snapshot a stopped VM later with: cove image build -from <vm> -tag <name>:latest")
		return nil
	}
	tw := tabwriter.NewWriter(env.Stdout, 0, 4, 2, ' ', 0)
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

type imageListResult struct {
	Ref     string `json:"ref"`
	Name    string `json:"name"`
	Tag     string `json:"tag"`
	Size    int64  `json:"size"`
	Source  string `json:"source,omitempty"`
	Created string `json:"created,omitempty"`
}

func writeImageListJSON(w io.Writer, entries []imagestore.Entry) error {
	results := make([]imageListResult, 0, len(entries))
	for _, entry := range entries {
		result := imageListResult{
			Ref:  entry.Ref.String(),
			Name: entry.Ref.Name,
			Tag:  entry.Ref.Tag,
		}
		if entry.Manifest != nil {
			result.Size = entry.Manifest.DiskSize
			result.Source = entry.Manifest.SourceVM
			if !entry.Manifest.CreatedAt.IsZero() {
				result.Created = entry.Manifest.CreatedAt.UTC().Format(time.RFC3339)
			}
		}
		results = append(results, result)
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("encode image list: %w", err)
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func printImageListUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove image list [-json]

List local images in the cove image store.

Flags:
  -json    emit machine-readable JSON

Columns:
  NAME      Image repository name
  TAG       Image tag
  SIZE      Uncompressed disk size in bytes
  SOURCE    Source VM used to build the image
  CREATED   Manifest creation time`)
}

func runImageRm(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("image rm", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.Usage = func() { printImageRmUsage(fs.Output()) }
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
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
	fmt.Fprintf(env.Stdout, "Deleted image %s\n", ref)
	return nil
}

func printImageRmUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove image rm <name[:tag]>

Delete a local image ref. The command refuses images that still have VM forks;
use cove image inspect <ref> to see downstream forks first.`)
}
