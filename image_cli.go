// image_cli.go — `cove image build|list|rm` subcommand router.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
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
	case "rm", "remove", "delete":
		return runImageRm(rest)
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
  rm   <name[:tag]>                    Delete a local image (refuses if forks exist)

Examples:
  cove image build -from base -tag cove-runner-macos:14.5
  cove image list
  cove image rm cove-runner-macos:14.5
  cove run -fork-from cove-runner-macos:14.5 -ephemeral`)
}

func runImageBuild(args []string) error {
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
	manifest, err := BuildImage(BuildImageOptions{SourceVM: *from, Ref: ref})
	if err != nil {
		return err
	}
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
