package guibench

import (
	"math"
	"testing"
)

func TestRowsIntegrityMetrics(t *testing.T) {
	// A populated notes table: two seeded noise rows the agent must leave intact.
	const before = "2|Groceries\n3|Standup agenda"
	// Target row the add task asks the agent to create.
	const targetAdd = "4|Trip itinerary"

	tests := []struct {
		name    string
		metric  string
		result  string // AFTER table dump
		before  string // BEFORE table dump (passed as expected)
		options map[string]any
		want    float64
		wantErr bool
	}{
		// rows_added_integrity
		{
			name:    "added target, noise intact",
			metric:  "rows_added_integrity",
			before:  before,
			result:  before + "\n" + targetAdd,
			options: map[string]any{"target": targetAdd},
			want:    1,
		},
		{
			name:    "added target but a noise row mutated",
			metric:  "rows_added_integrity",
			before:  before,
			result:  "2|Groceries\n3|Standup notes\n" + targetAdd, // "agenda"->"notes" collateral damage
			options: map[string]any{"target": targetAdd},
			want:    0,
		},
		{
			name:    "added target but a noise row deleted",
			metric:  "rows_added_integrity",
			before:  before,
			result:  "2|Groceries\n" + targetAdd, // dropped row 3
			options: map[string]any{"target": targetAdd},
			want:    0,
		},
		{
			name:    "target not added",
			metric:  "rows_added_integrity",
			before:  before,
			result:  before, // nothing happened
			options: map[string]any{"target": targetAdd},
			want:    0,
		},
		{
			name:    "added wrong row, target absent",
			metric:  "rows_added_integrity",
			before:  before,
			result:  before + "\n5|Wrong note",
			options: map[string]any{"target": targetAdd},
			want:    0,
		},
		{
			name:    "deleted everything then recreated target (the false positive)",
			metric:  "rows_added_integrity",
			before:  before,
			result:  targetAdd, // wiped both noise rows, kept only target
			options: map[string]any{"target": targetAdd},
			want:    0,
		},
		{
			name:    "added target onto empty table",
			metric:  "rows_added_integrity",
			before:  "",
			result:  targetAdd,
			options: map[string]any{"target": targetAdd},
			want:    1,
		},
		{
			name:    "added target twice (wrong count)",
			metric:  "rows_added_integrity",
			before:  before,
			result:  before + "\n" + targetAdd + "\n" + targetAdd,
			options: map[string]any{"target": targetAdd},
			want:    0,
		},
		{
			name:    "trailing newline difference tolerated",
			metric:  "rows_added_integrity",
			before:  before + "\n",
			result:  before + "\n" + targetAdd + "\n",
			options: map[string]any{"target": targetAdd},
			want:    1,
		},
		{
			name:    "added missing target option",
			metric:  "rows_added_integrity",
			before:  before,
			result:  before + "\n" + targetAdd,
			options: nil,
			wantErr: true,
		},
		{
			name:    "added bad target option type",
			metric:  "rows_added_integrity",
			before:  before,
			result:  before + "\n" + targetAdd,
			options: map[string]any{"target": 7},
			wantErr: true,
		},

		// rows_removed_integrity
		{
			name:    "removed target, noise intact",
			metric:  "rows_removed_integrity",
			before:  before + "\n4|Trip itinerary",
			result:  before,
			options: map[string]any{"target": "4|Trip itinerary"},
			want:    1,
		},
		{
			name:    "removed target but a noise row mutated",
			metric:  "rows_removed_integrity",
			before:  before + "\n4|Trip itinerary",
			result:  "2|Groceries\n3|Standup notes", // mutated row 3
			options: map[string]any{"target": "4|Trip itinerary"},
			want:    0,
		},
		{
			name:    "removed target plus a noise row (over-deletion)",
			metric:  "rows_removed_integrity",
			before:  before + "\n4|Trip itinerary",
			result:  "2|Groceries", // also dropped row 3
			options: map[string]any{"target": "4|Trip itinerary"},
			want:    0,
		},
		{
			name:    "target not removed",
			metric:  "rows_removed_integrity",
			before:  before + "\n4|Trip itinerary",
			result:  before + "\n4|Trip itinerary",
			options: map[string]any{"target": "4|Trip itinerary"},
			want:    0,
		},
		{
			name:    "deleted everything (the false positive)",
			metric:  "rows_removed_integrity",
			before:  before + "\n4|Trip itinerary",
			result:  "", // wiped the whole table
			options: map[string]any{"target": "4|Trip itinerary"},
			want:    0,
		},
		{
			name:    "removed last remaining target",
			metric:  "rows_removed_integrity",
			before:  "4|Trip itinerary",
			result:  "",
			options: map[string]any{"target": "4|Trip itinerary"},
			want:    1,
		},
		{
			name:    "removed missing target option",
			metric:  "rows_removed_integrity",
			before:  before + "\n4|Trip itinerary",
			result:  before,
			options: map[string]any{},
			wantErr: true,
		},
	}

	registry := Metrics()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, ok := registry[tt.metric]
			if !ok {
				t.Fatalf("metric %q not registered", tt.metric)
			}
			got, err := m(tt.result, tt.before, tt.options)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got score %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseRows(t *testing.T) {
	tests := []struct {
		name string
		dump string
		want map[string]int
	}{
		{"empty", "", map[string]int{}},
		{"whitespace only", "  \n\t\n", map[string]int{}},
		{"single", "1|a", map[string]int{"1|a": 1}},
		{"crlf and trailing", "1|a\r\n2|b\n", map[string]int{"1|a": 1, "2|b": 1}},
		{"duplicate rows counted", "1|a\n1|a", map[string]int{"1|a": 2}},
		{"trailing spaces trimmed", "1|a   \n1|a", map[string]int{"1|a": 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRows(tt.dump)
			if len(got) != len(tt.want) {
				t.Fatalf("parseRows(%q) = %v, want %v", tt.dump, got, tt.want)
			}
			for k, n := range tt.want {
				if got[k] != n {
					t.Fatalf("parseRows(%q)[%q] = %d, want %d", tt.dump, k, got[k], n)
				}
			}
		})
	}
}

// TestRowsIntegrityViaGetters exercises the end-to-end snapshot flow with a
// FakeProbe: a sqlite getter emits the AFTER dump, a file getter (reading the
// snapshot file written during setup) emits the BEFORE dump, and the integrity
// metric scores them — proving the "before file written in Config, read
// post-agent" mechanism works without a runner change.
func TestRowsIntegrityViaGetters(t *testing.T) {
	const dbPath = "/Users/tmc/Library/Group Containers/group.com.apple.notes/NoteStore.sqlite"
	const beforeFile = "/tmp/notes.before"
	const query = "SELECT id||'|'||title FROM notes ORDER BY id"
	const before = "2|Groceries\n3|Standup agenda"
	const target = "4|Trip itinerary"
	after := before + "\n" + target

	probe := FakeProbe{
		Files: map[string]string{
			beforeFile: before, // frozen during Config, read post-agent
		},
		Commands: map[string]ExecResult{
			"sqlite3 " + dbPath + " PRAGMA wal_checkpoint(FULL);\n" + query: {
				ExitCode: 0,
				// getSQLite drops the leading checkpoint row; emit one then the rows.
				Stdout: "0|0|0\n" + after + "\n",
			},
		},
	}

	task := &Task{
		ID:          "notes-add-integrity",
		Image:       "macos-base:v1",
		Instruction: "Add a note titled Trip itinerary.",
		Complexity:  2,
		Evaluator: Evaluator{
			Func:     StringList{"rows_added_integrity"},
			Result:   GetterSpec{Kind: "sqlite", Path: dbPath, Query: query},
			Expected: &GetterSpec{Kind: "file", Path: beforeFile},
			Options:  map[string]any{"target": target},
		},
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got, err := Evaluate(probe, task, nil, "")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if got != 1 {
		t.Fatalf("score = %v, want 1 (target added, noise intact)", got)
	}

	// Same getters, but the agent wiped the noise rows: the integrity metric
	// must catch the collateral damage and score 0.
	probe.Commands["sqlite3 "+dbPath+" PRAGMA wal_checkpoint(FULL);\n"+query] = ExecResult{
		ExitCode: 0,
		Stdout:   "0|0|0\n" + target + "\n", // only the target survived
	}
	got, err = Evaluate(probe, task, nil, "")
	if err != nil {
		t.Fatalf("evaluate (damaged): %v", err)
	}
	if got != 0 {
		t.Fatalf("score = %v, want 0 (noise rows destroyed)", got)
	}
}
