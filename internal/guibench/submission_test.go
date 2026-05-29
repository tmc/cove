package guibench

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleSubmission returns a structurally valid submission pinned to the sample
// corpus's versions, so a maintainer run can verify it.
func sampleSubmission() Submission {
	m := BuildManifest(sampleTasks(), "abc1234")
	return Submission{
		SchemaVersion:   SchemaVersion,
		Provider:        "anthropic",
		Model:           "claude-computer-use",
		AgentRef:        "git:deadbeef",
		CorpusVersion:   m.CorpusVersion,
		VerifierVersion: m.VerifierVersion,
		Tasks: []TaskResult{
			{TaskID: "finder-folder", Score: 1, Runs: []float64{1, 1, 1}},
			{TaskID: "settings-theme", Score: 0.5, Runs: []float64{1, 0, 0.5}},
		},
	}
}

func TestSubmissionValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Submission)
		wantErr string
	}{
		{"ok", func(*Submission) {}, ""},
		{"bad schema", func(s *Submission) { s.SchemaVersion = "v0" }, "schema version"},
		{"no provider", func(s *Submission) { s.Provider = "" }, "provider and model"},
		{"no versions", func(s *Submission) { s.CorpusVersion = "" }, "corpus and verifier"},
		{"no tasks", func(s *Submission) { s.Tasks = nil }, "no task results"},
		{"dup task", func(s *Submission) { s.Tasks = append(s.Tasks, s.Tasks[0]) }, "duplicate task id"},
		{"empty task id", func(s *Submission) { s.Tasks[0].TaskID = "" }, "empty id"},
		{"score out of range", func(s *Submission) { s.Tasks[0].Score = 1.5 }, "out of [0,1]"},
		{"run out of range", func(s *Submission) { s.Tasks[0].Runs = []float64{2} }, "run score"},
		{"mean mismatch", func(s *Submission) { s.Tasks[0].Runs = []float64{0, 0, 0} }, "!= mean of runs"},
		{"bad tier", func(s *Submission) { s.Tier = "trusted" }, "unknown tier"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := sampleSubmission()
			tt.mutate(&s)
			err := s.Validate()
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

func TestSubmissionOverall(t *testing.T) {
	s := sampleSubmission()
	if got := s.Overall(); got != 0.75 {
		t.Fatalf("Overall() = %v, want 0.75", got)
	}
	if got := (Submission{}).Overall(); got != 0 {
		t.Fatalf("Overall(empty) = %v, want 0", got)
	}
}

func TestStampVerified(t *testing.T) {
	m := BuildManifest(sampleTasks(), "abc1234")
	const now = "2026-05-29T00:00:00Z"

	t.Run("maintainer run matching versions verifies", func(t *testing.T) {
		got := StampVerified(sampleSubmission(), m, true, now)
		if got.Tier != TierVerified {
			t.Fatalf("tier = %q, want verified", got.Tier)
		}
		if got.VerifiedAt != now {
			t.Fatalf("VerifiedAt = %q, want %q", got.VerifiedAt, now)
		}
	})

	t.Run("self-reported run stays unverified", func(t *testing.T) {
		got := StampVerified(sampleSubmission(), m, false, now)
		if got.Tier != TierUnverified {
			t.Fatalf("tier = %q, want unverified", got.Tier)
		}
		if got.VerifiedAt != "" {
			t.Fatalf("VerifiedAt = %q, want empty for unverified", got.VerifiedAt)
		}
	})

	t.Run("maintainer run with mismatched corpus stays unverified", func(t *testing.T) {
		s := sampleSubmission()
		s.CorpusVersion = "v1:deadbeef0000"
		got := StampVerified(s, m, true, now)
		if got.Tier != TierVerified {
			// must NOT verify a result pinned to a different corpus
		} else {
			t.Fatalf("tier = verified for mismatched corpus, want unverified")
		}
	})

	t.Run("a submitter cannot self-stamp verified", func(t *testing.T) {
		s := sampleSubmission()
		s.Tier = TierVerified // submitter lies
		got := StampVerified(s, m, false, now)
		if got.Tier != TierUnverified {
			t.Fatalf("submitter self-stamp survived: tier = %q, want unverified", got.Tier)
		}
	})
}

func TestVerifyBundleStampsOnDisk(t *testing.T) {
	m := BuildManifest(sampleTasks(), "abc1234")
	dir := t.TempDir()
	writeSubmission(t, dir, sampleSubmission())

	const now = "2026-05-29T12:00:00Z"
	stamped, err := VerifyBundle(dir, m, true, now)
	if err != nil {
		t.Fatalf("VerifyBundle() = %v", err)
	}
	if stamped.Tier != TierVerified {
		t.Fatalf("stamped tier = %q, want verified", stamped.Tier)
	}

	// The bundle on disk must now carry the verified stamp.
	f, err := os.Open(filepath.Join(dir, SubmissionFile))
	if err != nil {
		t.Fatalf("open submission: %v", err)
	}
	defer f.Close()
	onDisk, err := DecodeSubmission(f)
	if err != nil {
		t.Fatalf("DecodeSubmission(on disk) = %v", err)
	}
	if onDisk.Tier != TierVerified || onDisk.VerifiedAt != now {
		t.Fatalf("on-disk submission = tier %q verifiedAt %q, want verified %q", onDisk.Tier, onDisk.VerifiedAt, now)
	}
}

func TestVerifyBundleSelfReportedStaysUnverified(t *testing.T) {
	m := BuildManifest(sampleTasks(), "abc1234")
	dir := t.TempDir()
	writeSubmission(t, dir, sampleSubmission())

	stamped, err := VerifyBundle(dir, m, false, "2026-05-29T12:00:00Z")
	if err != nil {
		t.Fatalf("VerifyBundle() = %v", err)
	}
	if stamped.Tier != TierUnverified {
		t.Fatalf("tier = %q, want unverified for non-maintainer run", stamped.Tier)
	}
}

func TestVerifyBundleMissingFile(t *testing.T) {
	m := BuildManifest(sampleTasks(), "abc1234")
	if _, err := VerifyBundle(t.TempDir(), m, true, "now"); err == nil {
		t.Fatal("VerifyBundle(empty dir) = nil, want error")
	}
}

func writeSubmission(t *testing.T, dir string, s Submission) {
	t.Helper()
	var buf bytes.Buffer
	if err := s.Encode(&buf); err != nil {
		t.Fatalf("encode submission: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, SubmissionFile), buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write submission: %v", err)
	}
}
