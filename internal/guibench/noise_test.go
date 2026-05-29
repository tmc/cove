package guibench

import (
	"reflect"
	"testing"
)

func TestNoiseRowsDeterministic(t *testing.T) {
	pools := [][]string{
		noiseIntColumn(10, 5),
		{"Groceries", "Standup agenda", "Reading list", "Gym plan"},
	}

	a := NoiseRows(7, 3, pools)
	b := NoiseRows(7, 3, pools)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("same seed gave different rows:\n a=%v\n b=%v", a, b)
	}
	if len(a) != 3 {
		t.Fatalf("got %d rows, want 3", len(a))
	}
	for _, rec := range a {
		if len(rec.Fields) != len(pools) {
			t.Fatalf("record %v has %d fields, want %d", rec, len(rec.Fields), len(pools))
		}
	}

	// A different seed should (very likely) differ given the pool size.
	c := NoiseRows(8, 3, pools)
	if reflect.DeepEqual(a, c) {
		t.Fatalf("distinct seeds gave identical rows: %v", a)
	}
}

func TestNoiseRowsEdgeCases(t *testing.T) {
	pools := [][]string{{"a"}, {"b"}}
	if got := NoiseRows(1, 0, pools); got != nil {
		t.Fatalf("n=0 gave %v, want nil", got)
	}
	if got := NoiseRows(1, -3, pools); got != nil {
		t.Fatalf("n<0 gave %v, want nil", got)
	}
	if got := NoiseRows(1, 2, nil); got != nil {
		t.Fatalf("no pools gave %v, want nil", got)
	}

	// An empty column pool yields an empty value for that column.
	got := NoiseRows(1, 1, [][]string{{}, {"x"}})
	if len(got) != 1 || got[0].Fields[0] != "" || got[0].Fields[1] != "x" {
		t.Fatalf("empty-pool column = %v, want first field empty", got)
	}
}

func TestNoiseRecordRow(t *testing.T) {
	r := NoiseRecord{Fields: []string{"3", "Standup agenda"}}
	if got := r.Row(); got != "3|Standup agenda" {
		t.Fatalf("Row() = %q, want %q", got, "3|Standup agenda")
	}
}

func TestNoiseSQLValues(t *testing.T) {
	pools := [][]string{
		noiseIntColumn(1, 2),
		{"don't", "plain"},
	}
	// Determinism: the rendered INSERT tail is stable for a seed.
	a := NoiseSQLValues(42, 2, pools)
	b := NoiseSQLValues(42, 2, pools)
	if a != b {
		t.Fatalf("non-deterministic SQL values:\n a=%q\n b=%q", a, b)
	}
	if a == "" {
		t.Fatalf("got empty SQL values for n=2")
	}
	// Each rendered tuple is parenthesized with quoted fields, and a single
	// quote in a value is doubled so it cannot break out of the literal.
	target := NoiseSQLValues(0, 1, [][]string{{"don't"}})
	if target != "('don''t')" {
		t.Fatalf("quote escaping = %q, want %q", target, "('don''t')")
	}
	if got := NoiseSQLValues(1, 0, pools); got != "" {
		t.Fatalf("n=0 SQL values = %q, want empty", got)
	}
}

func TestNoiseIntColumn(t *testing.T) {
	if got := noiseIntColumn(5, 3); !reflect.DeepEqual(got, []string{"5", "6", "7"}) {
		t.Fatalf("noiseIntColumn(5,3) = %v, want [5 6 7]", got)
	}
	if got := noiseIntColumn(1, 0); got != nil {
		t.Fatalf("noiseIntColumn(1,0) = %v, want nil", got)
	}
}

// TestNoiseRowsFeedIntegrity wires the noise helper to the integrity metric: a
// seeded noise set forms the BEFORE table; an added target forms the AFTER
// table; the metric scores 1, then 0 once a noise row is mutated — the full
// "seed distractors, then verify the agent left them alone" loop.
func TestNoiseRowsFeedIntegrity(t *testing.T) {
	pools := [][]string{
		noiseIntColumn(1, 4),
		{"Groceries", "Standup agenda", "Reading list", "Gym plan"},
	}
	noise := NoiseRows(99, 3, pools)

	var beforeLines []string
	for _, rec := range noise {
		beforeLines = append(beforeLines, rec.Row())
	}
	before := joinLines(beforeLines)
	const target = "9|Trip itinerary"
	after := before + "\n" + target

	add := Metrics()["rows_added_integrity"]
	got, err := add(after, before, map[string]any{"target": target})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1 {
		t.Fatalf("score = %v, want 1 (target added onto seeded noise, noise intact)", got)
	}

	// Mutate the first noise row in the AFTER snapshot: collateral damage.
	damagedLines := append([]string(nil), beforeLines...)
	damagedLines[0] = damagedLines[0] + " (edited)"
	damaged := joinLines(damagedLines) + "\n" + target
	got, err = add(damaged, before, map[string]any{"target": target})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 0 {
		t.Fatalf("score = %v, want 0 (a seeded noise row was mutated)", got)
	}
}

// joinLines joins rows with newlines, the whole-table dump form parseRows reads.
func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
