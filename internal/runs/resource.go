package runs

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/metrics"
)

const (
	resourceSampleEvent         = "resource_sample"
	guestLowMemoryPercent       = 10
	guestHighLoadAvg1           = 4
	guestHotProcessCPUPercent   = 80
	guestHighProcessCount       = 512
	maxResourceSummaryByteValue = uint64(1<<63 - 1)
)

// ResourceSummary is a derived view over resource_sample events.
type ResourceSummary struct {
	SampleCount                    int                     `json:"sample_count"`
	MinGuestMemoryAvailableBytes   *uint64                 `json:"min_guest_memory_available_bytes,omitempty"`
	MinGuestMemoryAvailablePercent *float64                `json:"min_guest_memory_available_percent,omitempty"`
	PeakGuestLoadAvg1              *float64                `json:"peak_guest_load_avg_1,omitempty"`
	PeakGuestProcessCount          *uint64                 `json:"peak_guest_process_count,omitempty"`
	TopGuestProcess                *ResourceProcessSummary `json:"top_guest_process,omitempty"`
	PeakHostCPUPercent             *float64                `json:"peak_host_cpu_percent,omitempty"`
	PeakHostRSSBytes               *uint64                 `json:"peak_host_rss_bytes,omitempty"`
	Warnings                       []ResourceWarning       `json:"warnings,omitempty"`
}

// ResourceProcessSummary identifies the hottest guest process seen in samples.
type ResourceProcessSummary struct {
	PID        uint64  `json:"pid"`
	CPUPercent float64 `json:"cpu_percent,omitempty"`
	RSSBytes   uint64  `json:"rss_bytes,omitempty"`
	Command    string  `json:"command,omitempty"`
	Phase      string  `json:"phase,omitempty"`
}

// ResourceWarning is a best-effort pressure hint derived from sampled metrics.
type ResourceWarning struct {
	Class  string `json:"class"`
	Reason string `json:"reason"`
}

func summarizeResources(events []metrics.Event) *ResourceSummary {
	var s ResourceSummary
	for _, e := range events {
		if e.EventType != resourceSampleEvent {
			continue
		}
		s.SampleCount++
		phase := extraString(e.Extra, "phase")
		if available, ok := resourceUint(e.Extra, "memory_available_bytes"); ok {
			total, okTotal := resourceUint(e.Extra, "memory_total_bytes")
			updateMinGuestMemory(&s, available, total, okTotal && total > 0)
		}
		if load, ok := resourceFloat(e.Extra, "guest_load_avg_1"); ok {
			setMaxFloat(&s.PeakGuestLoadAvg1, load)
		}
		if count, ok := resourceUint(e.Extra, "guest_process_count"); ok {
			setMaxUint(&s.PeakGuestProcessCount, count)
		}
		if proc, ok := resourceTopProcess(e.Extra, phase); ok && resourceProcessBetter(proc, s.TopGuestProcess) {
			s.TopGuestProcess = &proc
		}
		if cpu, ok := resourceFloat(e.Extra, "host_cpu_percent"); ok {
			setMaxFloat(&s.PeakHostCPUPercent, cpu)
		}
		if rss, ok := resourceUint(e.Extra, "host_rss_bytes"); ok {
			setMaxUint(&s.PeakHostRSSBytes, rss)
		}
	}
	if s.SampleCount == 0 {
		return nil
	}
	s.Warnings = resourceWarnings(&s)
	return &s
}

func updateMinGuestMemory(s *ResourceSummary, available, total uint64, hasTotal bool) {
	if s.MinGuestMemoryAvailableBytes != nil && available > *s.MinGuestMemoryAvailableBytes {
		return
	}
	if s.MinGuestMemoryAvailableBytes != nil && available == *s.MinGuestMemoryAvailableBytes && s.MinGuestMemoryAvailablePercent != nil {
		return
	}
	availableCopy := available
	s.MinGuestMemoryAvailableBytes = &availableCopy
	s.MinGuestMemoryAvailablePercent = nil
	if hasTotal {
		percent := 100 * float64(available) / float64(total)
		s.MinGuestMemoryAvailablePercent = &percent
	}
}

