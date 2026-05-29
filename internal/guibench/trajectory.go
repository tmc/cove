package guibench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Trajectory is one task attempt recorded as an ordered sequence of agent
// steps with the task's instruction and the final verifier reward (design 047
// §16). It is the unit a benchmark run drops nearly for free: a verifiable
// native-macOS UI-grounding example no public dataset carries. A Trajectory is
// produced two ways:
//
//   - oracle: the task's known-good [Task.Solution] is run on a fresh fork and
//     each step recorded (see [RecordOracleTrajectory]). The reward is 1 by
//     construction — these are the gold demonstrations.
//   - scored: a model run's captured events (a run bundle's events.jsonl and
//     screenshots) are transformed into this shape, with the reward the
//     verifier's [0,1] score.
//
// The on-disk export ([WriteDataset]) follows the HuggingFace datasets
// conventions so the result loads with `load_dataset("imagefolder", ...)` or
// `load_dataset("json", ...)` without a custom loading script: per-step rows
// live in metadata.jsonl keyed by a relative file_name that points into
// images/, whole trajectories live in trajectories.jsonl (one JSON object per
// line), and dataset_info.json carries the feature schema and provenance.
type Trajectory struct {
	// TrajectoryID uniquely names this attempt within a dataset. It is the run
	// id for a scored run, or "oracle-<task>-<seed>" for an oracle run.
	TrajectoryID string `json:"trajectory_id"`
	// TaskID is the corpus task this trajectory attempts.
	TaskID string `json:"task_id"`
	// Domain is the task's domain (Finder | Safari | ...), copied so a row is
	// self-describing without a corpus join.
	Domain string `json:"domain,omitempty"`
	// Instruction is the materialized natural-language goal given to the agent
	// (parameter placeholders already substituted).
	Instruction string `json:"instruction"`
	// Provider names the agent that produced a scored trajectory (e.g.
	// "anthropic"); it is "oracle" for an oracle run.
	Provider string `json:"provider"`
	// Source is one of [SourceOracle] or [SourceScored], so a consumer can
	// filter gold demonstrations from model attempts.
	Source string `json:"source"`
	// Seed is the parameter seed that materialized this variation, so the
	// instruction and reward are reproducible.
	Seed uint64 `json:"seed"`
	// Steps are the ordered agent steps. An oracle trajectory has one step per
	// solution action; a scored trajectory has one per captured agent action.
	Steps []TrajectoryStep `json:"steps"`
	// Reward is the verifier's terminal [0,1] score. It is 1 for an oracle
	// trajectory by construction.
	Reward float64 `json:"reward"`
	// Answer is the agent's final terminal answer, if any (the infeasible-task
	// "FAIL" or a free-text reply).
	Answer string `json:"answer,omitempty"`
	// RecordedAt is the UTC time the trajectory was recorded, RFC3339Nano.
	RecordedAt string `json:"recorded_at,omitempty"`
}

// TrajectoryStep is one agent action and the observation that preceded it. The
// fields mirror the de-facto computer-use trajectory shape (trycua cua-bench,
// OSWorld replay): a screenshot reference, an action, the observation text, and
// optional model reasoning. Screenshot is a dataset-relative path under images/
// (the HF imagefolder file_name), empty when no screenshot was captured (an
// oracle run records OCR text as the observation instead of pixels).
type TrajectoryStep struct {
	// Index is the zero-based step position within the trajectory.
	Index int `json:"index"`
	// Screenshot is the images/-relative PNG path of the pre-action observation,
	// or empty when only a text observation was captured.
	Screenshot string `json:"screenshot,omitempty"`
	// Action is the agent action taken at this step. For an oracle step it is
	// the solution argv joined into a single shell-ish string; for a scored
	// step it is the captured control action (e.g. "click", "type").
	Action string `json:"action"`
	// Observation is the text the agent saw before acting (on-screen OCR text
	// for an oracle step, or the captured observation for a scored step).
	Observation string `json:"observation,omitempty"`
	// Reasoning is the model's chain-of-thought for the step, when the capture
	// recorded it. Oracle steps have none.
	Reasoning string `json:"reasoning,omitempty"`
}

// Trajectory sources.
const (
	SourceOracle = "oracle" // known-good solution demonstration, reward 1
	SourceScored = "scored" // a model run scored by the verifier
)

