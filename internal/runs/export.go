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

	"github.com/tmc/cove/internal/metrics"
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
	if show.Fork != nil {
		if err := exportForkSummaryMarkdown(w, show.Fork); err != nil {
			return err
		}
	}
	if show.Network != nil {
		if err := exportNetworkSummaryMarkdown(w, show.Network); err != nil {
			return err
		}
	}
	if show.Resource != nil {
		if err := exportResourceSummaryMarkdown(w, show.Resource); err != nil {
			return err
		}
	}
	return nil
}

func exportForkSummaryMarkdown(w io.Writer, s *ForkSummary) error {
	if _, err := fmt.Fprint(w, "\n### Fork\n\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Field | Value |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- |"); err != nil {
		return err
	}
	for _, row := range forkSummaryRows(s) {
		if _, err := fmt.Fprintf(w, "| %s | %s |\n", markdownCell(row.Name), markdownCell(row.Value)); err != nil {
			return err
		}
	}
	return nil
}

func exportNetworkSummaryMarkdown(w io.Writer, s *NetworkSummary) error {
	if _, err := fmt.Fprint(w, "\n### Network\n\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Field | Value |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | --- |"); err != nil {
		return err
	}
	for _, row := range networkSummaryRows(s) {
		if _, err := fmt.Fprintf(w, "| %s | %s |\n", markdownCell(row.Name), markdownCell(row.Value)); err != nil {
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

func exportResourceSummaryMarkdown(w io.Writer, s *ResourceSummary) error {
	if _, err := fmt.Fprint(w, "\n### Resources\n\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| Metric | Value |"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "| --- | ---: |"); err != nil {
		return err
	}
	for _, row := range resourceSummaryRows(s) {
		if _, err := fmt.Fprintf(w, "| %s | %s |\n", markdownCell(row.Name), markdownCell(row.Value)); err != nil {
			return err
		}
	}
	if len(s.Warnings) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "\n**Resource hints:**"); err != nil {
		return err
	}
	for _, warning := range s.Warnings {
		if _, err := fmt.Fprintf(w, "- `%s`: %s\n", markdownCell(warning.Class), markdownCell(warning.Reason)); err != nil {
			return err
		}
	}
	return nil
}

func markdownCell(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", `\|`)
	return s
}
