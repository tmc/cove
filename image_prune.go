package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/imagestore"
	"github.com/tmc/cove/internal/storagepins"
)

type ImagePruneOptions struct {
	OlderThan time.Duration
	Filter    string
	DryRun    bool
	Now       func() time.Time
}

type ImagePruneSkipped struct {
	Ref    ImageRef
	Reason string
}

type ImagePruneResult struct {
	Removed []ImageRef
	Skipped []ImagePruneSkipped
}

func PruneImages(opts ImagePruneOptions) (ImagePruneResult, error) {
	entries, err := ListImages()
	if err != nil {
		return ImagePruneResult{}, err
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	cutoff := now()
	pins, err := storagepins.Load(coveRoot())
	if err != nil {
		return ImagePruneResult{}, fmt.Errorf("image prune: load pins: %w", err)
	}
	var res ImagePruneResult
	for _, entry := range entries {
		if !imagePruneMatches(entry, opts, cutoff) {
			continue
		}
		ref := entry.Ref
		if pins.IsPinned("image", ref.String()) {
			res.Skipped = append(res.Skipped, ImagePruneSkipped{Ref: ref, Reason: "pinned"})
			continue
		}
		forks, err := VMsForkedFromImage(ref)
		if err != nil {
			res.Skipped = append(res.Skipped, ImagePruneSkipped{Ref: ref, Reason: fmt.Sprintf("fork lookup failed: %v", err)})
			continue
		}
		if len(forks) > 0 {
			res.Skipped = append(res.Skipped, ImagePruneSkipped{Ref: ref, Reason: "has live forks: " + strings.Join(forks, ", ")})
			continue
		}
		if opts.DryRun {
			res.Removed = append(res.Removed, ref)
			continue
		}
		freed := pathSize(ref.Path())
		if err := DeleteImage(ref); err != nil {
			res.Skipped = append(res.Skipped, ImagePruneSkipped{Ref: ref, Reason: err.Error()})
			continue
		}
		res.Removed = append(res.Removed, ref)
		emitMetricEvent("image_gc_evict", cutoff, "ok", map[string]any{
			"image_ref":   ref.String(),
			"bytes_freed": freed,
		})
	}
	return res, nil
}

func imagePruneMatches(entry imagestore.Entry, opts ImagePruneOptions, now time.Time) bool {
	if opts.Filter != "" {
		if ok, err := filepath.Match(opts.Filter, entry.Ref.Tag); err == nil && ok {
			return true
		}
	}
	if opts.OlderThan <= 0 {
		return false
	}
	return now.Sub(imageCreatedAt(entry)) >= opts.OlderThan
}

func runImagePrune(args []string) error {
	fs := flag.NewFlagSet("image prune", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	olderThanText := fs.String("older-than", "7d", "delete images older than this duration")
	filter := fs.String("filter", "", "delete images with tags matching this glob")
	force := fs.Bool("force", false, "skip confirmation prompt")
	dryRun := fs.Bool("dry-run", false, "print images without deleting them")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove image prune [-older-than D] [-filter GLOB] [-force] [-dry-run]")
	}
	olderThan, err := parseImagePruneDuration(*olderThanText)
	if err != nil {
		return err
	}
	if olderThan <= 0 && strings.TrimSpace(*filter) == "" {
		return fmt.Errorf("image prune requires -older-than or -filter")
	}
	metricsRun, metricsErr := beginStandaloneMetricsRun("image-prune", "local")
	if metricsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics init: %v\n", metricsErr)
	}
	defer finishStandaloneMetricsRun(metricsRun)

	opts := ImagePruneOptions{OlderThan: olderThan, Filter: strings.TrimSpace(*filter), DryRun: true}
	plan, err := PruneImages(opts)
	if err != nil {
		return err
	}
	if len(plan.Removed) == 0 {
		if *dryRun {
			fmt.Println("No images would be pruned.")
		} else {
			fmt.Println("No images to prune.")
		}
		for _, sk := range plan.Skipped {
			fmt.Printf("keep %s (%s)\n", sk.Ref, sk.Reason)
		}
		return nil
	}
	for _, ref := range plan.Removed {
		fmt.Printf("would prune image %s\n", ref)
	}
	for _, sk := range plan.Skipped {
		fmt.Printf("keep %s (%s)\n", sk.Ref, sk.Reason)
	}
	if *dryRun {
		return nil
	}
	if !*force {
		fmt.Printf("Prune %d image(s)? [y/N] ", len(plan.Removed))
		var resp string
		fmt.Scanln(&resp)
		resp = strings.ToLower(strings.TrimSpace(resp))
		if resp != "y" && resp != "yes" {
			fmt.Println("aborted.")
			return nil
		}
	}
	opts.DryRun = false
	actual, err := PruneImages(opts)
	if err != nil {
		return err
	}
	for _, ref := range actual.Removed {
		fmt.Printf("pruned image %s\n", ref)
	}
	for _, sk := range actual.Skipped {
		fmt.Printf("keep %s (%s)\n", sk.Ref, sk.Reason)
	}
	return nil
}

func parseImagePruneDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := time.ParseDuration(strings.TrimSuffix(s, "d") + "h")
		if err != nil {
			return 0, fmt.Errorf("parse -older-than: %w", err)
		}
		return n * 24, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("parse -older-than: %w", err)
	}
	return d, nil
}
