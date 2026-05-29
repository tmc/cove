package guibench

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Tier marks the trust level of a submitted result (design 047 §11). Only a
// maintainer-executed run of submitted agent code, with corpus and verifier
// versions pinned, is citable as verified; a self-reported number is unverified
// and dismissed for headline claims (the XLANG model: aggregators carry vendor
// self-reports, but only maintainer-run numbers count).
type ResultTier string

const (
	// TierUnverified is a self-reported submission. It is accepted into the
	// data model but never cited as a headline number.
	TierUnverified ResultTier = "unverified"
	// TierVerified is a maintainer-executed run with pinned corpus + verifier.
	// Only [StampVerified] sets this, and only when the bundle validates.
	TierVerified ResultTier = "verified"
)

// SubmissionFile is the canonical filename of a result submission inside a
// bundle directory.
const SubmissionFile = "submission.json"

// Submission is the result-submission format an external party files (design
// 047 §9 slice 6). It records what was run (provider/model, agent ref), against
// what (corpus + verifier versions, task ids), and the resulting scores. It is
// pure data: validating it never touches a VM.
//
// The Tier field is authoritative only when set by the maintainer pipeline via
// [StampVerified]; a submitter's own claim of "verified" is ignored — a fresh
// submission is [TierUnverified] until a maintainer run stamps it.
type Submission struct {
	// SchemaVersion tracks the submission format ([SchemaVersion]).
	SchemaVersion string `json:"schema_version"`
	// Tier is the trust level; see [StampVerified]. A submitter should leave
	// this unset or "unverified"; [VerifyBundle] overwrites it.
	Tier ResultTier `json:"tier"`
	// Provider and Model identify the agent under test (e.g. "anthropic",
	// "claude-...-computer-use").
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// AgentRef pins the submitted agent code a maintainer re-runs to verify
	// (a git ref, container digest, or release tag).
	AgentRef string `json:"agent_ref"`
	// CorpusVersion and VerifierVersion pin what was scored (design 047 §7).
	// They MUST match the manifest a verified run uses.
	CorpusVersion   string `json:"corpus_version"`
	VerifierVersion string `json:"verifier_version"`
	// CoveCommit is the cove build that ran the scoring (empty for a
	// self-reported submission produced outside cove).
	CoveCommit string `json:"cove_commit,omitempty"`
	// Host describes the hardware the run executed on (design 047 §7).
	Host string `json:"host,omitempty"`
	// SubmittedAt is when the submission was filed (RFC3339).
	SubmittedAt string `json:"submitted_at,omitempty"`
	// VerifiedAt is set by [StampVerified] when a maintainer run validates it.
	VerifiedAt string `json:"verified_at,omitempty"`
	// Tasks are the per-task results. Each task id must appear at most once.
	Tasks []TaskResult `json:"tasks"`
}

// TaskResult is one task's outcome within a [Submission]. Score is the mean over
// Runs of the per-run [0,1] scores; Runs records the individual pass@1 scores so
// variance is recoverable (design 047 §11: pass@1 over >=3 runs, >20%-variance
// cells flagged).
type TaskResult struct {
	TaskID string    `json:"task_id"`
	Score  float64   `json:"score"`
	Runs   []float64 `json:"runs,omitempty"`
}

// Validate checks the submission's structural invariants without touching a VM:
// a known schema version, required identity fields, in-range scores, no
// duplicate task ids, and a self-consistent mean. It does NOT decide the tier;
// that is [StampVerified]'s job.
func (s Submission) Validate() error {
	if s.SchemaVersion != SchemaVersion {
		return fmt.Errorf("submission: schema version %q, want %q", s.SchemaVersion, SchemaVersion)
	}
	if s.Provider == "" || s.Model == "" {
		return fmt.Errorf("submission: provider and model are required")
	}
	if s.CorpusVersion == "" || s.VerifierVersion == "" {
		return fmt.Errorf("submission: corpus and verifier versions are required")
	}
	if len(s.Tasks) == 0 {
		return fmt.Errorf("submission: no task results")
	}
	seen := make(map[string]bool, len(s.Tasks))
	for _, tr := range s.Tasks {
		if tr.TaskID == "" {
			return fmt.Errorf("submission: task result with empty id")
		}
		if seen[tr.TaskID] {
			return fmt.Errorf("submission: duplicate task id %q", tr.TaskID)
		}
		seen[tr.TaskID] = true
		if tr.Score < 0 || tr.Score > 1 {
			return fmt.Errorf("submission: task %s score %v out of [0,1]", tr.TaskID, tr.Score)
		}
		for _, r := range tr.Runs {
			if r < 0 || r > 1 {
				return fmt.Errorf("submission: task %s run score %v out of [0,1]", tr.TaskID, r)
			}
		}
		if len(tr.Runs) > 0 && !approxEqual(tr.Score, meanf(tr.Runs)) {
			return fmt.Errorf("submission: task %s score %v != mean of runs %v", tr.TaskID, tr.Score, meanf(tr.Runs))
		}
	}
	switch s.Tier {
	case "", TierUnverified, TierVerified:
	default:
		return fmt.Errorf("submission: unknown tier %q", s.Tier)
	}
	return nil
}

