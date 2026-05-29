package guibench

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sampleTask() *Task {
	return &Task{
		ID:          "settings-appearance",
		Image:       "macos-base:v1",
		Instruction: "Set the display appearance to {THEME}.",
		Complexity:  1,
		Schema: []Param{
			{Name: "THEME", Pool: []string{"Dark", "Light"}},
			{
				Name:         "EXPECTED",
				ExpectedFrom: "THEME",
				Derive:       map[string]string{"Dark": "1", "Light": "0"},
			},
		},
		Evaluator: Evaluator{
			Func:   StringList{"plist_equals"},
			Result: GetterSpec{Kind: "defaults", Domain: "-g", Key: "AppleInterfaceStyle"},
		},
	}
}

func TestParamsDeterministic(t *testing.T) {
	task := sampleTask()

	// Same seed => identical materialization.
	a := task.Params(7)
	b := task.Params(7)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("same seed diverged: %v vs %v", a, b)
	}

	// Derived param tracks the chosen input.
	if want := map[string]string{"Dark": "1", "Light": "0"}[a["THEME"]]; a["EXPECTED"] != want {
		t.Fatalf("EXPECTED = %q, want %q for THEME=%q", a["EXPECTED"], want, a["THEME"])
	}

	// Across the seed space we observe both pool values (determinism, not
	// constancy).
	seen := map[string]bool{}
	for seed := uint64(0); seed < 32; seed++ {
		seen[task.Params(seed)["THEME"]] = true
	}
	if !seen["Dark"] || !seen["Light"] {
		t.Fatalf("seed sweep did not cover both pool values: %v", seen)
	}
}

func TestParamsTaskIDMix(t *testing.T) {
	// Two tasks with the same schema and seed should be able to diverge,
	// because the task id is mixed into the PRNG.
	t1 := sampleTask()
	t2 := sampleTask()
	t2.ID = "settings-appearance-2"
	diverged := false
	for seed := uint64(0); seed < 32; seed++ {
		if t1.Params(seed)["THEME"] != t2.Params(seed)["THEME"] {
			diverged = true
			break
		}
	}
	if !diverged {
		t.Fatalf("distinct task ids never diverged across seed sweep")
	}
}

func TestMaterialize(t *testing.T) {
	params := map[string]string{"THEME": "Dark", "NOTE_TITLE": "Trip"}
	tests := []struct {
		name     string
		template string
		want     string
	}{
		{"single", "appearance {THEME}", "appearance Dark"},
		{"two", "{NOTE_TITLE}: {THEME}", "Trip: Dark"},
		{"unknown left", "{UNKNOWN}", "{UNKNOWN}"},
		{"no braces", "plain", "plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Materialize(tt.template, params); got != tt.want {
				t.Fatalf("Materialize = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeAndValidate(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{
			name: "valid single metric",
			json: `{"id":"t1","image":"i","instruction":"do","evaluator":{"func":"exact_match","result":{"kind":"exec","args":["echo","ok"]}}}`,
		},
		{
			name: "valid list with conj",
			json: `{"id":"t2","image":"i","instruction":"do","evaluator":{"func":["exact_match","must_include"],"conj":"and","result":{"kind":"file","path":"/tmp/x"}}}`,
		},
		{
			name:    "unknown metric",
			json:    `{"id":"t3","image":"i","instruction":"do","evaluator":{"func":"not_a_metric","result":{"kind":"exec","args":["x"]}}}`,
			wantErr: "unknown metric",
		},
		{
			name:    "list without conj",
			json:    `{"id":"t4","image":"i","instruction":"do","evaluator":{"func":["exact_match","must_include"],"result":{"kind":"exec","args":["x"]}}}`,
			wantErr: "conj required",
		},
		{
			name:    "unknown getter kind",
			json:    `{"id":"t5","image":"i","instruction":"do","evaluator":{"func":"exact_match","result":{"kind":"telepathy"}}}`,
			wantErr: "unknown getter kind",
		},
		{
			name:    "empty id",
			json:    `{"id":"","image":"i","instruction":"do","evaluator":{"func":"exact_match","result":{"kind":"exec","args":["x"]}}}`,
			wantErr: "id is empty",
		},
		{
			name:    "unknown field",
			json:    `{"id":"t6","mystery":true,"evaluator":{"func":"exact_match","result":{"kind":"exec","args":["x"]}}}`,
			wantErr: "decode task",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(strings.NewReader(tt.json))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	good := `{"id":"a","image":"i","instruction":"do","evaluator":{"func":"exact_match","result":{"kind":"exec","args":["echo","ok"]}}}`
	if err := os.WriteFile(filepath.Join(dir, "a.json"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	// A non-json file is ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "a" {
		t.Fatalf("Load returned %d tasks, want 1 (id a)", len(tasks))
	}
}

func TestLoadEmptyCorpus(t *testing.T) {
	dir := t.TempDir()
	tasks, err := Load(dir)
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("empty corpus returned %d tasks", len(tasks))
	}
}

func TestLoadRejectsBadCorpus(t *testing.T) {
	dir := t.TempDir()
	bad := `{"id":"x","evaluator":{"func":"not_a_metric","result":{"kind":"exec","args":["x"]}}}`
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatalf("Load accepted a corpus with an unknown metric")
	}
}
