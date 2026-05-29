package guibench

import (
	"fmt"
	"strings"
)

// Before/after SQLite row-integrity metrics (design 047 §5).
//
// A single-point read ("does the title equal X?") accepts the catastrophic
// false positive AndroidWorld's validators were built to reject: an agent that
// deletes every note then re-creates the target one scores 1, even though it
// destroyed the user's data. These metrics instead compare a whole-table
// snapshot taken BEFORE the agent acts against the table AFTER, and score 1
// only when the target row was added (or removed) AND every other ("noise")
// row is byte-identical across the two snapshots — the collateral-damage check
// (AndroidWorld validate_rows_addition_integrity / validate_rows_removal_integrity).
//
// The metrics stay pure: the getter extracts each snapshot as a whole-table row
// dump (one row per line, columns the SQL joined with "|", e.g.
// SELECT id||'|'||title FROM notes ORDER BY id). The AFTER dump arrives as
// result, the BEFORE dump as expected, and the target row as the "target"
// option. Snapshot extraction (which needs a VM and a WAL checkpoint) lives in
// the getter; the comparison here never touches a VM.
//
// Capturing the BEFORE snapshot: the runner reads end-state only post-agent, so
// a task freezes the before-state during Config (setup) by copying the table
// dump to a guest file, e.g.
//
//	sqlite3 <db> "SELECT id||'|'||title FROM notes ORDER BY id" > /tmp/notes.before
//
// then points Evaluator.Expected at a file getter on /tmp/notes.before and
// Evaluator.Result at a sqlite getter that emits the same dump post-agent. The
// before file is written once in setup and never touched again, so reading it
// post-agent yields the pre-agent state. This needs no runner change and does
// not perturb the single-point metrics.

// metricRowsAddedIntegrity scores 1 iff the target row was added without side
// effects: result (the AFTER table dump) equals expected (the BEFORE dump) plus
// exactly one occurrence of the target row, and no other row changed. The
// required "target" option names the row that must appear (already in the
// getter's "|"-joined column form). It is the AndroidWorld
// validate_rows_addition_integrity check, specialized to one target row.
func metricRowsAddedIntegrity(result, expected string, options map[string]any) (float64, error) {
	target, err := stringOption(options, "target")
	if err != nil {
		return 0, fmt.Errorf("rows_added_integrity: %w", err)
	}
	if target == "" {
		return 0, fmt.Errorf("rows_added_integrity: target option is empty")
	}
	before := parseRows(expected)
	after := parseRows(result)
	return score(rowsDiffByOne(before, after, normalizeRow(target), added)), nil
}

// metricRowsRemovedIntegrity scores 1 iff the target row was removed without
// side effects: result (the AFTER table dump) equals expected (the BEFORE dump)
// minus exactly one occurrence of the target row, and no other row changed. The
// required "target" option names the row that must disappear. It is the
// AndroidWorld validate_rows_removal_integrity check, specialized to one target
// row.
func metricRowsRemovedIntegrity(result, expected string, options map[string]any) (float64, error) {
	target, err := stringOption(options, "target")
	if err != nil {
		return 0, fmt.Errorf("rows_removed_integrity: %w", err)
	}
	if target == "" {
		return 0, fmt.Errorf("rows_removed_integrity: target option is empty")
	}
	before := parseRows(expected)
	after := parseRows(result)
	return score(rowsDiffByOne(before, after, normalizeRow(target), removed)), nil
}

// rowDelta selects the direction rowsDiffByOne checks.
type rowDelta int

const (
	added   rowDelta = iota // after = before + {target}
	removed                 // after = before - {target}
)

// rowsDiffByOne reports whether the multisets before and after differ by exactly
// one occurrence of target in the given direction, with every other row
// byte-identical across the two snapshots. The whole-table multiset comparison
// is what catches collateral damage (a mutated or dropped noise row), the false
// positive a single-point read misses.
func rowsDiffByOne(before, after map[string]int, target string, dir rowDelta) bool {
	// The two snapshots must agree on every row except one extra target.
	want, got := before, after
	if dir == removed {
		want, got = after, before // after gains nothing; before has the extra target
	}
	extra := 0
	keys := map[string]bool{}
	for k := range want {
		keys[k] = true
	}
	for k := range got {
		keys[k] = true
	}
	for k := range keys {
		diff := got[k] - want[k]
		switch {
		case diff == 0:
			// Unchanged noise row: intact, the desired case.
		case k == target && diff == 1:
			extra += diff
		default:
			// Any other multiset difference is collateral damage (a noise row
			// added, removed, or mutated), or a wrong-count target change.
			return false
		}
	}
	return extra == 1
}

// parseRows reads a whole-table dump into a row multiset: one row per non-empty
// line, each line trimmed of trailing carriage-return/whitespace so a CRLF or a
// trailing-newline difference is not mistaken for a content change. The value is
// the occurrence count, so duplicate rows are compared faithfully.
func parseRows(dump string) map[string]int {
	rows := map[string]int{}
	for _, line := range strings.Split(dump, "\n") {
		row := normalizeRow(line)
		if row == "" {
			continue
		}
		rows[row]++
	}
	return rows
}

// normalizeRow trims trailing whitespace (including a stray carriage return)
// from a single row so transport-level line-ending noise does not register as a
// content difference. Leading and interior bytes are preserved: a column value
// that legitimately differs must still be caught.
func normalizeRow(line string) string {
	return strings.TrimRight(line, " \t\r")
}
