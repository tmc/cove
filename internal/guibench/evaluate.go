package guibench

import "fmt"

// Evaluate scores a task's end-state: it runs the result (and optional
// expected) getters through p, then combines every metric in the evaluator.
//
// params are the materialized task parameters (see [Task.Params]); they
// resolve {PARAM} placeholders in the getter specs. For an infeasible task the
// agentAnswer is the result value (the metric checks for "FAIL"); otherwise
// agentAnswer is ignored and the result getter supplies the value. The score
// is in [0,1]: an "and" conjunction averages sub-metrics, "or" takes the max
// (design 047 §4).
func Evaluate(p Probe, t *Task, params map[string]string, agentAnswer string) (float64, error) {
	var result string
	if t.Infeasible {
		result = agentAnswer
	} else {
		v, err := t.Evaluator.Result.Get(p, params)
		if err != nil {
			return 0, fmt.Errorf("task %s: %w", t.ID, err)
		}
		result = v
	}
	var expected string
	if t.Evaluator.Expected != nil {
		v, err := t.Evaluator.Expected.Get(p, params)
		if err != nil {
			return 0, fmt.Errorf("task %s: %w", t.ID, err)
		}
		expected = v
	}
	return ScoreMetrics(t.Evaluator, result, expected, params)
}

// ScoreMetrics combines the evaluator's metrics over an already-extracted
// result and expected value. It is the pure, VM-free half of [Evaluate],
// exported so callers can score values they extracted themselves. expected may
// also be supplied directly through an "expected" option (a literal gold value
// computed from params), which overrides the passed expected when set.
func ScoreMetrics(e Evaluator, result, expected string, params map[string]string) (float64, error) {
	if lit, ok := e.Options["expected"].(string); ok {
		expected = Materialize(lit, params)
	}
	registry := Metrics()
	scores := make([]float64, 0, len(e.Func))
	for _, name := range e.Func {
		m, ok := registry[name]
		if !ok {
			return 0, fmt.Errorf("unknown metric %q", name)
		}
		s, err := m(result, expected, e.Options)
		if err != nil {
			return 0, err
		}
		scores = append(scores, s)
	}
	switch len(scores) {
	case 0:
		return 0, fmt.Errorf("evaluator has no metrics")
	case 1:
		return scores[0], nil
	}
	switch e.Conj {
	case "or":
		return max(scores...), nil
	case "and":
		return mean(scores), nil
	default:
		return 0, fmt.Errorf("invalid conj %q", e.Conj)
	}
}

// mean returns the arithmetic mean of scores (caller guarantees len > 0).
func mean(scores []float64) float64 {
	var sum float64
	for _, s := range scores {
		sum += s
	}
	return sum / float64(len(scores))
}

// max returns the largest score (caller guarantees at least one).
func max(scores ...float64) float64 {
	m := scores[0]
	for _, s := range scores[1:] {
		if s > m {
			m = s
		}
	}
	return m
}
