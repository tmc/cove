package runs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func contractFixture(t *testing.T, manifest Manifest, events string) string {
	t.Helper()
	dir := t.TempDir()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func taskEventLine(sequence uint64, timestamp string) string {
	data, _ := json.Marshal(TaskEvent{SchemaVersion: SchemaVersion, RunID: "run", AttemptID: "attempt", Sequence: sequence, Kind: "step_completed", Timestamp: timestamp})
	return string(data) + "\n"
}

func TestLoadRecordVersionsAndOutcomes(t *testing.T) {
	for _, name := range []string{"legacy", "interrupted", "canceled", "cleanup_incomplete", "task_failure"} {
		t.Run(name, func(t *testing.T) {
			manifest := Manifest{SchemaVersion: SchemaVersion, RunID: "run", AttemptID: "attempt", StartedAt: "2026-09-30T00:00:00Z", EndedAt: "2026-09-30T00:01:00Z", ExitStatus: "ok", Outcome: "success"}
			switch name {
			case "legacy":
				manifest.SchemaVersion = 0
				manifest.Outcome = ""
			case "interrupted":
				manifest.EndedAt = ""
				manifest.Outcome = "success"
			default:
				manifest.Outcome = name
			}
			record, err := LoadRecord(contractFixture(t, manifest, "{\"event\":\"old\"}\n"))
			if err != nil {
				t.Fatal(err)
			}
			want := name
			if name == "legacy" {
				want = "success"
			}
			if record.Manifest.Outcome != want || record.Incomplete != (name == "interrupted") || record.LegacyEventCount != 1 {
				t.Fatalf("record = %+v", record)
			}
		})
	}
}

func TestLoadRecordEventIntegrity(t *testing.T) {
	first := taskEventLine(1, "2026-09-30T03:00:00Z")
	tests := []struct {
		name, events       string
		wantErr, truncated bool
	}{
		{"clock-skew", first + taskEventLine(2, "2026-09-30T01:00:00Z"), false, false},
		{"partial-tail", first + "{\"schema_version\":1,", false, true},
		{"complete-without-newline", strings.TrimSuffix(first, "\n"), false, false},
		{"corrupt-earlier", "{\"schema_version\":1,\n" + first, true, false},
		{"corrupt-tail", first + "not json", true, false},
		{"duplicate-sequence", first + first, true, false},
		{"oversized", first + strings.Repeat("x", MaxRecordBytes+1), true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := contractFixture(t, Manifest{SchemaVersion: SchemaVersion, RunID: "run", AttemptID: "attempt"}, tt.events)
			record, err := LoadRecord(dir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, tt.wantErr)
			}
			if err == nil && record.TruncatedEvents != tt.truncated {
				t.Fatalf("truncated = %t", record.TruncatedEvents)
			}
		})
	}
}

func TestLoadRecordArtifactBoundsAndDigest(t *testing.T) {
	body := []byte("redacted diagnostic")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	for _, name := range []string{"present", "unavailable", "failed", "corrupt-digest", "escape", "absolute", "symlink", "oversized", "wrong-size"} {
		t.Run(name, func(t *testing.T) {
			artifact := Artifact{Name: "capture", Status: "present", Path: "capture.txt", Size: int64(len(body)), Digest: digest}
			switch name {
			case "unavailable", "failed":
				artifact = Artifact{Name: "capture", Status: name, Reason: "no user session"}
			case "corrupt-digest":
				artifact.Digest = "sha256:" + strings.Repeat("0", 64)
			case "escape":
				artifact.Path = "../capture.txt"
			case "absolute":
				artifact.Path = "/capture.txt"
			case "oversized":
				artifact.Size = MaxArtifactBytes + 1
			case "wrong-size":
				artifact.Size++
			}
			dir := contractFixture(t, Manifest{SchemaVersion: SchemaVersion, RunID: "run", Artifacts: []Artifact{artifact}}, "")
			if err := os.WriteFile(filepath.Join(dir, "capture.txt"), body, 0600); err != nil {
				t.Fatal(err)
			}
			if name == "symlink" {
				outside := filepath.Join(t.TempDir(), "capture.txt")
				if err := os.WriteFile(outside, body, 0600); err != nil {
					t.Fatal(err)
				}
				os.Remove(filepath.Join(dir, "capture.txt"))
				if err := os.Symlink(outside, filepath.Join(dir, "capture.txt")); err != nil {
					t.Fatal(err)
				}
			}
			_, err := LoadRecord(dir)
			wantErr := name != "present" && name != "unavailable" && name != "failed"
			if (err != nil) != wantErr {
				t.Fatalf("error = %v, wantErr %t", err, wantErr)
			}
		})
	}
}

