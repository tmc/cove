package guibench

import (
	"strings"
	"testing"
)

// taskWith builds a minimal valid task with the given evaluator getter(s) and
// network allowlist, for rigor derivation tests.
func taskWith(id string, result GetterSpec, expected *GetterSpec, allow ...string) *Task {
	return &Task{
		ID:           id,
		Instruction:  "do the thing",
		NetworkAllow: allow,
		Evaluator: Evaluator{
			Func:     StringList{"exact_match"},
			Result:   result,
			Expected: expected,
		},
	}
}

func TestRigorOf(t *testing.T) {
	tests := []struct {
		name        string
		task        *Task
		wantTier    Tier
		wantEgress  string
		wantAllow   []string
		wantFlushes []FlushKind
	}{
		{
			name:        "tier A exec, deny-all, cfprefsd only",
			task:        taskWith("exec-1", GetterSpec{Kind: "exec", Args: []string{"echo", "hi"}}, nil),
			wantTier:    TierA,
			wantEgress:  "offline",
			wantFlushes: []FlushKind{FlushCfprefsd},
		},
		{
			name:        "tier B sqlite adds WAL flush",
			task:        taskWith("sql-1", GetterSpec{Kind: "sqlite", Path: "/db.sqlite", Query: "SELECT 1"}, nil),
			wantTier:    TierB,
			wantEgress:  "offline",
			wantFlushes: []FlushKind{FlushCfprefsd, FlushWAL},
		},
		{
			name:        "tier C accessibility, allowlisted",
			task:        taskWith("ax-1", GetterSpec{Kind: "accessibility", App: "Notes", Attr: "value"}, nil, "wikipedia.org"),
			wantTier:    TierC,
			wantEgress:  "task-allow",
			wantAllow:   []string{"wikipedia.org"},
			wantFlushes: []FlushKind{FlushCfprefsd},
		},
		{
			name:        "expected getter raises tier",
			task:        taskWith("mix-1", GetterSpec{Kind: "exec", Args: []string{"echo"}}, &GetterSpec{Kind: "applescript", Script: "1"}),
			wantTier:    TierC,
			wantEgress:  "offline",
			wantFlushes: []FlushKind{FlushCfprefsd},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := RigorOf(tt.task)
			if r.Tier != tt.wantTier {
				t.Errorf("tier = %q, want %q", r.Tier, tt.wantTier)
			}
			if r.EgressPolicy != tt.wantEgress {
				t.Errorf("egress policy = %q, want %q", r.EgressPolicy, tt.wantEgress)
			}
			if !equalStrings(r.EgressAllow, tt.wantAllow) {
				t.Errorf("egress allow = %v, want %v", r.EgressAllow, tt.wantAllow)
			}
			if !equalFlushes(r.Flushes, tt.wantFlushes) {
				t.Errorf("flushes = %v, want %v", r.Flushes, tt.wantFlushes)
			}
			if r.Isolation != isolationForkPerTask {
				t.Errorf("isolation = %q, want %q", r.Isolation, isolationForkPerTask)
			}
		})
	}
}

// TestRigorMatchesRunnerFlushes guards the rigor headline against drifting from
// the flushes the runner actually runs: RigorOf must claim a WAL checkpoint
// exactly when runFlushes would run one (i.e. when the task reads a SQLite db).
func TestRigorMatchesRunnerFlushes(t *testing.T) {
	sqlTask := taskWith("sql", GetterSpec{Kind: "sqlite", Path: "/a.db", Query: "SELECT 1"}, nil)
	if got := taskFlushes(sqlTask); !equalFlushes(got, []FlushKind{FlushCfprefsd, FlushWAL}) {
		t.Errorf("sqlite task flushes = %v, want [cfprefsd wal]", got)
	}
	// sqlitePaths is what runFlushes iterates; rigor must match it exactly.
	if len(sqlitePaths(sqlTask)) != 1 {
		t.Errorf("sqlitePaths = %d, want 1", len(sqlitePaths(sqlTask)))
	}
	nonSQL := taskWith("exec", GetterSpec{Kind: "exec", Args: []string{"echo"}}, nil)
	if got := taskFlushes(nonSQL); !equalFlushes(got, []FlushKind{FlushCfprefsd}) {
		t.Errorf("exec task flushes = %v, want [cfprefsd]", got)
	}
}