// Overall is the corpus-level success rate: the mean of the per-task scores.
// It returns 0 for an empty submission.
func (s Submission) Overall() float64 {
	if len(s.Tasks) == 0 {
		return 0
	}
	var sum float64
	for _, tr := range s.Tasks {
		sum += tr.Score
	}
	return sum / float64(len(s.Tasks))
}

// VerifyBundle validates a submitted result bundle on disk and stamps its tier.
//
// dir must contain a [SubmissionFile]. The submission is validated, then checked
// against the manifest the maintainer pinned for this run: a result whose
// corpus or verifier version does not match the manifest cannot be verified.
// When maintainerRun is true (a maintainer-executed run of the submitted agent
// code) and the versions match, the bundle is stamped [TierVerified]; otherwise
// it is stamped [TierUnverified]. The stamped submission is written back to
// dir/submission.json. It returns the stamped submission.
//
// A self-reported number is therefore never silently promoted: only a maintainer
// run over a matching manifest yields a verified stamp (design 047 §11).
func VerifyBundle(dir string, manifest Manifest, maintainerRun bool, now string) (Submission, error) {
	path := filepath.Join(dir, SubmissionFile)
	f, err := os.Open(path)
	if err != nil {
		return Submission{}, fmt.Errorf("verify bundle: %w", err)
	}
	sub, err := DecodeSubmission(f)
	f.Close()
	if err != nil {
		return Submission{}, fmt.Errorf("verify bundle %s: %w", path, err)
	}
	if err := manifest.Validate(); err != nil {
		return Submission{}, fmt.Errorf("verify bundle: manifest: %w", err)
	}
	stamped := StampVerified(sub, manifest, maintainerRun, now)
	out := filepath.Join(dir, SubmissionFile)
	wf, err := os.Create(out)
	if err != nil {
		return Submission{}, fmt.Errorf("verify bundle: write %s: %w", out, err)
	}
	if err := stamped.Encode(wf); err != nil {
		wf.Close()
		return Submission{}, err
	}
	if err := wf.Close(); err != nil {
		return Submission{}, fmt.Errorf("verify bundle: close %s: %w", out, err)
	}
	return stamped, nil
}

// StampVerified is the pure tier decision: it returns a copy of sub with Tier
// set. The result is [TierVerified] only when maintainerRun is true and the
// submission's corpus and verifier versions match the manifest; otherwise it is
// [TierUnverified]. now (RFC3339) is recorded as VerifiedAt for a verified
// stamp. This is the unit-testable core of [VerifyBundle].
func StampVerified(sub Submission, manifest Manifest, maintainerRun bool, now string) Submission {
	out := sub
	out.VerifiedAt = ""
	verified := maintainerRun &&
		sub.CorpusVersion == manifest.CorpusVersion &&
		sub.VerifierVersion == manifest.VerifierVersion
	if verified {
		out.Tier = TierVerified
		out.VerifiedAt = now
	} else {
		out.Tier = TierUnverified
	}
	return out
}

// Encode writes the submission as indented JSON.
func (s Submission) Encode(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return fmt.Errorf("encode submission: %w", err)
	}
	return nil
}

// DecodeSubmission reads and validates a submission from r.
func DecodeSubmission(r io.Reader) (Submission, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var s Submission
	if err := dec.Decode(&s); err != nil {
		return Submission{}, fmt.Errorf("decode submission: %w", err)
	}
	if err := s.Validate(); err != nil {
		return Submission{}, err
	}
	return s, nil
}

// approxEqual reports whether a and b are within a small tolerance, so a stored
// mean and a recomputed mean agree despite float rounding.
func approxEqual(a, b float64) bool {
	const eps = 1e-9
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}

// meanf returns the arithmetic mean of xs, or 0 for an empty slice.
func meanf(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// HeldOutNames returns the held-out task ids from a manifest, sorted, for a
// maintainer who runs the verified tier over the reserved partition.
func HeldOutNames(m Manifest) []string {
	out := append([]string(nil), m.HeldOut...)
	sort.Strings(out)
	return out
}
