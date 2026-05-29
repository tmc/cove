package guibench

import "fmt"

// A Composite scores a task whose success spans several independent end-states,
// each read by its OWN getter and judged by its OWN metric — for example
// "the Messages SQLite row exists (sqlite_row_matches) AND the exported Preview
// file is the right picture (image_similar)". The built-in [Evaluator] feeds a
// single Result getter to every metric in its Func list, which is right when
// the metrics judge one value different ways but cannot express "different
// metrics over different artifacts." A Composite fills that gap while reusing
// the same getters ([GetterSpec.Get]) and the same pure scoring half
// ([ScoreMetrics]); it adds no new VM surface (design 047 §4 AND/OR
// composition, AndroidWorld composite_*.py).
//
// Conj combines the per-check scores: "and" => mean (every artifact must be
// right, partial credit is the fraction correct), "or" => max (any one
// artifact suffices). A single check needs no Conj. Composite is deliberately
// flat (a list of checks, not a tree): the corpus's macOS tasks compose a
// handful of end-states, never a nested boolean expression, and a flat list
// keeps the JSON and the scoring obvious.
type Composite struct {
	Conj   string  `json:"conj,omitempty"` // "and" | "or"; required when len(Checks) > 1
	Checks []Check `json:"checks"`
}

// A Check is one artifact-and-judgement pair within a [Composite]: read the
// value with Result (and the optional Expected getter), then score it with the
// metrics in Func combined by the check's own Conj — the same single-getter
// contract the built-in [Evaluator] already implements.
type Check struct {
	Func     StringList     `json:"func"`
	Conj     string         `json:"conj,omitempty"` // combines this check's own metrics
	Result   GetterSpec     `json:"result"`
	Expected *GetterSpec    `json:"expected,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
}

// validate checks the composite's structure without touching a VM: a non-empty
// check list, a Conj when there is more than one check, and a valid metric +
// getter for every check.
func (c Composite) validate() error {
	if len(c.Checks) == 0 {
		return fmt.Errorf("composite: checks is empty")
	}
	if len(c.Checks) > 1 {
		switch c.Conj {
		case "and", "or":
		case "":
			return fmt.Errorf("composite: conj required when checks is a list")
		default:
			return fmt.Errorf("composite: invalid conj %q", c.Conj)
		}
	}
	known := metricNames()
	for i, ch := range c.Checks {
		if len(ch.Func) == 0 {
			return fmt.Errorf("composite: check %d: func is empty", i)
		}
		for _, name := range ch.Func {
			if !known[name] {
				return fmt.Errorf("composite: check %d: unknown metric %q", i, name)
			}
		}
		if len(ch.Func) > 1 {
			switch ch.Conj {
			case "and", "or":
			case "":
				return fmt.Errorf("composite: check %d: conj required when func is a list", i)
			default:
				return fmt.Errorf("composite: check %d: invalid conj %q", i, ch.Conj)
			}
		}
		if err := ch.Result.validate(); err != nil {
			return fmt.Errorf("composite: check %d: result getter: %w", i, err)
		}
		if ch.Expected != nil {
			if err := ch.Expected.validate(); err != nil {
				return fmt.Errorf("composite: check %d: expected getter: %w", i, err)
			}
		}
	}
	return nil
}

// Tier reports the highest privilege tier any check's getters require, so the
// verifier can confirm the base image carries the grants the composite needs.
// Tier values sort A < B < C (their string order), so the running maximum is a
// plain string comparison, matching [MaxTier].
func (c Composite) Tier() Tier {
	t := TierA
	for _, ch := range c.Checks {
		if g := ch.Result.Tier(); g > t {
			t = g
		}
		if ch.Expected != nil {
			if g := ch.Expected.Tier(); g > t {
				t = g
			}
		}
	}
	return t
}

// Evaluate reads every check's artifact off the guest through p, scores each
// check, and combines the per-check scores by Conj ("and" => mean, "or" =>
// max). It is the multi-getter analogue of [Evaluate]; like that function it is
// VM-touching only through p, delegating the pure scoring to [ScoreMetrics].
func (c Composite) Evaluate(p Probe, params map[string]string) (float64, error) {
	if err := c.validate(); err != nil {
		return 0, err
	}
	scores := make([]float64, 0, len(c.Checks))
	for i, ch := range c.Checks {
		s, err := ch.evaluate(p, params)
		if err != nil {
			return 0, fmt.Errorf("check %d: %w", i, err)
		}
		scores = append(scores, s)
	}
	if len(scores) == 1 {
		return scores[0], nil
	}
	return combine(c.Conj, scores)
}

// evaluate reads this check's result (and optional expected) value off the
// guest, then scores it through the shared [ScoreMetrics], so a check behaves
// exactly like a single-getter [Evaluator].
func (ch Check) evaluate(p Probe, params map[string]string) (float64, error) {
	result, err := ch.Result.Get(p, params)
	if err != nil {
		return 0, err
	}
	var expected string
	if ch.Expected != nil {
		v, err := ch.Expected.Get(p, params)
		if err != nil {
			return 0, err
		}
		expected = v
	}
	return ScoreMetrics(Evaluator{
		Func:    ch.Func,
		Conj:    ch.Conj,
		Options: ch.Options,
	}, result, expected, params)
}

// combine folds per-check scores by conjunction: "and" averages (every check
// must pass for a full score, partial credit is the fraction passing), "or"
// takes the max (any one check suffices). It is the shared rule [ScoreMetrics]
// applies to a metric list, lifted to a check list (design 047 §4).
func combine(conj string, scores []float64) (float64, error) {
	switch conj {
	case "or":
		return max(scores...), nil
	case "and":
		return mean(scores), nil
	default:
		return 0, fmt.Errorf("invalid conj %q", conj)
	}
}