func TestSummarizeRigor(t *testing.T) {
	byTask := map[string]TaskRigor{
		"a": RigorOf(taskWith("a", GetterSpec{Kind: "exec", Args: []string{"echo"}}, nil)),
		"b": RigorOf(taskWith("b", GetterSpec{Kind: "sqlite", Path: "/x.db", Query: "SELECT 1"}, nil)),
		"c": RigorOf(taskWith("c", GetterSpec{Kind: "accessibility", App: "Notes", Attr: "value"}, nil, "apple.com")),
	}
	s := SummarizeRigor(byTask)
	if s.Tasks != 3 {
		t.Fatalf("tasks = %d, want 3", s.Tasks)
	}
	if s.EgressLocked != 2 || s.EgressAllowlisted != 1 {
		t.Errorf("egress locked/allowlisted = %d/%d, want 2/1", s.EgressLocked, s.EgressAllowlisted)
	}
	if s.TierCounts["A"] != 1 || s.TierCounts["B"] != 1 || s.TierCounts["C"] != 1 {
		t.Errorf("tier counts = %v, want A:1 B:1 C:1", s.TierCounts)
	}
	if !s.FlushesAllTasks {
		t.Errorf("FlushesAllTasks = false, want true (every task flushes cfprefsd)")
	}
	if s.WALCheckpointTasks != 1 {
		t.Errorf("WALCheckpointTasks = %d, want 1 (only the sqlite task)", s.WALCheckpointTasks)
	}
}

func TestRigorSummaryHeadline(t *testing.T) {
	// All deny-all, mixed tiers, one WAL: the headline must read 100% locked.
	byTask := map[string]TaskRigor{
		"a": RigorOf(taskWith("a", GetterSpec{Kind: "exec", Args: []string{"echo"}}, nil)),
		"b": RigorOf(taskWith("b", GetterSpec{Kind: "sqlite", Path: "/x.db", Query: "SELECT 1"}, nil)),
	}
	h := SummarizeRigor(byTask).Headline()
	for _, want := range []string{"2 scored", "100% egress-locked", "Tier-A verified", "Tier-B verified", "flush cfprefsd", "SQLite WAL"} {
		if !strings.Contains(h, want) {
			t.Errorf("headline %q missing %q", h, want)
		}
	}

	// Empty summary is explicit, not a misleading 100%.
	if got := (RigorSummary{}).Headline(); got != "no tasks scored" {
		t.Errorf("empty headline = %q, want %q", got, "no tasks scored")
	}
}

// TestAggregateStampsRigor confirms that outcomes carrying rigor produce cells
// and a report summary with that rigor, and that rigorless outcomes (hand-built
// fixtures) leave the summary nil so the report omits the section.
func TestAggregateStampsRigor(t *testing.T) {
	axRigor := RigorOf(taskWith("ax", GetterSpec{Kind: "accessibility", App: "Notes", Attr: "value"}, nil))
	sqlRigor := RigorOf(taskWith("sql", GetterSpec{Kind: "sqlite", Path: "/x.db", Query: "SELECT 1"}, nil))
	outcomes := []Outcome{
		{Provider: "claude", TaskID: "ax", Run: 0, Score: 1, Status: StatusScored, Rigor: &axRigor},
		{Provider: "claude", TaskID: "ax", Run: 1, Score: 1, Status: StatusScored, Rigor: &axRigor},
		{Provider: "claude", TaskID: "sql", Run: 0, Score: 0, Status: StatusScored, Rigor: &sqlRigor},
		{Provider: "gpt", TaskID: "ax", Run: 0, Score: 0, Status: StatusScored, Rigor: &axRigor},
	}
	rep, err := Aggregate(outcomes, 2, testMeta(), nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if rep.Rigor == nil {
		t.Fatal("report rigor summary is nil, want populated")
	}
	if rep.Rigor.Tasks != 2 {
		t.Errorf("rigor summary tasks = %d, want 2 (ax + sql, counted once)", rep.Rigor.Tasks)
	}
	if rep.Rigor.TierCounts["C"] != 1 || rep.Rigor.TierCounts["B"] != 1 {
		t.Errorf("tier counts = %v, want C:1 B:1", rep.Rigor.TierCounts)
	}
	// Every cell scoring the ax task must carry Tier-C rigor.
	for _, c := range rep.Cells {
		if c.TaskID == "ax" {
			if c.Rigor == nil || c.Rigor.Tier != TierC {
				t.Errorf("ax cell rigor = %+v, want Tier-C", c.Rigor)
			}
		}
	}

	// Rigorless outcomes -> nil summary, omitted section.
	bare := []Outcome{{Provider: "claude", TaskID: "t", Run: 0, Score: 1, Status: StatusScored}}
	bareRep, err := Aggregate(bare, 1, testMeta(), nil)
	if err != nil {
		t.Fatalf("Aggregate bare: %v", err)
	}
	if bareRep.Rigor != nil {
		t.Errorf("bare report rigor = %+v, want nil", bareRep.Rigor)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalFlushes(a, b []FlushKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
