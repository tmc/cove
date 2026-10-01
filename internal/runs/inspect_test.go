package runs

import (
	"bytes"
	"strings"
	"testing"
)

func TestInspectionRealReaderAndExport(t *testing.T) {
	record, err := LoadRecord("testdata/task-run")
	if err != nil {
		t.Fatal(err)
	}
	record.Manifest.PrimaryError = "<script>alert('guest')</script>"
	record.Manifest.CaptureErrors = []string{"capture unavailable"}
	record.Manifest.CleanupErrors = []string{"cleanup failed"}
	record.Manifest.Task.Inputs = []Input{{Name: "secret-input-name", Secret: true}}
	record.Events = append(record.Events, TaskEvent{Sequence: 99, Kind: "observation", Payload: map[string]any{"secret": "payload-secret"}, ArtifactRefs: []string{"screenshot", "../../escape"}})
	inspection := InspectRecord(record)
	for _, format := range []string{"text", "json", "html"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			if err := RenderInspection(&output, inspection, format); err != nil {
				t.Fatal(err)
			}
			text := output.String()
			for _, excluded := range []string{"secret-input-name", "payload-secret", "../../escape"} {
				if strings.Contains(text, excluded) {
					t.Fatalf("export contains %q", excluded)
				}
			}
			for _, want := range []string{"capture unavailable", "cleanup failed", "no user session", "task_failure"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %s", want, text)
				}
			}
			if format == "html" && strings.Contains(text, "<script>") {
				t.Fatal("unescaped guest HTML")
			}
		})
	}
}

func TestInspectionInterruptedAndCanceled(t *testing.T) {
	for _, tc := range []struct {
		name, outcome string
		incomplete    bool
		want          string
	}{{"interrupted", "success", true, "interrupted"}, {"canceled", "canceled", false, "canceled"}, {"cleanup", "cleanup_incomplete", false, "cleanup_incomplete"}} {
		t.Run(tc.name, func(t *testing.T) {
			got := InspectRecord(Record{Manifest: Manifest{Outcome: tc.outcome}, Incomplete: tc.incomplete, TruncatedEvents: true})
			if got.Outcome != tc.want || !got.TruncatedEvents {
				t.Fatalf("inspection=%+v", got)
			}
		})
	}
}

func TestComparisonVerifiedIdentity(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	base := func() Record {
		return Record{Manifest: Manifest{Task: &Task{Kind: "go-workspace", ImageDigest: digest, PlanDigest: digest, SourceDigest: digest, Inputs: []Input{{Name: "source", Digest: digest}}}}}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Record)
		want   bool
		reason string
	}{{"equal", func(*Record) {}, true, ""}, {"unknown", func(r *Record) { r.Manifest.Task.ImageDigest = "" }, false, "image identity unverified"}, {"changed", func(r *Record) { r.Manifest.Task.PlanDigest = "sha256:" + strings.Repeat("b", 64) }, false, "plan identities differ"}, {"secret", func(r *Record) {
		r.Manifest.Task.Inputs = append(r.Manifest.Task.Inputs, Input{Name: "TOKEN", Secret: true})
	}, false, "secret input equality"}, {"legacy", func(r *Record) { r.Manifest.Task = nil }, false, "provenance unavailable"}} {
		t.Run(tc.name, func(t *testing.T) {
			l, r := base(), base()
			tc.mutate(&r)
			comparison := CompareRecords(l, r)
			if comparison.Comparable != tc.want || tc.reason != "" && !strings.Contains(strings.Join(comparison.Reasons, ";"), tc.reason) {
				t.Fatalf("comparison=%+v", comparison)
			}
		})
	}
}