// stepRow is one flattened per-step record in metadata.jsonl. It carries the
// HF imagefolder file_name (the images/-relative screenshot path) plus the
// trajectory join keys, so the dataset loads as an image dataset where each
// example is one (screenshot, action) grounding pair. A step with no screenshot
// is still emitted with an empty file_name so a json-loader sees every step.
type stepRow struct {
	FileName     string  `json:"file_name"`
	TrajectoryID string  `json:"trajectory_id"`
	TaskID       string  `json:"task_id"`
	Domain       string  `json:"domain,omitempty"`
	Instruction  string  `json:"instruction"`
	Provider     string  `json:"provider"`
	Source       string  `json:"source"`
	Seed         uint64  `json:"seed"`
	StepIndex    int     `json:"step_index"`
	Action       string  `json:"action"`
	Observation  string  `json:"observation,omitempty"`
	Reasoning    string  `json:"reasoning,omitempty"`
	Reward       float64 `json:"reward"`
}

// DatasetInfo is dataset_info.json: the feature schema and provenance for an
// exported trajectory dataset, written so a consumer can read what the columns
// mean and which corpus/verifier produced the rewards without parsing the rows.
// It mirrors the HuggingFace dataset_info.json role (a description plus the
// feature dtypes) while staying a plain JSON object cove writes itself.
type DatasetInfo struct {
	Description    string             `json:"description"`
	SchemaVersion  string             `json:"schema_version"`
	VerifierHash   string             `json:"verifier_hash"`
	Builder        string             `json:"builder"` // "guibench-trajectory"
	CreatedAt      string             `json:"created_at"`
	TrajectoryN    int                `json:"trajectory_count"`
	StepN          int                `json:"step_count"`
	ScreenshotN    int                `json:"screenshot_count"`
	Sources        []string           `json:"sources"` // distinct Trajectory.Source values present
	Features       map[string]Feature `json:"features"`
	ImagesRelative string             `json:"images_dir"` // "images"
}

// Feature names a metadata.jsonl column's dtype in the HuggingFace
// datasets.Features vocabulary (so the JSON is self-documenting): "image" for
// the screenshot file_name column, "string"/"int64"/"float64"/"uint64" for the
// scalar columns.
type Feature struct {
	Dtype string `json:"dtype"`
}

// datasetFeatures is the fixed feature schema of metadata.jsonl, matching the
// stepRow JSON keys. The file_name column is the HF "image" feature, so the
// imagefolder loader decodes the referenced PNGs into a screenshot column.
func datasetFeatures() map[string]Feature {
	return map[string]Feature{
		"file_name":     {Dtype: "image"},
		"trajectory_id": {Dtype: "string"},
		"task_id":       {Dtype: "string"},
		"domain":        {Dtype: "string"},
		"instruction":   {Dtype: "string"},
		"provider":      {Dtype: "string"},
		"source":        {Dtype: "string"},
		"seed":          {Dtype: "uint64"},
		"step_index":    {Dtype: "int64"},
		"action":        {Dtype: "string"},
		"observation":   {Dtype: "string"},
		"reasoning":     {Dtype: "string"},
		"reward":        {Dtype: "float64"},
	}
}

// Validate checks a trajectory's structural invariants: a non-empty task id and
// instruction, a known source, and a reward in [0,1]. It does not touch a VM.
func (t *Trajectory) Validate() error {
	if t.TaskID == "" {
		return fmt.Errorf("trajectory: task id is empty")
	}
	if t.Instruction == "" {
		return fmt.Errorf("trajectory %s: instruction is empty", t.TaskID)
	}
	switch t.Source {
	case SourceOracle, SourceScored:
	default:
		return fmt.Errorf("trajectory %s: unknown source %q", t.TaskID, t.Source)
	}
	if t.Reward < 0 || t.Reward > 1 {
		return fmt.Errorf("trajectory %s: reward %v out of [0,1]", t.TaskID, t.Reward)
	}
	for i, s := range t.Steps {
		if s.Index != i {
			return fmt.Errorf("trajectory %s: step %d has index %d", t.TaskID, i, s.Index)
		}
		if s.Action == "" {
			return fmt.Errorf("trajectory %s: step %d has empty action", t.TaskID, i)
		}
	}
	return nil
}

