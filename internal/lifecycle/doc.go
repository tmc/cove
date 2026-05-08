// Package lifecycle enforces per-VM run-budget limits.
//
// ConsumeRunBudget atomically increments the runs.counter file in a
// VM directory and returns ErrBudgetExceeded once the configured
// budget is reached. RunsUsed reads the current count and CounterPath
// returns the on-disk location. Idle and max-age stop thresholds are
// configured via the vmpolicy package.
package lifecycle
