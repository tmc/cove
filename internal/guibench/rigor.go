package guibench

import (
	"sort"
	"strconv"
	"strings"
)

// TaskRigor is the verification-rigor provenance of one scored task: the
// guarantees the harness enforced while producing its number (design 047 §16,
// the "OSWorld-Verified" brand play). trycua and the other GUI-agent
// leaderboards publish a bare success rate and are silent on all four of these,
// so a reader cannot tell a rigorously-isolated, egress-locked, FDA/AX-verified
// number from a best-effort one. The benchmark makes them first-class columns.
//
// A TaskRigor is a pure projection of a [Task]: [RigorOf] derives every field
// from the task's evaluator and egress policy, so it cannot drift from the task
// it describes. It records what the harness guarantees structurally (every task
// runs in a fresh RAM-overlay fork, so [Isolation] is constant), not what a
// given run observed.
type TaskRigor struct {
	// Isolation is the reset discipline the task ran under. Every task runs in a
	// fresh per-task RAM-overlay fork that is discarded after scoring (design 047
	// §6, §12), so this is constant; it is published so the column is explicit
	// rather than assumed.
	Isolation string `json:"isolation"`
	// EgressPolicy is the network-egress decision the task ran under: the
	// [EgressLockdown] policy name ("offline" for deny-all, "task-allow" for an
	// allowlisted task).
	EgressPolicy string `json:"egress_policy"`
	// EgressAllow lists the exact domains an allowlisted task could reach; empty
	// for a deny-all (offline) task. Sorted and de-duplicated by [TaskEgress].
	EgressAllow []string `json:"egress_allow,omitempty"`
	// Tier is the privilege tier the task's getters required (A/B/C, design 047
	// §5): the highest tier across the result and expected getters.
	Tier Tier `json:"tier"`
	// Flushes lists the pre-read flushes the runner ran before scoring this task
	// (design 047 §7), in stable order. cfprefsd always runs; a WAL checkpoint is
	// added for every SQLite db a getter reads. This is the discipline that keeps
	// the verifier from reading stale async state — OSWorld's largest verifier-bug
	// class.
	Flushes []FlushKind `json:"flushes"`
}

// isolationForkPerTask is the constant isolation guarantee: a fresh RAM-overlay
// fork per task, discarded after scoring (design 047 §6).
const isolationForkPerTask = "fork-per-task (RAM overlay, discarded)"

// RigorOf derives the verification-rigor provenance of a task. It is pure: it
// reads the task's evaluator (for the privilege tier and the SQLite flushes the
// runner will run) and its egress policy, and reports the constant fork-per-task
// isolation guarantee. It matches the flushes [runFlushes] actually runs, so the
// published metadata is the discipline the runner enforced, not an aspiration.
func RigorOf(t *Task) TaskRigor {
	egress := TaskEgress(t)
	return TaskRigor{
		Isolation:    isolationForkPerTask,
		EgressPolicy: egress.PolicyName,
		EgressAllow:  egress.Allow,
		Tier:         taskTier(t),
		Flushes:      taskFlushes(t),
	}
}

// taskTier returns the highest privilege tier across a task's getters, the same
// rule [MaxTier] applies across a corpus.
func taskTier(t *Task) Tier {
	tier := t.Evaluator.Result.Tier()
	if t.Evaluator.Expected != nil {
		if e := t.Evaluator.Expected.Tier(); e > tier {
			tier = e
		}
	}
	return tier
}

// taskFlushes returns the pre-read flushes the runner runs for the task, in the
// order [runFlushes] runs them: cfprefsd always, then a WAL checkpoint per
// distinct SQLite db a getter reads.
func taskFlushes(t *Task) []FlushKind {
	out := []FlushKind{FlushCfprefsd}
	for range sqlitePaths(t) {
		out = append(out, FlushWAL)
	}
	return out
}

