package guibench

import (
	"bytes"
	"strings"
	"testing"
)

func sampleTasks() []*Task {
	return []*Task{
		{
			ID:        "finder-folder",
			Image:     "macos-base:v1",
			Domain:    "Finder",
			Evaluator: Evaluator{Func: StringList{"file_exists"}, Result: GetterSpec{Kind: "file", Path: "/Users/tmc/Desktop/Project"}},
		},
		{
			ID:        "safari-url",
			Image:     "macos-base:v1",
			Domain:    "Safari",
			Subset:    []string{SubsetHeldOut},
			Evaluator: Evaluator{Func: StringList{"url_in"}, Result: GetterSpec{Kind: "screen_ocr"}},
		},
		{
			ID:        "settings-theme",
			Image:     "macos-base:v1",
			Domain:    "Settings",
			Subset:    []string{SubsetTestSmall},
			Evaluator: Evaluator{Func: StringList{"plist_equals"}, Result: GetterSpec{Kind: "defaults", Domain: "-g", Key: "AppleInterfaceStyle"}},
		},
	}
}

func TestBuildManifestPartition(t *testing.T) {
	tasks := sampleTasks()
	m := BuildManifest(tasks, "abc1234")

	if m.TaskCount != 3 {
		t.Fatalf("TaskCount = %d, want 3", m.TaskCount)
	}
	if len(m.HeldOut) != 1 || m.HeldOut[0] != "safari-url" {
		t.Fatalf("HeldOut = %v, want [safari-url]", m.HeldOut)
	}
	wantPublic := []string{"finder-folder", "settings-theme"}
	if strings.Join(m.Public, ",") != strings.Join(wantPublic, ",") {
		t.Fatalf("Public = %v, want %v", m.Public, wantPublic)
	}
	if m.CoveCommit != "abc1234" {
		t.Fatalf("CoveCommit = %q, want abc1234", m.CoveCommit)
	}
	if m.CorpusVersion != CorpusVersion(tasks) || m.VerifierVersion != VerifierVersion() {
		t.Fatalf("manifest versions do not match corpus")
	}
	if !m.Matches(tasks) {
		t.Fatalf("Matches(tasks) = false, want true")
	}
}

func TestManifestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Manifest)
		wantErr string
	}{
		{"ok", func(*Manifest) {}, ""},
		{"bad schema", func(m *Manifest) { m.SchemaVersion = "v0" }, "schema version"},
		{"empty corpus", func(m *Manifest) { m.CorpusVersion = "" }, "corpus version is empty"},
		{"empty verifier", func(m *Manifest) { m.VerifierVersion = "" }, "verifier version is empty"},
		{"count mismatch", func(m *Manifest) { m.TaskCount = 99 }, "task count"},
		{"dup partition", func(m *Manifest) { m.HeldOut = append(m.HeldOut, m.Public[0]); m.TaskCount++ }, "both public and held-out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := BuildManifest(sampleTasks(), "abc1234")
			tt.mutate(&m)
			err := m.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}

func TestManifestRoundTrip(t *testing.T) {
	m := BuildManifest(sampleTasks(), "abc1234")
	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		t.Fatalf("Encode() = %v", err)
	}
	got, err := DecodeManifest(&buf)
	if err != nil {
		t.Fatalf("DecodeManifest() = %v", err)
	}
	if got.CorpusVersion != m.CorpusVersion || got.VerifierVersion != m.VerifierVersion {
		t.Fatalf("round-trip versions diverged: %+v vs %+v", got, m)
	}
	if len(got.HeldOut) != 1 || got.HeldOut[0] != "safari-url" {
		t.Fatalf("round-trip HeldOut = %v", got.HeldOut)
	}
}

func TestManifestMatchesDetectsDrift(t *testing.T) {
	tasks := sampleTasks()
	m := BuildManifest(tasks, "abc1234")
	// Mutate the corpus so the scoring shape changes: a different evaluator func.
	tasks[0].Evaluator.Func = StringList{"must_include"}
	if m.Matches(tasks) {
		t.Fatalf("Matches() = true after corpus scoring change, want false")
	}
}