// rows flattens a trajectory into its per-step metadata.jsonl records.
func (t *Trajectory) rows() []stepRow {
	out := make([]stepRow, 0, len(t.Steps))
	for _, s := range t.Steps {
		out = append(out, stepRow{
			FileName:     s.Screenshot,
			TrajectoryID: t.TrajectoryID,
			TaskID:       t.TaskID,
			Domain:       t.Domain,
			Instruction:  t.Instruction,
			Provider:     t.Provider,
			Source:       t.Source,
			Seed:         t.Seed,
			StepIndex:    s.Index,
			Action:       s.Action,
			Observation:  s.Observation,
			Reasoning:    s.Reasoning,
			Reward:       t.Reward,
		})
	}
	return out
}

// WriteDataset writes the trajectories as a HuggingFace-loadable dataset under
// dir, creating it if needed. The layout is:
//
//	dir/
//	  trajectories.jsonl   one Trajectory JSON object per line (json loader)
//	  metadata.jsonl       one per-step row per line, file_name -> images/...
//	  dataset_info.json    feature schema + provenance
//	  images/              referenced screenshots (only when steps carry them)
//
// images/ is created only when at least one step has a screenshot; a screenshot
// is sourced through screenshots, a map from a step's images/-relative path to
// its PNG bytes (the oracle/scored exporter fills it). WriteDataset is a pure
// transform plus file I/O: it does not run a task or touch a VM. verifierHash
// is recorded in dataset_info.json so a consumer knows which verifier assigned
// the rewards; pass [VerifierVersion].
func WriteDataset(dir string, trajs []*Trajectory, screenshots map[string][]byte, verifierHash string) error {
	for _, t := range trajs {
		if err := t.Validate(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create dataset dir: %w", err)
	}

	if err := writeJSONLValues(filepath.Join(dir, "trajectories.jsonl"), trajs); err != nil {
		return fmt.Errorf("write trajectories.jsonl: %w", err)
	}

	var rows []stepRow
	var stepN, shotN int
	used := make(map[string]bool)
	for _, t := range trajs {
		for _, r := range t.rows() {
			rows = append(rows, r)
			stepN++
			if r.FileName != "" {
				used[r.FileName] = true
			}
		}
	}
	if err := writeJSONLValues(filepath.Join(dir, "metadata.jsonl"), rows); err != nil {
		return fmt.Errorf("write metadata.jsonl: %w", err)
	}

	if len(used) > 0 {
		imagesDir := filepath.Join(dir, "images")
		if err := os.MkdirAll(imagesDir, 0o755); err != nil {
			return fmt.Errorf("create images dir: %w", err)
		}
		for rel := range used {
			data, ok := screenshots[rel]
			if !ok {
				return fmt.Errorf("screenshot %q referenced by a step but not provided", rel)
			}
			// rel is images/-relative (e.g. "images/000.png"); write under dir.
			path := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("create screenshot dir: %w", err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				return fmt.Errorf("write screenshot %s: %w", rel, err)
			}
			shotN++
		}
	}

	info := DatasetInfo{
		Description:    "cove guibench native-macOS UI-grounding trajectories (design 047 §16)",
		SchemaVersion:  SchemaVersion,
		VerifierHash:   verifierHash,
		Builder:        "guibench-trajectory",
		CreatedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		TrajectoryN:    len(trajs),
		StepN:          stepN,
		ScreenshotN:    shotN,
		Sources:        distinctSources(trajs),
		Features:       datasetFeatures(),
		ImagesRelative: "images",
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dataset_info: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dataset_info.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write dataset_info.json: %w", err)
	}
	return nil
}

// distinctSources returns the sorted distinct Source values across trajs.
func distinctSources(trajs []*Trajectory) []string {
	seen := make(map[string]bool)
	for _, t := range trajs {
		seen[t.Source] = true
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// writeJSONLValues writes one compact JSON object per line to path.
func writeJSONLValues[T any](path string, items []T) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, it := range items {
		if err := enc.Encode(it); err != nil {
			return err
		}
	}
	return nil
}

// shotName returns the deterministic images/-relative path for a step's
// screenshot, namespaced by trajectory id so two trajectories never collide.
func shotName(trajectoryID string, index int) string {
	return fmt.Sprintf("images/%s-%03d.png", safeSlug(trajectoryID), index)
}

// safeSlug reduces an id to a filesystem-safe slug (alphanumerics, dash,
// underscore), so a trajectory id with a slash or space cannot escape images/.
func safeSlug(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	if len(b) == 0 {
		// Fall back to a hash so an all-punctuation id still yields a name.
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])[:8]
	}
	return string(b)
}