// RigorSummary is the corpus-level rigor rollup published in the citable report:
// the one-line "100% egress-locked, N Tier-C AX-verified, all persisted-state
// getters flush before read" claim a reader can check against the per-task
// columns. It is derived purely from the per-task rigor of the scored tasks.
type RigorSummary struct {
	// Tasks is the number of distinct tasks the summary covers.
	Tasks int `json:"tasks"`
	// EgressLocked is how many tasks ran fully offline (deny-all); EgressAllowlisted
	// is how many ran under a named allowlist. They sum to Tasks.
	EgressLocked      int `json:"egress_locked"`
	EgressAllowlisted int `json:"egress_allowlisted"`
	// TierCounts is the per-tier task count keyed by tier label ("A"/"B"/"C").
	TierCounts map[string]int `json:"tier_counts"`
	// FlushesAllTasks reports whether every task flushed cfprefsd before reading
	// (always true under the current runner); WALCheckpointTasks is how many tasks
	// additionally checkpointed a SQLite WAL.
	FlushesAllTasks    bool `json:"flushes_all_tasks"`
	WALCheckpointTasks int  `json:"wal_checkpoint_tasks"`
	// Isolation is the constant per-task isolation guarantee.
	Isolation string `json:"isolation"`
}

// SummarizeRigor rolls a set of per-task rigor records into a corpus-level
// [RigorSummary]. Duplicate task ids (e.g. one rigor record per outcome) are
// counted once, so passing every outcome's rigor yields a per-task summary.
func SummarizeRigor(byTask map[string]TaskRigor) RigorSummary {
	s := RigorSummary{
		TierCounts:      map[string]int{},
		FlushesAllTasks: true,
		Isolation:       isolationForkPerTask,
	}
	for _, r := range byTask {
		s.Tasks++
		if len(r.EgressAllow) == 0 {
			s.EgressLocked++
		} else {
			s.EgressAllowlisted++
		}
		s.TierCounts[string(r.Tier)]++
		if !hasFlush(r.Flushes, FlushCfprefsd) {
			s.FlushesAllTasks = false
		}
		if hasFlush(r.Flushes, FlushWAL) {
			s.WALCheckpointTasks++
		}
	}
	if s.Tasks == 0 {
		s.FlushesAllTasks = false
	}
	return s
}

// Headline renders the corpus rigor summary as the one-line citable claim. It
// reads "N tasks: 100% egress-locked, T Tier-C ..., all persisted-state getters
// flush before read" so a reader gets the rigor stance at a glance.
func (s RigorSummary) Headline() string {
	if s.Tasks == 0 {
		return "no tasks scored"
	}
	var parts []string
	if s.EgressLocked == s.Tasks {
		parts = append(parts, "100% egress-locked")
	} else {
		parts = append(parts, pluralTasks(s.EgressLocked, "egress-locked")+
			", "+pluralTasks(s.EgressAllowlisted, "allowlisted"))
	}
	parts = append(parts, s.tierClause())
	if s.FlushesAllTasks {
		flush := "all tasks flush cfprefsd before read"
		if s.WALCheckpointTasks > 0 {
			flush += "; " + pluralTasks(s.WALCheckpointTasks, "checkpoint a SQLite WAL")
		}
		parts = append(parts, flush)
	}
	return pluralTasks(s.Tasks, "scored") + ": " + strings.Join(parts, "; ")
}

// tierClause renders the per-tier distribution in A,B,C order, omitting empty
// tiers, e.g. "12 Tier-A verified, 3 Tier-B verified, 1 Tier-C verified".
func (s RigorSummary) tierClause() string {
	var parts []string
	for _, tier := range []Tier{TierA, TierB, TierC} {
		if n := s.TierCounts[string(tier)]; n > 0 {
			parts = append(parts, pluralTasks(n, "Tier-"+string(tier)+" verified"))
		}
	}
	if len(parts) == 0 {
		return "no privilege tiers"
	}
	return strings.Join(parts, ", ")
}

// pluralTasks renders "N <noun>" where N is a task count, e.g. "3 scored".
func pluralTasks(n int, noun string) string {
	return strconv.Itoa(n) + " " + noun
}

// hasFlush reports whether kind is in the flush list.
func hasFlush(flushes []FlushKind, kind FlushKind) bool {
	for _, f := range flushes {
		if f == kind {
			return true
		}
	}
	return false
}

// sortedFlushKinds returns flush kinds as sorted strings, for stable rendering.
func sortedFlushKinds(flushes []FlushKind) []string {
	out := make([]string, 0, len(flushes))
	for _, f := range flushes {
		out = append(out, string(f))
	}
	sort.Strings(out)
	return out
}
