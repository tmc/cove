// image_gc.go — `cove image gc` sweeps unreferenced local images.
//
// Mirrors the disposable / ephemeral GC pattern in gc.go. An image is
// considered unreachable (and a sweep candidate) when no VM in
// vmconfig.BaseDir() has cfg.ParentImage equal to its ref. The same
// VMsForkedFromImage check that gates `cove image rm` is reused so a
// concurrent fork that lands between planning and deletion still
// blocks the sweep.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const cacheImageDefaultTTL = 7 * 24 * time.Hour

// ImageGCOptions configures GCImages.
type ImageGCOptions struct {
	OlderThan time.Duration
	DryRun    bool
}

// ImageGCSkipped records why a candidate was kept.
type ImageGCSkipped struct {
	Ref    ImageRef
	Reason string
}

// ImageGCResult summarizes a sweep.
type ImageGCResult struct {
	Removed []ImageRef
	Skipped []ImageGCSkipped
}

// GCImages walks ~/.vz/images/ and removes images that have zero live
// forks (and, when opts.OlderThan > 0, were created longer ago than
// that). Errors deleting individual images are accumulated into
// Skipped rather than aborting the sweep.
func GCImages(opts ImageGCOptions) (ImageGCResult, error) {
	entries, err := ListImages()
	if err != nil {
		return ImageGCResult{}, err
	}
	var res ImageGCResult
	now := time.Now()
	for _, entry := range entries {
		ref := entry.Ref
		if cacheTTL, ok := cacheImageTTL(ref); ok {
			created := imageCreatedAt(entry)
			age := now.Sub(created)
			if age < cacheTTL {
				emitImageGCKeep(ref, "recent", now)
				res.Skipped = append(res.Skipped, ImageGCSkipped{
					Ref:    ref,
					Reason: fmt.Sprintf("cache image newer than CACHE-TTL (age %s, ttl %s)", age.Round(time.Second), cacheTTL),
				})
				continue
			}
		}
		if opts.OlderThan > 0 && entry.Manifest != nil {
			age := now.Sub(entry.Manifest.CreatedAt)
			if age < opts.OlderThan {
				emitImageGCKeep(ref, "recent", now)
				res.Skipped = append(res.Skipped, ImageGCSkipped{
					Ref:    ref,
					Reason: fmt.Sprintf("newer than -older-than threshold (age %s)", age.Round(time.Second)),
				})
				continue
			}
		}
		forks, err := VMsForkedFromImage(ref)
		if err != nil {
			res.Skipped = append(res.Skipped, ImageGCSkipped{
				Ref:    ref,
				Reason: fmt.Sprintf("fork lookup failed: %v", err),
			})
			continue
		}
		if len(forks) > 0 {
			emitImageGCKeep(ref, "in_use", now)
			res.Skipped = append(res.Skipped, ImageGCSkipped{
				Ref:    ref,
				Reason: "has live forks: " + strings.Join(forks, ", "),
			})
			continue
		}
		if opts.DryRun {
			res.Removed = append(res.Removed, ref)
			continue
		}
		removed, skipped := gcImageLocked(ref, now)
		if skipped != nil {
			res.Skipped = append(res.Skipped, *skipped)
			continue
		}
		if removed {
			res.Removed = append(res.Removed, ref)
		}
	}
	return res, nil
}

// gcImageLocked performs the recheck-and-remove for a single image
// under the per-image lock. Returns (removed, skipped) — at most one
// is non-zero. If the lock cannot be acquired (concurrent fork or
// tag), returns a Skipped reason and gc will retry next sweep. Closes
// R1+R3+R7 in docs/research/image-gc-race-audit-2026-05-08.md.
func gcImageLocked(ref ImageRef, now time.Time) (bool, *ImageGCSkipped) {
	imgLock, err := TryAcquireImageLock(ref.Path())
	if err != nil {
		return false, &ImageGCSkipped{
			Ref:    ref,
			Reason: "image busy (concurrent fork or tag); will retry",
		}
	}
	defer imgLock.Release()
	recheck, err := VMsForkedFromImage(ref)
	if err != nil {
		return false, &ImageGCSkipped{
			Ref:    ref,
			Reason: fmt.Sprintf("fork recheck failed: %v", err),
		}
	}
	if len(recheck) > 0 {
		emitImageGCKeep(ref, "in_use", now)
		return false, &ImageGCSkipped{
			Ref:    ref,
			Reason: "fork raced into existence: " + strings.Join(recheck, ", "),
		}
	}
	path := ref.Path()
	if path == "" || path == ImagesBaseDir() {
		return false, &ImageGCSkipped{
			Ref:    ref,
			Reason: "refusing to remove image root",
		}
	}
	freed := pathSize(path)
	if err := os.RemoveAll(path); err != nil {
		return false, &ImageGCSkipped{
			Ref:    ref,
			Reason: fmt.Sprintf("remove failed: %v", err),
		}
	}
	emitMetricEvent("image_gc_evict", now, "ok", map[string]any{
		"image_ref":   ref.String(),
		"bytes_freed": freed,
	})
	return true, nil
}