func renderResourceSummary(w io.Writer, s *ResourceSummary) error {
	if s == nil {
		return nil
	}
	if _, err := fmt.Fprintln(w, "Resources:"); err != nil {
		return err
	}
	for _, row := range resourceSummaryRows(s) {
		if _, err := fmt.Fprintf(w, "  %s: %s\n", row.Name, row.Value); err != nil {
			return err
		}
	}
	if len(s.Warnings) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "  hints:"); err != nil {
		return err
	}
	for _, warning := range s.Warnings {
		if _, err := fmt.Fprintf(w, "    %s: %s\n", warning.Class, warning.Reason); err != nil {
			return err
		}
	}
	return nil
}

type resourceSummaryRow struct {
	Name  string
	Value string
}

func resourceSummaryRows(s *ResourceSummary) []resourceSummaryRow {
	if s == nil {
		return nil
	}
	rows := []resourceSummaryRow{{Name: "samples", Value: fmt.Sprint(s.SampleCount)}}
	if s.MinGuestMemoryAvailableBytes != nil {
		value := formatResourceBytes(*s.MinGuestMemoryAvailableBytes)
		if s.MinGuestMemoryAvailablePercent != nil {
			value += fmt.Sprintf(" (%.1f%%)", *s.MinGuestMemoryAvailablePercent)
		}
		rows = append(rows, resourceSummaryRow{Name: "guest_memory_min_available", Value: value})
	}
	if s.PeakGuestLoadAvg1 != nil {
		rows = append(rows, resourceSummaryRow{Name: "guest_load_avg_1_peak", Value: fmt.Sprintf("%.2f", *s.PeakGuestLoadAvg1)})
	}
	if s.PeakGuestProcessCount != nil {
		rows = append(rows, resourceSummaryRow{Name: "guest_processes_peak", Value: fmt.Sprint(*s.PeakGuestProcessCount)})
	}
	if s.TopGuestProcess != nil {
		p := s.TopGuestProcess
		value := fmt.Sprintf("pid=%d cpu=%.1f%%", p.PID, p.CPUPercent)
		if p.RSSBytes > 0 {
			value += " rss=" + formatResourceBytes(p.RSSBytes)
		}
		if p.Command != "" {
			value = p.Command + " " + value
		}
		if p.Phase != "" {
			value += " phase=" + p.Phase
		}
		rows = append(rows, resourceSummaryRow{Name: "guest_top_process", Value: value})
	}
	if s.PeakHostCPUPercent != nil {
		rows = append(rows, resourceSummaryRow{Name: "host_cpu_percent_peak", Value: fmt.Sprintf("%.1f%%", *s.PeakHostCPUPercent)})
	}
	if s.PeakHostRSSBytes != nil {
		rows = append(rows, resourceSummaryRow{Name: "host_rss_peak", Value: formatResourceBytes(*s.PeakHostRSSBytes)})
	}
	return rows
}

func resourceWarnings(s *ResourceSummary) []ResourceWarning {
	var warnings []ResourceWarning
	if s.MinGuestMemoryAvailableBytes != nil && s.MinGuestMemoryAvailablePercent != nil && *s.MinGuestMemoryAvailablePercent < guestLowMemoryPercent {
		warnings = append(warnings, ResourceWarning{
			Class:  "guest_memory_low",
			Reason: fmt.Sprintf("minimum available memory was %s (%.1f%%)", formatResourceBytes(*s.MinGuestMemoryAvailableBytes), *s.MinGuestMemoryAvailablePercent),
		})
	}
	if s.PeakGuestLoadAvg1 != nil && *s.PeakGuestLoadAvg1 >= guestHighLoadAvg1 {
		warnings = append(warnings, ResourceWarning{
			Class:  "guest_load_high",
			Reason: fmt.Sprintf("peak guest 1-minute load average was %.2f", *s.PeakGuestLoadAvg1),
		})
	}
	if s.TopGuestProcess != nil && s.TopGuestProcess.CPUPercent >= guestHotProcessCPUPercent {
		command := s.TopGuestProcess.Command
		if command == "" {
			command = fmt.Sprintf("pid %d", s.TopGuestProcess.PID)
		}
		warnings = append(warnings, ResourceWarning{
			Class:  "guest_process_hot",
			Reason: fmt.Sprintf("%s reached %.1f%% CPU", command, s.TopGuestProcess.CPUPercent),
		})
	}
	if s.PeakGuestProcessCount != nil && *s.PeakGuestProcessCount >= guestHighProcessCount {
		warnings = append(warnings, ResourceWarning{
			Class:  "guest_process_count_high",
			Reason: fmt.Sprintf("guest process count peaked at %d", *s.PeakGuestProcessCount),
		})
	}
	return warnings
}

