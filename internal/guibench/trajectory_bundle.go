package guibench

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BundleSpec carries the corpus join keys a raw run bundle does not record, so
// [TrajectoryFromBundle] can turn the bundle into a scored [Trajectory]. A run
// bundle (~/.vz/runs/<id>/) holds events and screenshots but not the task it
// attempted, the materialized instruction, or the verifier's reward — those come
// from the scoring run and are supplied here.
type BundleSpec struct {
	Dir         string  // run bundle directory (e.g. ~/.vz/runs/<id>)
	TaskID      string  // the corpus task the bundle attempted (required)
	Provider    string  // the agent that produced the bundle
	Domain      string  // the task domain (optional)
	Instruction string  // the materialized instruction given to the agent
	Seed        uint64  // the parameter seed that materialized the variation
	Reward      float64 // the verifier's [0,1] score for the bundle (required)
}

// TrajectoryFromBundle transforms a captured run bundle into a scored
// [Trajectory] (design 047 §16). It is a pure on-disk transform built on the
// package's [LoadTrace] reader: it takes each action-bearing step's action,
// observation, and screenshot, attaches the supplied verifier reward and corpus
// keys, and returns the [Trajectory] plus a screenshot map keyed by each step's
// images/-relative file_name (ready for [WriteDataset]). No VM is touched.
//
// Only steps that carry an action become trajectory steps; pure lifecycle events
// (run.start, run.exit) are skipped, since a trajectory is a sequence of agent
// actions. The reward is the verifier score the caller passes, because a raw
// bundle is not corpus-aware.
func TrajectoryFromBundle(spec BundleSpec) (*Trajectory, map[string][]byte, error) {
	if spec.Dir == "" {
		return nil, nil, fmt.Errorf("bundle: dir is empty")
	}
	if spec.TaskID == "" {
		return nil, nil, fmt.Errorf("bundle: task id is empty")
	}
	if spec.Reward < 0 || spec.Reward > 1 {
		return nil, nil, fmt.Errorf("bundle: reward %v out of [0,1]", spec.Reward)
	}
	// A run bundle must carry events.jsonl. LoadTrace tolerates its absence (a
	// manifest-only bundle still renders in the trace viewer), but a trajectory
	// cannot be reconstructed without an events log, so fail closed here.
	if _, err := os.Stat(filepath.Join(spec.Dir, "events.jsonl")); err != nil {
		return nil, nil, fmt.Errorf("bundle: missing events.jsonl: %w", err)
	}

	trace, err := LoadTrace(spec.Dir)
	if err != nil {
		return nil, nil, err
	}

	trajID := filepath.Base(strings.TrimRight(spec.Dir, "/"))
	if trace.RunID != "" {
		trajID = trace.RunID
	}
	if trajID == "" || trajID == "." {
		trajID = "scored-" + spec.TaskID
	}
	instruction := spec.Instruction
	if instruction == "" {
		// Validate requires a non-empty instruction; fall back to the task id so a
		// bundle exported without an instruction still yields a usable record.
		instruction = spec.TaskID
	}
	traj := &Trajectory{
		TrajectoryID: trajID,
		TaskID:       spec.TaskID,
		Domain:       spec.Domain,
		Instruction:  instruction,
		Provider:     spec.Provider,
		Source:       SourceScored,
		Seed:         spec.Seed,
		Reward:       spec.Reward,
	}

	shots := make(map[string][]byte)
	idx := 0
	for _, s := range trace.Steps {
		if s.Action == "" {
			continue // lifecycle event, not an agent action
		}
		step := TrajectoryStep{
			Index:       idx,
			Action:      s.Action,
			Observation: s.Observation,
		}
		// s.Screenshot is already normalized to "screenshots/<name>" by LoadTrace.
		if s.Screenshot != "" {
			path := filepath.Join(spec.Dir, filepath.FromSlash(s.Screenshot))
			if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
				rel := shotName(trajID, idx)
				shots[rel] = data
				step.Screenshot = rel
			}
		}
		traj.Steps = append(traj.Steps, step)
		idx++
	}
	if len(traj.Steps) == 0 {
		// A bundle with no recorded agent actions still yields a one-step terminal
		// record, so the trajectory is non-empty and loadable.
		traj.Steps = append(traj.Steps, TrajectoryStep{Index: 0, Action: "(no recorded actions)"})
	}
	if err := traj.Validate(); err != nil {
		return nil, nil, err
	}
	return traj, shots, nil
}