func emitImageGCKeep(ref ImageRef, reason string, started time.Time) {
	if reason != "in_use" && reason != "recent" {
		return
	}
	emitMetricEvent("image_gc_keep", started, "ok", map[string]any{
		"image_ref": ref.String(),
		"reason":    reason,
	})
}

func imageCreatedAt(entry ImageEntry) time.Time {
	if entry.Manifest != nil && !entry.Manifest.CreatedAt.IsZero() {
		return entry.Manifest.CreatedAt
	}
	info, err := os.Stat(filepath.Join(entry.Ref.Path(), "manifest.json"))
	if err == nil {
		return info.ModTime()
	}
	return time.Now()
}

func pathSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func isCacheImage(ref ImageRef) bool {
	return strings.HasPrefix(ref.Name, "cache/")
}

func cacheImageTTL(ref ImageRef) (time.Duration, bool) {
	if !isCacheImage(ref) {
		return 0, false
	}
	data, err := os.ReadFile(filepath.Join(ref.Path(), "CACHE-TTL"))
	if err != nil {
		return cacheImageDefaultTTL, true
	}
	ttl, err := time.ParseDuration(strings.TrimSpace(string(data)))
	if err != nil || ttl <= 0 {
		return cacheImageDefaultTTL, true
	}
	return ttl, true
}

// runImageGC implements `cove image gc [-dry-run] [-yes] [-older-than D]`.
func runImageGC(args []string) error {
	fs := flag.NewFlagSet("image gc", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "print images without deleting them")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	olderThan := fs.Duration("older-than", 0, "only delete images older than this duration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	metricsRun, metricsErr := beginStandaloneMetricsRun("image-gc", "cache")
	if metricsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics init: %v\n", metricsErr)
	}
	defer finishStandaloneMetricsRun(metricsRun)

	plan, err := GCImages(ImageGCOptions{OlderThan: *olderThan, DryRun: true})
	if err != nil {
		return err
	}
	if len(plan.Removed) == 0 {
		if *dryRun {
			fmt.Println("No unreferenced images would be removed.")
		} else {
			fmt.Println("No unreferenced images to remove.")
		}
		for _, sk := range plan.Skipped {
			fmt.Printf("keep %s (%s)\n", sk.Ref, sk.Reason)
		}
		return nil
	}
	for _, ref := range plan.Removed {
		fmt.Printf("would remove image %s\n", ref)
	}
	for _, sk := range plan.Skipped {
		fmt.Printf("keep %s (%s)\n", sk.Ref, sk.Reason)
	}
	if *dryRun {
		return nil
	}
	if !*yes {
		fmt.Printf("Remove %d image(s)? [y/N] ", len(plan.Removed))
		var resp string
		fmt.Scanln(&resp)
		resp = strings.ToLower(strings.TrimSpace(resp))
		if resp != "y" && resp != "yes" {
			fmt.Println("aborted.")
			return nil
		}
	}
	actual, err := GCImages(ImageGCOptions{OlderThan: *olderThan, DryRun: false})
	if err != nil {
		return err
	}
	for _, ref := range actual.Removed {
		fmt.Printf("removed image %s\n", ref)
	}
	for _, sk := range actual.Skipped {
		fmt.Printf("keep %s (%s)\n", sk.Ref, sk.Reason)
	}
	return nil
}