func resourceTopProcess(extra map[string]any, phase string) (ResourceProcessSummary, bool) {
	raw, ok := extra["guest_top_processes"]
	if !ok {
		return ResourceProcessSummary{}, false
	}
	var best ResourceProcessSummary
	found := false
	switch items := raw.(type) {
	case []any:
		for _, item := range items {
			if p, ok := resourceProcess(item, phase); ok && (!found || resourceProcessBetter(p, &best)) {
				best = p
				found = true
			}
		}
	case []map[string]any:
		for _, item := range items {
			if p, ok := resourceProcess(item, phase); ok && (!found || resourceProcessBetter(p, &best)) {
				best = p
				found = true
			}
		}
	}
	return best, found
}

func resourceProcess(raw any, phase string) (ResourceProcessSummary, bool) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return ResourceProcessSummary{}, false
	}
	pid, ok := resourceUint(obj, "pid")
	if !ok || pid == 0 {
		return ResourceProcessSummary{}, false
	}
	p := ResourceProcessSummary{PID: pid, Phase: phase}
	if cpu, ok := resourceFloat(obj, "cpu_percent", "cpuPercent"); ok {
		p.CPUPercent = cpu
	}
	if rss, ok := resourceUint(obj, "rss_bytes", "rssBytes"); ok {
		p.RSSBytes = rss
	}
	if command, ok := obj["command"].(string); ok {
		p.Command = strings.TrimSpace(command)
	}
	return p, true
}

func resourceProcessBetter(candidate ResourceProcessSummary, current *ResourceProcessSummary) bool {
	if current == nil {
		return true
	}
	if candidate.CPUPercent != current.CPUPercent {
		return candidate.CPUPercent > current.CPUPercent
	}
	if candidate.RSSBytes != current.RSSBytes {
		return candidate.RSSBytes > current.RSSBytes
	}
	return candidate.PID < current.PID
}

func resourceFloat(extra map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		v, ok := extra[key]
		if !ok {
			continue
		}
		if f, ok := resourceNumberFloat(v); ok {
			return f, true
		}
	}
	return 0, false
}

func resourceUint(extra map[string]any, keys ...string) (uint64, bool) {
	for _, key := range keys {
		v, ok := extra[key]
		if !ok {
			continue
		}
		if u, ok := resourceNumberUint(v); ok {
			return u, true
		}
	}
	return 0, false
}

func resourceNumberFloat(v any) (float64, bool) {
	switch v := v.(type) {
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case float32:
		f := float64(v)
		return f, !math.IsNaN(f) && !math.IsInf(f, 0)
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	default:
		return 0, false
	}
}

func resourceNumberUint(v any) (uint64, bool) {
	switch v := v.(type) {
	case uint64:
		return v, true
	case uint:
		return uint64(v), true
	case uint32:
		return uint64(v), true
	case int:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int64:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case int32:
		if v < 0 {
			return 0, false
		}
		return uint64(v), true
	case float64:
		if v < 0 || math.Trunc(v) != v || math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return uint64(v), true
	case json.Number:
		u, err := strconv.ParseUint(v.String(), 10, 64)
		return u, err == nil
	case string:
		u, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		return u, err == nil
	default:
		return 0, false
	}
}

func setMinFloat(dst **float64, v float64) {
	if *dst == nil || v < **dst {
		vv := v
		*dst = &vv
	}
}

func setMaxFloat(dst **float64, v float64) {
	if *dst == nil || v > **dst {
		vv := v
		*dst = &vv
	}
}

func setMinUint(dst **uint64, v uint64) {
	if *dst == nil || v < **dst {
		vv := v
		*dst = &vv
	}
}

func setMaxUint(dst **uint64, v uint64) {
	if *dst == nil || v > **dst {
		vv := v
		*dst = &vv
	}
}

func formatResourceBytes(v uint64) string {
	if v > maxResourceSummaryByteValue {
		return fmt.Sprintf("%d B", v)
	}
	return bytefmt.Size(int64(v))
}
