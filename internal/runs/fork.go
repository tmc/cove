package runs

import (
	"fmt"
	"io"
	"strings"

	"github.com/tmc/cove/internal/metrics"
)

const forkCreatedEvent = "fork_created"

// ForkSummary is a derived view over fork_created events.
type ForkSummary struct {
	SourceKind   string `json:"source_kind,omitempty"`
	SourceRef    string `json:"source_ref,omitempty"`
	ChildName    string `json:"child_name,omitempty"`
	ChildPath    string `json:"child_path,omitempty"`
	Mode         string `json:"mode,omitempty"`
	DiskReuse    string `json:"disk_reuse,omitempty"`
	Ephemeral    *bool  `json:"ephemeral,omitempty"`
	Keep         *bool  `json:"keep,omitempty"`
	Cleanup      string `json:"cleanup,omitempty"`
	Verification string `json:"verification,omitempty"`
	Limitation   string `json:"limitation,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
}

func summarizeFork(events []metrics.Event) *ForkSummary {
	var s *ForkSummary
	for _, e := range events {
		if e.EventType != forkCreatedEvent {
			continue
		}
		sourceRef := extraString(e.Extra, "source_ref")
		if sourceRef == "" {
			sourceRef = e.ImageRef
		}
		sourceKind := extraString(e.Extra, "source_kind")
		if sourceKind == "" {
			sourceKind = inferForkSourceKind(sourceRef)
		}
		next := ForkSummary{
			SourceKind:   sourceKind,
			SourceRef:    sourceRef,
			ChildName:    extraString(e.Extra, "child_name"),
			ChildPath:    extraString(e.Extra, "child_path"),
			Mode:         extraString(e.Extra, "mode"),
			DiskReuse:    extraString(e.Extra, "disk_reuse"),
			Ephemeral:    extraBoolPtr(e.Extra, "ephemeral"),
			Keep:         extraBoolPtr(e.Extra, "keep"),
			Cleanup:      extraString(e.Extra, "cleanup"),
			Verification: extraString(e.Extra, "verification"),
			Limitation:   extraString(e.Extra, "limitation"),
			DurationMS:   e.DurationMS,
		}
		s = &next
	}
	if s == nil || s.empty() {
		return nil
	}
	return s
}

func (s *ForkSummary) empty() bool {
	return s.SourceKind == "" &&
		s.SourceRef == "" &&
		s.ChildName == "" &&
		s.ChildPath == "" &&
		s.Mode == "" &&
		s.DiskReuse == "" &&
		s.Ephemeral == nil &&
		s.Keep == nil &&
		s.Cleanup == "" &&
		s.Verification == "" &&
		s.Limitation == "" &&
		s.DurationMS == 0
}

func renderForkSummary(w io.Writer, s *ForkSummary) error {
	if s == nil {
		return nil
	}
	if _, err := fmt.Fprintln(w, "Fork:"); err != nil {
		return err
	}
	for _, row := range forkSummaryRows(s) {
		if _, err := fmt.Fprintf(w, "  %s: %s\n", row.Name, row.Value); err != nil {
			return err
		}
	}
	return nil
}

type forkSummaryRow struct {
	Name  string
	Value string
}

func forkSummaryRows(s *ForkSummary) []forkSummaryRow {
	if s == nil {
		return nil
	}
	var rows []forkSummaryRow
	if source := forkSourceValue(s.SourceKind, s.SourceRef); source != "" {
		rows = append(rows, forkSummaryRow{Name: "source", Value: source})
	}
	if s.ChildName != "" {
		rows = append(rows, forkSummaryRow{Name: "child", Value: s.ChildName})
	}
	if s.ChildPath != "" {
		rows = append(rows, forkSummaryRow{Name: "child_path", Value: s.ChildPath})
	}
	if s.Mode != "" {
		rows = append(rows, forkSummaryRow{Name: "mode", Value: s.Mode})
	}
	if s.DiskReuse != "" {
		rows = append(rows, forkSummaryRow{Name: "disk_reuse", Value: s.DiskReuse})
	}
	if s.Ephemeral != nil {
		rows = append(rows, forkSummaryRow{Name: "ephemeral", Value: fmt.Sprint(*s.Ephemeral)})
	}
	if s.Keep != nil {
		rows = append(rows, forkSummaryRow{Name: "keep", Value: fmt.Sprint(*s.Keep)})
	}
	if s.Cleanup != "" {
		rows = append(rows, forkSummaryRow{Name: "cleanup", Value: s.Cleanup})
	}
	if s.Verification != "" {
		rows = append(rows, forkSummaryRow{Name: "verification", Value: s.Verification})
	}
	if s.DurationMS > 0 {
		rows = append(rows, forkSummaryRow{Name: "duration", Value: fmt.Sprintf("%dms", s.DurationMS)})
	}
	if s.Limitation != "" {
		rows = append(rows, forkSummaryRow{Name: "limitation", Value: s.Limitation})
	}
	return rows
}

func forkSourceValue(kind, ref string) string {
	switch {
	case kind == "":
		return ref
	case ref == "":
		return kind
	default:
		return kind + " " + ref
	}
}

func inferForkSourceKind(ref string) string {
	if ref == "" {
		return ""
	}
	if strings.Contains(ref, ":") {
		return "image"
	}
	return "vm"
}

func extraBoolPtr(extra map[string]any, key string) *bool {
	if extra == nil {
		return nil
	}
	v, ok := extra[key]
	if !ok {
		return nil
	}
	switch v := v.(type) {
	case bool:
		return &v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			b := true
			return &b
		case "false":
			b := false
			return &b
		default:
			return nil
		}
	default:
		return nil
	}
}
