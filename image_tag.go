package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/cove/internal/imagestore"
)

type ImageTagOptions struct {
	Source imagestore.Ref
	Target imagestore.Ref
}

func TagImage(opts ImageTagOptions) error {
	if opts.Source.Name == "" || opts.Source.Tag == "" {
		return fmt.Errorf("image tag: source ref required")
	}
	if opts.Target.Name == "" || opts.Target.Tag == "" {
		return fmt.Errorf("image tag: target ref required")
	}
	if opts.Source == opts.Target {
		return fmt.Errorf("image tag: source and target are the same: %s", opts.Source)
	}
	if !ImageExists(opts.Source) {
		return fmt.Errorf("image tag: source image %s not found", opts.Source)
	}
	if ImageExists(opts.Target) {
		return fmt.Errorf("image tag: target image %s already exists", opts.Target)
	}
	// Hold the source image lock through clone + manifest write +
	// rename. Closes R7 in docs/research/image-gc-race-audit-2026-05-08.md
	// (gc deleting the source while we walk it).
	srcLock, err := acquireImageLockHook(opts.Source.Path())
	if err != nil {
		return fmt.Errorf("image tag: acquire source lock: %w", err)
	}
	defer func() {
		if releaseErr := srcLock.Release(); releaseErr != nil {
			fmt.Fprintf(os.Stderr, "warning: release image lock: %v\n", releaseErr)
		}
	}()
	manifest, err := LoadImageManifest(opts.Source)
	if err != nil {
		return fmt.Errorf("image tag: %w", err)
	}
	manifest.Name = opts.Target.Name
	manifest.Tag = opts.Target.Tag

	targetDir := opts.Target.Path()
	parent := filepath.Dir(targetDir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("image tag: create target parent: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".tag-"+opts.Target.Tag+"-")
	if err != nil {
		return fmt.Errorf("image tag: create temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmp) }
	if err := cloneImageDirectory(opts.Source.Path(), tmp); err != nil {
		cleanup()
		return err
	}
	if err := writeImageManifest(tmp, manifest); err != nil {
		cleanup()
		return fmt.Errorf("image tag: write manifest: %w", err)
	}
	if err := os.Rename(tmp, targetDir); err != nil {
		cleanup()
		return fmt.Errorf("image tag: publish target: %w", err)
	}
	return nil
}

func cloneImageDirectory(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == "manifest.json" {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := cloneFile(path, target); err != nil {
			if copyErr := copyFile(path, target); copyErr != nil {
				return fmt.Errorf("image tag: clone %s: %w (copy fallback: %v)", rel, err, copyErr)
			}
		}
		return nil
	})
}

func runImageTag(args []string) error {
	fs := flag.NewFlagSet("image tag", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: cove image tag <src-ref> <dst-ref>")
	}
	src, err := ParseImageRef(fs.Arg(0))
	if err != nil {
		return err
	}
	dst, err := ParseImageRef(fs.Arg(1))
	if err != nil {
		return err
	}
	if err := TagImage(ImageTagOptions{Source: src, Target: dst}); err != nil {
		return err
	}
	fmt.Printf("Tagged image %s as %s\n", strings.TrimSpace(src.String()), dst)
	return nil
}