func TestTaskProvenanceRejectsSecretDigest(t *testing.T) {
	task := Task{Kind: "recipe", Inputs: []Input{{Name: "API_TOKEN", Secret: true, Digest: "sha256:" + strings.Repeat("a", 64)}}}
	if err := ValidateTask(task); err == nil {
		t.Fatal("accepted secret digest")
	}
	task.Inputs[0].Digest = ""
	if err := ValidateTask(task); err != nil {
		t.Fatal(err)
	}
}

func TestLoadShowTaskOnlyBundle(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "run")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{SchemaVersion: SchemaVersion, RunID: "run", AttemptID: "attempt", Outcome: "task_failure", EndedAt: "2026-09-30T01:00:00Z", PrimaryError: "compile failed", CleanupErrors: []string{"retain guest failed"}, Task: &Task{Kind: "recipe", Retention: "retain"}, Artifacts: []Artifact{{Name: "AX", Status: "unavailable", Reason: "permission denied"}}}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600)
	os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(taskEventLine(1, "2026-09-30T01:00:00Z")), 0600)
	show, err := LoadShow(root, "run")
	if err != nil {
		t.Fatal(err)
	}
	if show.Result.Status != "task_failure" || show.Failure.Reason != "compile failed" {
		t.Fatalf("show = %+v", show)
	}
	var output bytes.Buffer
	if err := RenderShow(&output, show); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"compile failed", "retain guest failed", "permission denied", "#1 step_completed"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %q: %s", want, output.String())
		}
	}
}

func TestTaskRecordTextEscapesControls(t *testing.T) {
	var output bytes.Buffer
	record := Record{Manifest: Manifest{RunID: "run", Outcome: "task_failure", Task: &Task{Kind: "recipe\x1b[2J"}, CaptureErrors: []string{"failed\nforged status"}}, Events: []TaskEvent{{Sequence: 1, Kind: "step_failed", StepID: "build\rpass"}}}
	if err := renderTaskRecord(&output, record); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "\x1b") || strings.Contains(output.String(), "\nforged status") || strings.Contains(output.String(), "\rpass") {
		t.Fatalf("controls rendered: %q", output.String())
	}
	if !strings.Contains(output.String(), "\\x1b") || !strings.Contains(output.String(), "\\nforged status") {
		t.Fatalf("escaped text missing: %q", output.String())
	}
}

func TestVersionedListPreservesMetricsAndIncompleteAuthority(t *testing.T) {
	for _, name := range []string{"complete", "incomplete"} {
		t.Run(name, func(t *testing.T) {
			manifest := Manifest{SchemaVersion: SchemaVersion, RunID: "run", StartedAt: "2026-09-30T00:00:00Z", EndedAt: "2026-09-30T00:01:00Z", Outcome: "success"}
			if name == "incomplete" {
				manifest.EndedAt = ""
			}
			dir := contractFixture(t, manifest, "")
			metric := `{"timestamp":"2026-09-30T00:01:00Z","event_type":"run_complete","duration_ms":1234,"status":"ok","extra":{"exit_code":0}}` + "\n"
			if err := os.WriteFile(filepath.Join(dir, "metrics.jsonl"), []byte(metric), 0600); err != nil {
				t.Fatal(err)
			}
			summary, found, err := readRun(dir, "fallback")
			if err != nil || !found {
				t.Fatalf("summary=%+v found=%t error=%v", summary, found, err)
			}
			want := "ok"
			if name == "incomplete" {
				want = "interrupted"
			}
			if summary.Status != want || summary.TotalDurationMS != 1234 || summary.ExitCode == nil || *summary.ExitCode != 0 || summary.EventCount != 1 {
				t.Fatalf("summary=%+v", summary)
			}
		})
	}
}
