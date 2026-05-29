package guibench_test

import (
	"fmt"

	"github.com/tmc/cove/internal/guibench"
)

// ExampleTaskEgress shows the contamination defense (design 047 §8): a task
// with no network allowlist runs fully offline during scoring, so the agent
// cannot wget the gold reference into the path the verifier checks.
func ExampleTaskEgress() {
	denyAll := guibench.TaskEgress(&guibench.Task{ID: "finder-folder"})
	fmt.Println("deny-all:", denyAll.DenyAll(), denyAll.Permits("huggingface.co"))

	allowed := guibench.TaskEgress(&guibench.Task{
		ID:           "wiki-note",
		NetworkAllow: []string{"wikipedia.org"},
	})
	fmt.Println("allowlist:", allowed.Permits("en.wikipedia.org"), allowed.Permits("huggingface.co"))
	// Output:
	// deny-all: true false
	// allowlist: true false
}

// ExampleStampVerified shows the verified-tier rule (design 047 §11): only a
// maintainer-executed run with matching corpus + verifier versions is stamped
// verified; a self-reported number stays unverified.
func ExampleStampVerified() {
	tasks := []*guibench.Task{{
		ID:        "t1",
		Image:     "macos-base:v1",
		Evaluator: guibench.Evaluator{Func: guibench.StringList{"file_exists"}, Result: guibench.GetterSpec{Kind: "file", Path: "/x"}},
	}}
	m := guibench.BuildManifest(tasks, "abc1234")

	sub := guibench.Submission{
		SchemaVersion:   guibench.SchemaVersion,
		Provider:        "anthropic",
		Model:           "claude-computer-use",
		CorpusVersion:   m.CorpusVersion,
		VerifierVersion: m.VerifierVersion,
		Tasks:           []guibench.TaskResult{{TaskID: "t1", Score: 1}},
	}

	maintainer := guibench.StampVerified(sub, m, true, "2026-05-29T00:00:00Z")
	selfReport := guibench.StampVerified(sub, m, false, "2026-05-29T00:00:00Z")
	fmt.Println("maintainer:", maintainer.Tier)
	fmt.Println("self-report:", selfReport.Tier)
	// Output:
	// maintainer: verified
	// self-report: unverified
}
