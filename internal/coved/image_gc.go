package coved

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	runmetrics "github.com/tmc/vz-macos/internal/metrics"
)

const DefaultImageGCInterval = time.Hour

type ImageGCStats struct {
	ManifestsScanned int
	ManifestsRemoved int
	BytesFreed       int64
	DurationMS       int64
	Skipped          bool
}

type ImageGCScheduler struct {
	Interval    time.Duration
	HomeDir     string
	MetricsPath string
	Logger      *slog.Logger
	Now         func() time.Time

	mu    sync.Mutex
	stats ImageGCStats
	last  time.Time
	runs  int64
	bytes int64
}

func NewImageGCScheduler(home string, logger *slog.Logger) *ImageGCScheduler {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return &ImageGCScheduler{
		Interval:    DefaultImageGCInterval,
		HomeDir:     home,
		MetricsPath: filepath.Join(home, ".vz", "metrics.jsonl"),
		Logger:      logger,
		Now:         time.Now,
	}
}

func (s *ImageGCScheduler) RunScheduledImageGC(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = DefaultImageGCInterval
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if _, err := s.RunOnce(ctx); err != nil && s.Logger != nil {
				s.Logger.Debug("scheduled image gc", slog.Any("err", err))
			}
			timer.Reset(interval)
		}
	}
}

func (s *ImageGCScheduler) RunOnce(ctx context.Context) (ImageGCStats, error) {
	if err := ctx.Err(); err != nil {
		return ImageGCStats{}, err
	}
	start := s.now()
	stats, err := s.runOnceLocked(ctx)
	stats.DurationMS = time.Since(start).Milliseconds()
	if emitErr := s.emit(ctx, start, stats, err); emitErr != nil && err == nil {
		err = emitErr
	}
	s.record(stats)
	return stats, err
}

func (s *ImageGCScheduler) Stats() (ImageGCStats, time.Time, int64, int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats, s.last, s.runs, s.bytes
}

func (s *ImageGCScheduler) runOnceLocked(ctx context.Context) (ImageGCStats, error) {
	lock, err := s.acquireLock()
	if err != nil {
		if os.IsExist(err) {
			return ImageGCStats{Skipped: true}, nil
		}
		return ImageGCStats{}, err
	}
	defer lock()

	refs, err := s.referencedImages()
	if err != nil {
		return ImageGCStats{}, err
	}
	images, err := s.listImages()
	if err != nil {
		return ImageGCStats{}, err
	}
	var stats ImageGCStats
	stats.ManifestsScanned = len(images)
	for _, img := range images {
		if refs[img.ref] {
			continue
		}
		size := dirSize(img.path)
		if err := os.RemoveAll(img.path); err != nil {
			if s.Logger != nil {
				s.Logger.Debug("remove image", slog.String("path", img.path), slog.Any("err", err))
			}
			continue
		}
		stats.ManifestsRemoved++
		stats.BytesFreed += size
	}
	return stats, nil
}

func (s *ImageGCScheduler) emit(ctx context.Context, started time.Time, stats ImageGCStats, runErr error) error {
	path := s.MetricsPath
	if path == "" {
		path = filepath.Join(s.home(), ".vz", "metrics.jsonl")
	}
	sink, err := runmetrics.NewJSONLSink(path)
	if err != nil {
		return err
	}
	defer sink.Close()
	status := "ok"
	if stats.Skipped {
		status = "skipped"
	} else if runErr != nil {
		status = "error"
	}
	extra := map[string]any{
		"manifests_scanned": stats.ManifestsScanned,
		"manifests_removed": stats.ManifestsRemoved,
		"bytes_freed":       stats.BytesFreed,
		"duration_ms":       stats.DurationMS,
	}
	return sink.Emit(ctx, runmetrics.Event{
		Timestamp:  started.UTC().Format(time.RFC3339Nano),
		EventType:  "image.gc.run",
		DurationMS: stats.DurationMS,
		Status:     status,
		Extra:      extra,
	})
}

func (s *ImageGCScheduler) record(stats ImageGCStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats = stats
	s.last = s.now()
	s.runs++
	s.bytes += stats.BytesFreed
}

func (s *ImageGCScheduler) acquireLock() (func(), error) {
	path := filepath.Join(s.home(), ".vz", "image-gc.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return func() {
		_ = f.Close()
		_ = os.Remove(path)
	}, nil
}

type localImage struct {
	ref  string
	path string
}

func (s *ImageGCScheduler) listImages() ([]localImage, error) {
	root := filepath.Join(s.home(), ".vz", "images")
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []localImage
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || !d.IsDir() || path == root {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "manifest.json")); err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) < 2 {
			return nil
		}
		ref := strings.Join(parts[:len(parts)-1], "/") + ":" + parts[len(parts)-1]
		out = append(out, localImage{ref: ref, path: path})
		return filepath.SkipDir
	})
	return out, err
}

func (s *ImageGCScheduler) referencedImages() (map[string]bool, error) {
	root := filepath.Join(s.home(), ".vz", "vms")
	refs := make(map[string]bool)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return refs, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "config.json"))
		if err != nil {
			continue
		}
		var cfg struct {
			ParentImage string `json:"parentImage"`
		}
		if json.Unmarshal(data, &cfg) == nil && cfg.ParentImage != "" {
			refs[cfg.ParentImage] = true
		}
	}
	return refs, nil
}

func (s *ImageGCScheduler) home() string {
	if s.HomeDir != "" {
		return s.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

func (s *ImageGCScheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func dirSize(path string) int64 {
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
