package runs

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/metrics"
)

func TestRunID(t *testing.T) {
	tests := []struct {
		name     string
		fallback string
		extra    map[string]any
		want     string
	}{
		{"nil extra", "fb", nil, "fb"},
		{"missing key", "fb", map[string]any{"other": "x"}, "fb"},
		{"empty value", "fb", map[string]any{"run_id": ""}, "fb"},
		{"non-string", "fb", map[string]any{"run_id": 42}, "fb"},
		{"override", "fb", map[string]any{"run_id": "abc"}, "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runID(tt.fallback, tt.extra); got != tt.want {
				t.Errorf("runID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		want  *int
	}{
		{"nil", nil, nil},
		{"missing", map[string]any{"x": 1}, nil},
		{"float exact", map[string]any{"exit_code": float64(2)}, intp(2)},
		{"float fractional", map[string]any{"exit_code": 2.5}, nil},
		{"int", map[string]any{"exit_code": 7}, intp(7)},
		{"json.Number", map[string]any{"exit_code": json.Number("9")}, intp(9)},
		{"json.Number bad", map[string]any{"exit_code": json.Number("nope")}, nil},
		{"unhandled type", map[string]any{"exit_code": "0"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exitCode(tt.extra)
			switch {
			case got == nil && tt.want == nil:
			case got == nil || tt.want == nil:
				t.Errorf("exitCode = %v, want %v", got, tt.want)
			case *got != *tt.want:
				t.Errorf("exitCode = %d, want %d", *got, *tt.want)
			}
		})
	}
}

func TestParseTime(t *testing.T) {
	if got := parseTime("not-a-time"); !got.IsZero() {
		t.Errorf("parseTime garbage = %v, want zero", got)
	}
	want := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	if got := parseTime("2026-05-09T12:00:00Z"); !got.Equal(want) {
		t.Errorf("parseTime = %v, want %v", got, want)
	}
}

func TestExtraString(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		key   string
		want  string
	}{
		{"nil", nil, "k", ""},
		{"missing", map[string]any{"a": 1}, "k", ""},
		{"string", map[string]any{"k": "v"}, "k", "v"},
		{"stringer", map[string]any{"k": stringerVal("S")}, "k", "S"},
		{"fallback fmt.Sprint", map[string]any{"k": 42}, "k", "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extraString(tt.extra, tt.key); got != tt.want {
				t.Errorf("extraString = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEventStatus(t *testing.T) {
	if got := eventStatus(metrics.Event{Status: "ok"}); got != "ok" {
		t.Errorf("eventStatus set = %q", got)
	}
	if got := eventStatus(metrics.Event{}); got != "-" {
		t.Errorf("eventStatus empty = %q, want -", got)
	}
}

func TestShortReason(t *testing.T) {
	long := strings.Repeat("a", 130)
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"  hi  ", "hi"},
		{"first\nsecond", "first"},
		{long, strings.Repeat("a", 117) + "..."},
	}
	for _, tt := range tests {
		if got := shortReason(tt.in); got != tt.want {
			t.Errorf("shortReason(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func intp(i int) *int { return &i }

type stringerVal string

func (s stringerVal) String() string { return string(s) }
