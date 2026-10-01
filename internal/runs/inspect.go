package runs

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"reflect"
)

// Inspection is a bounded projection of recorded evidence, never a replay.
// Event payloads and secret input provenance are excluded from exports.
type Inspection struct {
	VMName          string           `json:"vm_name,omitempty"`
	TaskKind        string           `json:"task_kind,omitempty"`
	ImageDigest     string           `json:"image_digest,omitempty"`
	PlanDigest      string           `json:"plan_digest,omitempty"`
	SourceDigest    string           `json:"source_digest,omitempty"`
	RunID           string           `json:"run_id"`
	AttemptID       string           `json:"attempt_id,omitempty"`
	Outcome         string           `json:"outcome"`
	Incomplete      bool             `json:"incomplete"`
	TruncatedEvents bool             `json:"truncated_events"`
	PrimaryError    string           `json:"primary_error,omitempty"`
	CaptureErrors   []string         `json:"capture_errors,omitempty"`
	CleanupErrors   []string         `json:"cleanup_errors,omitempty"`
	Retention       string           `json:"declared_retention,omitempty"`
	Route           string           `json:"guest_route,omitempty"`
	Backend         string           `json:"backend,omitempty"`
	Steps           []InspectionStep `json:"steps,omitempty"`
	Artifacts       []Artifact       `json:"artifacts,omitempty"`
}

type InspectionStep struct {
	Sequence     uint64   `json:"sequence"`
	Kind         string   `json:"kind"`
	StepID       string   `json:"step_id,omitempty"`
	Timestamp    string   `json:"timestamp"`
	Status       string   `json:"status,omitempty"`
	DurationMS   int64    `json:"duration_ms,omitempty"`
	ArtifactRefs []string `json:"artifact_refs,omitempty"`
}

func InspectRecord(record Record) Inspection {
	m := record.Manifest
	result := Inspection{VMName: m.VMName, RunID: m.RunID, AttemptID: m.AttemptID, Outcome: m.Outcome, Incomplete: record.Incomplete, TruncatedEvents: record.TruncatedEvents, PrimaryError: m.PrimaryError, CaptureErrors: append([]string(nil), m.CaptureErrors...), CleanupErrors: append([]string(nil), m.CleanupErrors...), Artifacts: append([]Artifact(nil), m.Artifacts...)}
	if record.Incomplete {
		result.Outcome = "interrupted"
	}
	if m.Task != nil {
		result.TaskKind = m.Task.Kind
		result.ImageDigest = m.Task.ImageDigest
		result.PlanDigest = m.Task.PlanDigest
		result.SourceDigest = m.Task.SourceDigest
		result.Retention = m.Task.Retention
		result.Route = m.Task.GuestRoute
		result.Backend = m.Task.Backend
	}
	names := make(map[string]bool)
	for _, artifact := range m.Artifacts {
		names[artifact.Name] = true
	}
	for _, event := range record.Events {
		step := InspectionStep{Sequence: event.Sequence, Kind: event.Kind, StepID: event.StepID, Timestamp: event.Timestamp, Status: event.Status, DurationMS: event.DurationMS}
		for _, name := range event.ArtifactRefs {
			if names[name] {
				step.ArtifactRefs = append(step.ArtifactRefs, name)
			}
		}
		result.Steps = append(result.Steps, step)
	}
	return result
}

