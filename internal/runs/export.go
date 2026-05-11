package runs

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/vz-macos/internal/metrics"
)

// ExportJSON writes the full run metrics event array as JSON.
func ExportJSON(w io.Writer, root, prefix string) error {
	if w == nil {
		return fmt.Errorf("export json: nil writer")
	}
	show, err := LoadShow(root, prefix)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(show.Events)
}

// ExportGHASummary writes a GitHub Actions markdown summary.
func ExportGHASummary(w io.Writer, root, prefix string) error {
	if w == nil {
		return fmt.Errorf("export gha summary: nil writer")
	}
	show, err := LoadShow(root, prefix)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "## Cove Run %s\n\n", show.RunID); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Phase | Status | Duration |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- | ---: |"); err != nil {
		return err
	}
	for _, e := range show.Lifecycle {
		if _, err := fmt.Fprintf(w, "| %s | %s | %dms |\n", e.EventType, badge(eventStatus(e)), e.DurationMS); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "**Result:** %s", badge(show.Result.Status)); err != nil {
		return err
	}
	if show.Result.HasExitCode {
		if _, err := fmt.Fprintf(w, " exit_code=%d", show.Result.ExitCode); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, " wallclock=%dms", show.Result.WallclockMS); err != nil {
		return err
	}
	if ref := runImageRef(show.Events); ref != "" {
		if _, err := fmt.Fprintf(w, " image_ref=%s", ref); err != nil {
			return err
		}
	}
	if show.Result.FailedEvents > 0 {
		if _, err := fmt.Fprintf(w, " failed_events=%d", show.Result.FailedEvents); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if show.Failure.Class != "" {
		if _, err := fmt.Fprintf(w, "\n**Failure:** `%s`: %s\n", show.Failure.Class, show.Failure.Reason); err != nil {
			return err
		}
	}
	return nil
}

// ExportTarGz writes a gzip tarball of the full run directory.
func ExportTarGz(w io.Writer, root, prefix string) error {
	if w == nil {
		return fmt.Errorf("export tar: nil writer")
	}
	dir, err := matchRunDir(root, prefix)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(dir), path)
		if err != nil {
			return err
		}
		h.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		return nil
	}); err != nil {
		if closeErr := closeTarGz(tw, gz); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return fmt.Errorf("export tar: %w", err)
	}
	if err := closeTarGz(tw, gz); err != nil {
		return fmt.Errorf("export tar: %w", err)
	}
	return nil
}

func closeTarGz(tw *tar.Writer, gz *gzip.Writer) error {
	return closeAll(tw, gz)
}

func closeAll(closers ...io.Closer) error {
	var err error
	for _, c := range closers {
		if e := c.Close(); e != nil {
			err = errors.Join(err, e)
		}
	}
	return err
}

func runImageRef(events []metrics.Event) string {
	for _, e := range events {
		if e.ImageRef != "" {
			return e.ImageRef
		}
	}
	return ""
}

func badge(status string) string {
	switch strings.ToLower(status) {
	case "ok":
		return "[ok] ok"
	case "", "-":
		return "[n/a] n/a"
	default:
		return "[fail] " + status
	}
}
