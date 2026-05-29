package guibench

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Task is a declarative, parameterized macOS benchmark task (design 047 §4).
//
// A Task is a template: its Schema declares typed parameters, and Params
// materializes a concrete variation from a seed. Instruction, Setup, and the
// Evaluator's expected value all reference parameter placeholders, so a given
// seed yields one deterministic, self-consistent variation (design 047 §10).
type Task struct {
	ID          string      `json:"id"`
	Image       string      `json:"image"`            // cove image ref to fork from
	Domain      string      `json:"domain,omitempty"` // Finder | Safari | Settings | ... (per-domain aggregation, §7)
	Instruction string      `json:"instruction"`      // goal given to the agent
	Source      string      `json:"source"`           // provenance URL/note
	Complexity  int         `json:"complexity"`       // maps to a step budget
	Config      []SetupStep `json:"config"`           // ordered setup steps run after fork
	Evaluator   Evaluator   `json:"evaluator"`
	Infeasible  bool        `json:"infeasible"` // success = agent answers FAIL
	// Solution is a deterministic, known-good sequence of guest actions that
	// solves the task — the steps an ideal agent would take. It is NOT given to
	// the agent during a scored run; it exists for the verifier self-check
	// (design 047 §9 slice 4), which runs Config then Solution and asserts the
	// evaluator scores 1.0, and asserts a no-op (Config only) scores 0.0. This
	// is the AndroidWorld "is the validator correct" discipline: a verifier that
	// cannot recognize its own gold solution is broken. For an infeasible task
	// the solution is the terminal answer "FAIL" (see [Task.SolutionAnswer]).
	Solution []SetupStep `json:"solution,omitempty"`
	Subset   []string    `json:"subset,omitempty"` // named subsets this task belongs to, e.g. "test_small" (OSWorld pattern)
	Schema   []Param     `json:"schema,omitempty"`
	// NetworkAllow is the per-task egress allowlist. Empty (the default) means
	// the task runs fully offline during scoring so the agent cannot fetch a
	// gold reference (design 047 §8); a task that genuinely needs the network
	// lists exactly the domains it may reach. See [TaskEgress].
	NetworkAllow []string `json:"network_allow,omitempty"`
}

// SetupStep is one ordered setup action run after fork, before the agent acts.
// Args entries may contain {PARAM} placeholders resolved against materialized
// parameters.
type SetupStep struct {
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	WorkDir string            `json:"work_dir,omitempty"`
}

// Evaluator scores the agent's end-state against an optional reference.
//
// Func is a single metric name or a list; when it is a list, Conj selects how
// sub-scores combine: "and" => mean, "or" => max (design 047 §4). Result and
// Expected are getter specs (the getter reads live state off the guest);
// Expected is optional. Both may contain {PARAM} placeholders.
type Evaluator struct {
	Func     StringList     `json:"func"`
	Conj     string         `json:"conj,omitempty"` // "and" | "or"
	Result   GetterSpec     `json:"result"`
	Expected *GetterSpec    `json:"expected,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
}

// Param is a typed task parameter drawn from a controlled pool.
//
// Name is the placeholder (used as {Name}). Pool is the set of candidate
// values; Params picks one deterministically by seed. ExpectedFrom, when set,
// names another param whose chosen value derives this one by lookup in Derive
// (so the gold value tracks the chosen input). When ExpectedFrom is empty the
// param is an independent choice from Pool.
type Param struct {
	Name         string            `json:"name"`
	Pool         []string          `json:"pool"`
	ExpectedFrom string            `json:"expected_from,omitempty"`
	Derive       map[string]string `json:"derive,omitempty"`
}

// Params deterministically materializes the schema into a name->value map.
//
// The same seed always yields the same values (math/rand/v2 over a seeded
// source, never the global rand or crypto/rand), so the verifier's expected
// value — computed from these same params — stays consistent with the
// instruction. Params with no schema returns an empty, non-nil map.
func (t *Task) Params(seed uint64) map[string]string {
	out := make(map[string]string, len(t.Schema))
	if len(t.Schema) == 0 {
		return out
	}
	// Mix the task id into the seed so distinct tasks with the same schema and
	// seed still diverge.
	r := rand.New(rand.NewPCG(seed, hash64(t.ID)))
	// Resolve independent params first, then derived ones, so derivation can
	// read an already-chosen input.
	for _, p := range t.Schema {
		if p.ExpectedFrom != "" {
			continue
		}
		if len(p.Pool) == 0 {
			continue
		}
		out[p.Name] = p.Pool[r.IntN(len(p.Pool))]
	}
	for _, p := range t.Schema {
		if p.ExpectedFrom == "" {
			continue
		}
		out[p.Name] = p.Derive[out[p.ExpectedFrom]]
	}
	return out
}

// Materialize substitutes the given params into a string template, replacing
// each {NAME} with its value. Unknown placeholders are left untouched.
func Materialize(template string, params map[string]string) string {
	if len(params) == 0 || !strings.ContainsRune(template, '{') {
		return template
	}
	// Replace longest names first to avoid prefix collisions.
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	out := template
	for _, name := range names {
		out = strings.ReplaceAll(out, "{"+name+"}", params[name])
	}
	return out
}

// hash64 is a small deterministic FNV-1a over s, used to mix the task id into
// the PRNG seed.
func hash64(s string) uint64 {
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// Validate checks the task's structural invariants. It rejects unknown
// metrics, a missing conjunction when Func is a list, and an unknown getter
// kind. It does not touch a VM.
func (t *Task) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("task: id is empty")
	}
	if len(t.Evaluator.Func) == 0 {
		return fmt.Errorf("task %s: evaluator func is empty", t.ID)
	}
	known := metricNames()
	for _, name := range t.Evaluator.Func {
		if !known[name] {
			return fmt.Errorf("task %s: unknown metric %q", t.ID, name)
		}
	}
	if len(t.Evaluator.Func) > 1 {
		switch t.Evaluator.Conj {
		case "and", "or":
		case "":
			return fmt.Errorf("task %s: conj required when func is a list", t.ID)
		default:
			return fmt.Errorf("task %s: invalid conj %q", t.ID, t.Evaluator.Conj)
		}
	}
	if err := t.Evaluator.Result.validate(); err != nil {
		return fmt.Errorf("task %s: result getter: %w", t.ID, err)
	}
	if t.Evaluator.Expected != nil {
		if err := t.Evaluator.Expected.validate(); err != nil {
			return fmt.Errorf("task %s: expected getter: %w", t.ID, err)
		}
	}
	for _, p := range t.Schema {
		if p.Name == "" {
			return fmt.Errorf("task %s: schema param has empty name", t.ID)
		}
	}
	return nil
}

// Decode reads one task from r, validating it before returning.
func Decode(r io.Reader) (*Task, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var t Task
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf("decode task: %w", err)
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return &t, nil
}

// Load reads every *.json file in dir as a task, validating each. Tasks are
// returned sorted by id. An empty directory yields an empty slice and no error
// (the corpus ships with zero tasks).
func Load(dir string) ([]*Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("load corpus: %w", err)
	}
	var tasks []*Task
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("load corpus: %w", err)
		}
		t, err := Decode(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks, nil
}

// StringList is a JSON field that accepts either a single string or a list of
// strings, normalizing both to a slice.
type StringList []string

// UnmarshalJSON accepts "x" or ["x","y"].
func (s *StringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		*s = StringList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("string list: want string or []string: %w", err)
	}
	*s = StringList(many)
	return nil
}
