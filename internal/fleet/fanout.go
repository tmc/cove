package fleet

import (
	"context"
	"sort"
	"strings"
	"time"
)

// FanOutOutcome records the result of issuing a command to a single host
// during a fan-out run.
type FanOutOutcome struct {
	Host       string `json:"host"`
	OK         bool   `json:"ok"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// FanOutResult aggregates the per-host outcomes of a fan-out run together with
// success and failure tallies.
type FanOutResult struct {
	Outcomes []FanOutOutcome `json:"outcomes"`
	Success  int             `json:"success"`
	Failed   int             `json:"failed"`
}

// FanOut issues run concurrently to every entry and aggregates the results
// fail-soft: a failure on one host is recorded as a non-OK outcome rather than
// aborting the sweep. Outcomes are sorted by host name for stable output. A
// timeout <= 0 falls back to DefaultQueryTimeout.
func FanOut(ctx context.Context, entries []Entry, timeout time.Duration, run QueryFunc[string]) FanOutResult {
	if timeout <= 0 {
		timeout = DefaultQueryTimeout
	}
	results := QueryAllWithTimeout(ctx, entries, timeout, run)
	out := FanOutResult{Outcomes: make([]FanOutOutcome, 0, len(results))}
	for _, r := range results {
		outcome := FanOutOutcome{
			Host:       r.Host,
			OK:         r.Error == nil,
			Output:     strings.TrimRight(r.Value, "\n"),
			DurationMS: r.Duration.Milliseconds(),
		}
		if r.Error != nil {
			outcome.Error = r.Error.Error()
			out.Failed++
		} else {
			out.Success++
		}
		out.Outcomes = append(out.Outcomes, outcome)
	}
	sort.Slice(out.Outcomes, func(i, j int) bool { return out.Outcomes[i].Host < out.Outcomes[j].Host })
	return out
}