func RenderInspection(w io.Writer, inspection Inspection, format string) error {
	if w == nil {
		return fmt.Errorf("render inspection: nil writer")
	}
	switch format {
	case "json":
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(inspection)
	case "html":
		return inspectionHTML.Execute(w, inspection)
	case "text":
		if _, err := fmt.Fprintf(w, "Run: %s attempt=%s\nOutcome: %s incomplete=%t truncated_events=%t\n", terminalText(inspection.RunID), terminalText(inspection.AttemptID), terminalText(inspection.Outcome), inspection.Incomplete, inspection.TruncatedEvents); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "Guest: %s (recorded identity; current existence unverified)\nDeclared retention: %s route=%s backend=%s\nPrimary failure: %s\n", terminalText(inspection.VMName), terminalText(inspection.Retention), terminalText(inspection.Route), terminalText(inspection.Backend), terminalText(inspection.PrimaryError)); err != nil {
			return err
		}
		for _, event := range inspection.Steps {
			if _, err := fmt.Fprintf(w, "#%d %s step=%s status=%s %dms evidence=%v\n", event.Sequence, terminalText(event.Kind), terminalText(event.StepID), terminalText(event.Status), event.DurationMS, terminalText(fmt.Sprint(event.ArtifactRefs))); err != nil {
				return err
			}
		}
		for _, artifact := range inspection.Artifacts {
			if _, err := fmt.Fprintf(w, "Evidence %s: %s reason=%s path=%s\n", terminalText(artifact.Name), terminalText(artifact.Status), terminalText(artifact.Reason), terminalText(artifact.Path)); err != nil {
				return err
			}
		}
		for _, errText := range inspection.CaptureErrors {
			if _, err := fmt.Fprintf(w, "Capture error: %s\n", terminalText(errText)); err != nil {
				return err
			}
		}
		for _, errText := range inspection.CleanupErrors {
			if _, err := fmt.Fprintf(w, "Cleanup error: %s\n", terminalText(errText)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown inspection format: %q", format)
	}
}

var inspectionHTML = template.Must(template.New("inspection").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'"><title>Run {{.RunID}}</title><style>body{font:16px system-ui;max-width:70rem;margin:2rem auto;padding:1rem}td,th{text-align:left;padding:.5rem;vertical-align:top}pre{white-space:pre-wrap;overflow-wrap:anywhere}</style><h1>Run {{.RunID}}</h1><p>Recorded history; no task is executed.</p><p>Outcome: <strong>{{.Outcome}}</strong>. Incomplete: {{.Incomplete}}. Truncated final event: {{.TruncatedEvents}}.</p><p>Guest: {{.VMName}} (recorded identity; current existence unverified). Task: {{.TaskKind}}.</p><p>Image: {{.ImageDigest}}. Plan: {{.PlanDigest}}. Source: {{.SourceDigest}}.</p><p>Declared retention: {{.Retention}}. Route: {{.Route}}. Backend: {{.Backend}}.</p><h2>Primary failure</h2><pre>{{.PrimaryError}}</pre><h2>Timeline</h2><table><tr><th>Sequence</th><th>Event / step</th><th>Result</th><th>Evidence</th></tr>{{range .Steps}}<tr><td>{{.Sequence}}</td><td>{{.Kind}} / {{.StepID}}<br>{{.Timestamp}}</td><td>{{.Status}} ({{.DurationMS}} ms)</td><td>{{range .ArtifactRefs}}{{.}}<br>{{end}}</td></tr>{{end}}</table><h2>Evidence availability</h2>{{range .Artifacts}}<p><strong>{{.Name}}</strong>: {{.Status}} {{.Reason}}</p>{{if .Path}}<pre>{{.Path}} ({{.Size}} bytes; {{.Digest}})</pre>{{end}}{{end}}<h2>Capture errors</h2>{{range .CaptureErrors}}<pre>{{.}}</pre>{{end}}<h2>Cleanup errors</h2>{{range .CleanupErrors}}<pre>{{.}}</pre>{{end}}</html>`))

// Comparison reports comparability before describing outcomes; it never ranks runs.
type Comparison struct {
	Left       Inspection `json:"left"`
	Right      Inspection `json:"right"`
	Comparable bool       `json:"comparable"`
	Reasons    []string   `json:"reasons,omitempty"`
}

func CompareRecords(left, right Record) Comparison {
	result := Comparison{Left: InspectRecord(left), Right: InspectRecord(right)}
	l, r := left.Manifest.Task, right.Manifest.Task
	if l == nil || r == nil {
		result.Reasons = []string{"task provenance unavailable"}
		return result
	}
	if l.Kind == "" || r.Kind == "" {
		result.Reasons = append(result.Reasons, "task kind unverified")
	}
	if l.Kind != r.Kind {
		result.Reasons = append(result.Reasons, "task kinds differ")
	}
	for _, field := range []struct{ name, l, r string }{{"image", l.ImageDigest, r.ImageDigest}, {"plan", l.PlanDigest, r.PlanDigest}, {"source", l.SourceDigest, r.SourceDigest}} {
		if !validDigest(field.l) || !validDigest(field.r) {
			result.Reasons = append(result.Reasons, field.name+" identity unverified")
		} else if field.l != field.r {
			result.Reasons = append(result.Reasons, field.name+" identities differ")
		}
	}
	li, ri := make(map[string]string), make(map[string]string)
	secret := false
	for _, input := range l.Inputs {
		if input.Secret {
			secret = true
			continue
		}
		if !validDigest(input.Digest) {
			result.Reasons = append(result.Reasons, "left input identity unverified")
		}
		li[input.Name] = input.Digest
	}
	for _, input := range r.Inputs {
		if input.Secret {
			secret = true
			continue
		}
		if !validDigest(input.Digest) {
			result.Reasons = append(result.Reasons, "right input identity unverified")
		}
		ri[input.Name] = input.Digest
	}
	if secret {
		result.Reasons = append(result.Reasons, "secret input equality cannot be verified")
	}
	if !reflect.DeepEqual(li, ri) {
		result.Reasons = append(result.Reasons, "declared input identities differ")
	}
	result.Comparable = len(result.Reasons) == 0
	return result
}
