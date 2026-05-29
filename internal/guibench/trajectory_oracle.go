package guibench

import (
	"fmt"
)

// RecordOracleTrajectory runs a task's known-good [Task.Solution] on a fresh
// fork and records each solution step as a [Trajectory] step, then evaluates to
// fill the reward (design 047 §16). The result is a gold demonstration: the
// agent action is the solution argv, the observation is the on-screen text the
// step acted against, and the reward is the verifier's score (1 for a correct
// gold solution — [SelfCheck] is the discipline that proves it).
//
// It reuses the [SelfCheckEnv] fork path, so the live oracle export and the live
// scored run share one substrate. The recorder is the minimal per-step hook the
// scout flagged as missing: it wraps the existing [runSteps] loop to capture one
// step record per solution action, without changing the scored runner or the
// self-check. An infeasible task has no on-guest solution steps; its oracle
// trajectory is a single terminal step whose action is the gold answer.
//
// Screenshots: when the session's [Probe] also satisfies [Screenshotter] the
// pre-action screenshot is captured into screenshots, keyed by the step's
// images/-relative file_name, and the [Trajectory] step references it. Without a
// screenshotter (the unit-test [FakeProbe] path) the step records OCR text as
// its observation and carries no screenshot — the dataset is still valid, just
// text-grounded rather than pixel-grounded.
func RecordOracleTrajectory(env SelfCheckEnv, t *Task, seed uint64) (*Trajectory, map[string][]byte, error) {
	if err := t.CheckSelfCheckable(); err != nil {
		return nil, nil, err
	}
	params := t.Params(seed)

	sess, err := env.Acquire(t.Image)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire: %w", err)
	}
	defer sess.Close()

	p := sess.Probe()
	if err := runSteps(p, t.Config, params); err != nil {
		return nil, nil, fmt.Errorf("setup: %w", err)
	}

	traj := &Trajectory{
		TrajectoryID: fmt.Sprintf("oracle-%s-%d", t.ID, seed),
		TaskID:       t.ID,
		Domain:       t.Domain,
		Instruction:  Materialize(t.Instruction, params),
		Provider:     SourceOracle,
		Source:       SourceOracle,
		Seed:         seed,
	}
	screenshots := make(map[string][]byte)

	if t.Infeasible {
		// No on-guest steps: the gold solution is the terminal answer "FAIL".
		obs, shot := observe(p, traj.TrajectoryID, 0, screenshots)
		traj.Steps = append(traj.Steps, TrajectoryStep{
			Index:       0,
			Screenshot:  shot,
			Action:      "answer FAIL",
			Observation: obs,
		})
		traj.Answer = t.SolutionAnswer(true)
	} else {
		for i, s := range t.Solution {
			args := materializeArgs(s.Args, params)
			if len(args) == 0 {
				return nil, nil, fmt.Errorf("solution step %d: empty args", i)
			}
			// Observe before acting, so the screenshot/text is the pre-action
			// state the agent would have grounded against.
			obs, shot := observe(p, traj.TrajectoryID, i, screenshots)
			traj.Steps = append(traj.Steps, TrajectoryStep{
				Index:       i,
				Screenshot:  shot,
				Action:      joinArgs(args),
				Observation: obs,
			})
			if _, _, _, err := p.Exec(args, s.Env, s.WorkDir); err != nil {
				return nil, nil, fmt.Errorf("solution step %d (%v): %w", i, args, err)
			}
		}
	}

	answer := ""
	if t.Infeasible {
		answer = t.SolutionAnswer(true)
	}
	reward, err := Evaluate(p, t, params, answer)
	if err != nil {
		return nil, nil, fmt.Errorf("evaluate: %w", err)
	}
	traj.Reward = reward

	if err := traj.Validate(); err != nil {
		return nil, nil, err
	}
	return traj, screenshots, nil
}

// RecordOracleCorpus records an oracle [Trajectory] for every self-checkable
// task in tasks. A task that cannot be self-checked (no solution, not
// infeasible) is skipped rather than failing the whole export, since some
// corpus tasks may not yet have a gold solution. The merged screenshot map is
// keyed by each step's images/-relative file_name (trajectory-id namespaced, so
// no collisions across tasks).
func RecordOracleCorpus(env SelfCheckEnv, tasks []*Task, seed uint64) ([]*Trajectory, map[string][]byte, error) {
	var trajs []*Trajectory
	screenshots := make(map[string][]byte)
	for _, t := range tasks {
		if err := t.CheckSelfCheckable(); err != nil {
			continue
		}
		traj, shots, err := RecordOracleTrajectory(env, t, seed)
		if err != nil {
			return nil, nil, fmt.Errorf("task %s: %w", t.ID, err)
		}
		trajs = append(trajs, traj)
		for k, v := range shots {
			screenshots[k] = v
		}
	}
	return trajs, screenshots, nil
}

// Screenshotter is the optional pixel-observation seam: a [Probe] that can
// capture the guest display returns the PNG bytes. It is kept off the core
// [Probe] interface (which stays the minimal getter transport) so a backend
// opts in without inflating every getter's surface — the registered-runtime
// escape-hatch pattern. The unit-test [FakeProbe] does not implement it, so the
// recorder degrades to text-only observations without a VM.
type Screenshotter interface {
	Screenshot() ([]byte, error)
}

// observe captures the pre-action observation for a step: the screenshot bytes
// when the probe is a [Screenshotter] (stored under the returned file_name), and
// the on-screen OCR text. A capture error is non-fatal — the step still records
// whatever observation succeeded — because a missing screenshot must not abort a
// gold demonstration.
func observe(p Probe, trajectoryID string, index int, screenshots map[string][]byte) (obsText, fileName string) {
	if sc, ok := p.(Screenshotter); ok {
		if data, err := sc.Screenshot(); err == nil && len(data) > 0 {
			fileName = shotName(trajectoryID, index)
			screenshots[fileName] = data
		}
	}
	if text, err := p.OCRAllText(); err == nil {
		obsText = text
	}
	return obsText, fileName
}
