package guibench

import (
	"bytes"
	"strings"
	"testing"
)

// calibratedReport builds a rigorless two-task report and attaches a
// CalibrationSummary, so the rendered report exercises the calibration section
// without depending on the rigor fixture.
func calibratedReport(t *testing.T, results []SelfCheckResult) *ScoreReport {
	t.Helper()
	rep := sampleReport(t) // outcomes carry no rigor; isolate the calibration path
	cal := SummarizeCalibration(results)
	rep.Calibration = &cal
	return rep
}

func TestRenderMarkdownCalibration(t *testing.T) {
	rep := calibratedReport(t, []SelfCheckResult{
		{TaskID: "a", Seed: 5, OK: true},
		{TaskID: "b", Seed: 5, Good: 1, NoOp: 1, OK: false},
	})
	var buf bytes.Buffer
	if err := RenderMarkdown(&buf, rep); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	out := buf.String()

	mustContain := []string{
		"## Verifier calibration",
		"1/2 verifier-calibrated",
		"| Miscalibrated | 1 / 2 |",
		"| Self-check seed | 5 |",
	}
	for _, sub := range mustContain {
		if !strings.Contains(out, sub) {
			t.Errorf("calibration markdown missing %q\n---\n%s", sub, out)
		}
	}
}

// TestRenderMarkdownNoCalibrationOmitsSection guards that a report aggregated
// without a self-check pass prints no empty calibration section.
func TestRenderMarkdownNoCalibrationOmitsSection(t *testing.T) {
	rep := sampleReport(t) // no Calibration attached
	var buf bytes.Buffer
	if err := RenderMarkdown(&buf, rep); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	if strings.Contains(buf.String(), "## Verifier calibration") {
		t.Errorf("calibrationless report rendered a Verifier calibration section:\n%s", buf.String())
	}
}

// TestReportCalibrationJSONRoundTrip confirms calibration survives the
// score.json round trip and renders identically.
func TestReportCalibrationJSONRoundTrip(t *testing.T) {
	rep := calibratedReport(t, []SelfCheckResult{{TaskID: "a", Seed: 9, OK: true}})
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	got, err := ReadReport(&buf)
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	if got.Calibration == nil || got.Calibration.Verified != rep.Calibration.Verified {
		t.Fatalf("calibration summary not preserved: %+v", got.Calibration)
	}
	var a, b bytes.Buffer
	if err := RenderMarkdown(&a, rep); err != nil {
		t.Fatal(err)
	}
	if err := RenderMarkdown(&b, got); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Error("calibration markdown differs after JSON round-trip")
	}
}
