package guibench

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// SchemaVersion is the task-schema + scoring-contract version. Bump it when the
// task JSON shape, the conjunction semantics, or the [0,1] score contract
// changes, so an old score.json is never silently compared against a new
// verifier (design 047 §7: "corpus and verifier are versioned together").
const SchemaVersion = "v1"

// VerifierVersion is a stable digest of the scoring surface: the schema
// version, the registered metric names, and the supported getter kinds. A
// citable result records this hash, so a reader can tell whether two numbers
// were produced by the same verifier — the bench/README.md rule that the
// verifier hash is recorded because "verifier brittleness means the corpus must
// be versioned and the verifier hash recorded" (§7).
//
// The hash is over the SORTED metric and getter names plus the schema version,
// so it is independent of map iteration order and stable across builds. It does
// NOT hash the Go implementation bytes (those are pinned by the cove commit a
// result already records); it captures the verifier's externally observable
// surface, which is what determines whether two runs are comparable.
func VerifierVersion() string {
	metrics := sortedKeys(metricNames())
	getters := getterKindNames()
	var b strings.Builder
	fmt.Fprintf(&b, "schema=%s\n", SchemaVersion)
	fmt.Fprintf(&b, "metrics=%s\n", strings.Join(metrics, ","))
	fmt.Fprintf(&b, "getters=%s\n", strings.Join(getters, ","))
	sum := sha256.Sum256([]byte(b.String()))
	return SchemaVersion + ":" + hex.EncodeToString(sum[:])[:12]
}

// CorpusVersion is a stable digest of the corpus's task identities and verifier
// shape. It hashes, for each task in id order, the task id, image, domain, and
// evaluator func+conj — the fields that decide what is scored and how — so two
// runs over the same corpus version are directly comparable, and a corpus edit
// that changes scoring produces a new version. It does not hash instruction
// prose (an instruction reword does not change the gold answer).
//
// A result records both the version and the participating task ids (§7); use
// [TaskIDs] for the latter.
func CorpusVersion(tasks []*Task) string {
	ordered := make([]*Task, len(tasks))
	copy(ordered, tasks)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var b strings.Builder
	fmt.Fprintf(&b, "verifier=%s\n", VerifierVersion())
	for _, t := range ordered {
		fmt.Fprintf(&b, "task=%s image=%s domain=%s func=%s conj=%s\n",
			t.ID, t.Image, t.Domain, strings.Join(t.Evaluator.Func, "+"), t.Evaluator.Conj)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return SchemaVersion + ":" + hex.EncodeToString(sum[:])[:12]
}

// TaskIDs returns the sorted ids of the tasks, for recording the exact corpus
// participation of a result.
func TaskIDs(tasks []*Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	sort.Strings(out)
	return out
}

// Domains returns the sorted distinct domains present in the tasks. Tasks with
// an empty domain contribute the sentinel "(none)" so an unlabeled task still
// aggregates into a visible bucket rather than vanishing.
func Domains(tasks []*Task) []string {
	seen := make(map[string]bool)
	for _, t := range tasks {
		seen[domainOf(t)] = true
	}
	return sortedKeys(seen)
}

// domainOf returns the task's domain, mapping the empty domain to a visible
// sentinel.
func domainOf(t *Task) string {
	if t.Domain == "" {
		return "(none)"
	}
	return t.Domain
}

// sortedKeys returns the keys of a set in sorted order.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
