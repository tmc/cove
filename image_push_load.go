// image_push_load.go — image transport for the local store.
//
// `cove image push` can either tar a local image to a file/stdout or publish
// it to an OCI registry, depending on the destination shape. `cove image load`
// keeps the tarball import path for files/stdin; registry imports use
// `cove image pull`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tmc/cove/internal/imagestore"
	"golang.org/x/term"
)

var stdoutIsTerminal = term.IsTerminal
var stdinIsTerminal = term.IsTerminal

func runImagePush(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("image push", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	gz := fs.Bool("gzip", false, "gzip-compress the tarball")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return fmt.Errorf("image push requires <ref> and <file|registry/ref:tag|->")
	}
	ref, err := ParseImageRef(fs.Arg(0))
	if err != nil {
		return err
	}
	dst := fs.Arg(1)
	if isRegistryReference(dst) {
		if *gz {
			return fmt.Errorf("image push: -gzip is only valid for tarball export")
		}
		desc, err := PushImageToRegistry(context.Background(), ref, dst)
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "Pushed image %s to %s\n", ref, dst)
		fmt.Fprintf(env.Stdout, "  digest: %s\n", desc.Digest)
		return nil
	}
	if dst == "-" {
		if fd, ok := writerFD(env.Stdout); ok && stdoutIsTerminal(fd) {
			return fmt.Errorf("image push: refusing to write tarball to a TTY (redirect stdout or pass a file path)")
		}
		if err := imagestore.WriteTar(ref, env.Stdout, *gz); err != nil {
			return err
		}
		fmt.Fprintf(env.Stderr, "Pushed image %s to stdout\n", ref)
		return nil
	}
	if err := imagestore.WriteTarFile(ref, dst, *gz); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Pushed image %s to %s\n", ref, dst)
	return nil
}

func runImageLoad(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("image load", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	tag := fs.String("tag", "", "override image ref on load (name[:tag])")
	force := fs.Bool("force", false, "overwrite if image already exists")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("image load requires <file|->")
	}
	src := fs.Arg(0)
	if isRegistryReference(src) {
		return fmt.Errorf("image load: %q looks like a registry reference; use cove image pull", src)
	}
	if src == "-" {
		if fd, ok := readerFD(env.Stdin); ok && stdinIsTerminal(fd) {
			return fmt.Errorf("image load: refusing to read tarball from a TTY (redirect stdin or pass a file path)")
		}
		ref, err := imagestore.ReadTar(env.Stdin, *tag, *force)
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "Loaded image %s\n", ref)
		return nil
	}
	ref, err := imagestore.LoadTarFromFile(src, *tag, *force)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Loaded image %s\n", ref)
	return nil
}

func runImagePull(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("image pull", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	tag := fs.String("tag", "", "override image ref on pull (name[:tag])")
	force := fs.Bool("force", false, "overwrite if image already exists")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("image pull requires <registry/ref:tag>")
	}
	ref, desc, err := PullImageFromRegistry(context.Background(), fs.Arg(0), *tag, *force)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "Pulled image %s from %s\n", ref, fs.Arg(0))
	fmt.Fprintf(env.Stdout, "  digest: %s\n", desc.Digest)
	return nil
}

func writerFD(w io.Writer) (int, bool) {
	f, ok := w.(*os.File)
	if !ok || f == nil {
		return 0, false
	}
	return int(f.Fd()), true
}

func readerFD(r io.Reader) (int, bool) {
	f, ok := r.(*os.File)
	if !ok || f == nil {
		return 0, false
	}
	return int(f.Fd()), true
}
