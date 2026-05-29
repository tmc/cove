package guibench

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

// SubsetHeldOut is the canonical name of the held-out partition: tasks reserved
// for maintainer-run verified scoring and never published in the public split,
// so an external submitter cannot tune against them (design 047 §8, §11). A task
// joins the held-out partition by listing this name in its Subset field, exactly
// like any other subset (see [Task.InSubset]).
const SubsetHeldOut = "held_out"

// Manifest pins a corpus version, its verifier version, and a documented
// public/held-out task partition (design 047 §6, §9 slice 6). A citable result
// references a manifest so a reader can tell which corpus and which verifier
// produced a number, and which tasks were public vs. maintainer-only.
//
// The manifest is a pure projection of a loaded corpus: [BuildManifest] derives
// every field from the tasks plus the build identity, so it cannot drift from
// the corpus it describes. It is not where gold references live — those stay in
// the verifier, host-side, unreachable from the guest (design 047 §8).
type Manifest struct {
	// SchemaVersion is the manifest format version, tracking [SchemaVersion].
	SchemaVersion string `json:"schema_version"`
	// CorpusVersion is the digest of task identities + scoring shape
	// ([CorpusVersion]). Two runs over the same corpus version are comparable.
	CorpusVersion string `json:"corpus_version"`
	// VerifierVersion is the digest of the scoring surface ([VerifierVersion]).
	VerifierVersion string `json:"verifier_version"`
	// CoveCommit is the cove build commit that produced this manifest; recorded
	// because the verifier's Go implementation is pinned by the commit, not by
	// the verifier-version surface hash (design 047 §7).
	CoveCommit string `json:"cove_commit"`
	// CreatedAt is when the manifest was built (UTC, RFC3339).
	CreatedAt string `json:"created_at"`
	// TaskCount is the total number of tasks in the corpus.
	TaskCount int `json:"task_count"`
	// Public lists the task ids in the public partition (everything not held
	// out). Sorted.
	Public []string `json:"public"`
	// HeldOut lists the task ids reserved for maintainer-run verified scoring
	// (the [SubsetHeldOut] subset). Sorted.
	HeldOut []string `json:"held_out"`
	// Domains lists the distinct task domains for per-domain aggregation.
	Domains []string `json:"domains"`
}

// BuildManifest derives a [Manifest] from a loaded corpus. coveCommit is the
// build commit (the caller supplies it from the version package). The public
// and held-out partitions are computed from each task's Subset membership:
// a task in the [SubsetHeldOut] subset is held out, every other task is public.
func BuildManifest(tasks []*Task, coveCommit string) Manifest {
	var public, heldOut []string
	for _, t := range tasks {
		if t.InSubset(SubsetHeldOut) {
			heldOut = append(heldOut, t.ID)
		} else {
			public = append(public, t.ID)
		}
	}
	sort.Strings(public)
	sort.Strings(heldOut)
	return Manifest{
		SchemaVersion:   SchemaVersion,
		CorpusVersion:   CorpusVersion(tasks),
		VerifierVersion: VerifierVersion(),
		CoveCommit:      coveCommit,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		TaskCount:       len(tasks),
		Public:          public,
		HeldOut:         heldOut,
	}.withDomains(tasks)
}

// withDomains fills the Domains field; kept separate so [BuildManifest] reads as
// a single struct literal.
func (m Manifest) withDomains(tasks []*Task) Manifest {
	m.Domains = Domains(tasks)
	return m
}

// Validate checks the manifest's structural invariants: a known schema version,
// non-empty corpus and verifier versions, a task count matching the partition
// sizes, and no task id in both partitions. It does not touch a VM.
func (m Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("manifest: schema version %q, want %q", m.SchemaVersion, SchemaVersion)
	}
	if m.CorpusVersion == "" {
		return fmt.Errorf("manifest: corpus version is empty")
	}
	if m.VerifierVersion == "" {
		return fmt.Errorf("manifest: verifier version is empty")
	}
	if got := len(m.Public) + len(m.HeldOut); got != m.TaskCount {
		return fmt.Errorf("manifest: task count %d != partition total %d", m.TaskCount, got)
	}
	seen := make(map[string]bool, m.TaskCount)
	for _, id := range m.Public {
		seen[id] = true
	}
	for _, id := range m.HeldOut {
		if seen[id] {
			return fmt.Errorf("manifest: task %q in both public and held-out partitions", id)
		}
	}
	return nil
}

// Matches reports whether the manifest describes the given corpus: same corpus
// and verifier versions. It is the runner's guard that the corpus it loaded is
// the one a manifest (and any result citing it) was pinned to.
func (m Manifest) Matches(tasks []*Task) bool {
	return m.CorpusVersion == CorpusVersion(tasks) && m.VerifierVersion == VerifierVersion()
}

// Encode writes the manifest as indented JSON.
func (m Manifest) Encode(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return nil
}

// DecodeManifest reads a manifest from r and validates it.
func DecodeManifest(r io.Reader) (Manifest, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}
