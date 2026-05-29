package guibench

import (
	"context"
	"fmt"
)

// The [Tier] type and the TierA/TierB/TierC constants are defined in tier.go,
// alongside the [Tier.Grant] and [MaxTier] helpers; the [Backend] interface
// reports its grant level as a Tier.

// Backend is the swappable substrate a corpus runs on (design 047 §9 slice 7).
//
// The benchmark engine is substrate-agnostic: it loads tasks, materializes
// parameters, drives the agent, reads end-state through a [Probe], and scores
// with pure [Metric]s. Everything that touches a real machine — provisioning a
// fresh isolated environment per task, running the agent loop, and discarding
// the environment — lives behind Backend. The reference implementation forks an
// ephemeral RAM-overlay cove VM per task (design 013), but a third party can
// supply a Backend over a different macOS substrate and run the same corpus
// unchanged.
//
// The contract a Backend must honor for results to be citable:
//
//   - Acquire returns a hermetic environment that has seen no prior task's
//     state (design 047 §6: one fresh fork per task, never reused, never soft
//     reset). The reference VZ-fork backend gets this from the RAM-overlay
//     ephemeral fork's throw-everything-away property.
//   - The environment carries exactly the privilege grants the corpus needs,
//     no more (design 047 §5 tiers A/B/C). Tier-A getters need none; Tier B
//     needs Full Disk Access baked into the base image; Tier C needs Apple
//     Events + Accessibility. A Backend declares what it can satisfy through
//     [Backend.MaxTier].
//   - No gold reference is reachable from inside the environment (design 047
//     §8 contamination rule): egress is denied except where a task's setup
//     explicitly needs it.
type Backend interface {
	// Acquire provisions a fresh, hermetic environment for one task and returns
	// a [Session] bound to it. The image names the substrate-specific base to
	// fork from (a cove image ref for the reference backend). Acquire must not
	// reuse an environment across tasks.
	Acquire(ctx context.Context, image string) (Session, error)

	// MaxTier reports the highest privilege tier this backend's environments
	// satisfy (design 047 §5; tiers are ordered TierA < TierB < TierC). The
	// engine refuses a corpus whose [MaxTier] exceeds this, rather than letting
	// a Tier-B/C getter silently read denied or stale state on an under-granted
	// image. The reference VZ-fork backend reports the grant level baked into
	// the base image and verified by `cove doctor` before save.
	MaxTier() Tier
}

// Session is one task's hold on a hermetic environment. The engine runs the
// task's setup steps and agent against the Session, reads end-state through its
// [Probe], then Closes it to discard the environment.
type Session interface {
	// Probe reads live state off the environment for the getters (design 047
	// §5). It is the same [Probe] Tier-A getters already use.
	Probe() Probe

	// RunAgent runs the computer-use agent against instruction with the given
	// step budget and returns the agent's final answer. budget is derived from
	// the task's complexity (design 047 §7). For an infeasible task the answer
	// is what the [metricInfeasible] metric scores against "FAIL".
	RunAgent(ctx context.Context, instruction string, budget int) (string, error)

	// Close discards the environment. After Close the Session's Probe must not
	// be used. Discarding a RAM-overlay fork is the reset (design 047 §6), so a
	// Backend has nothing to roll back.
	Close() error
}

// CanRun reports whether b can score every task in tasks without an
// under-granted getter: the corpus's [MaxTier] must not exceed the backend's
// (design 047 §5, §12). A nil error means the engine may run the corpus on b;
// otherwise the message names the gap so an operator provisions the missing
// grant rather than getting silent verifier failures.
func CanRun(b Backend, tasks []*Task) error {
	need := MaxTier(tasks)
	if have := b.MaxTier(); need > have {
		return fmt.Errorf("backend satisfies tier %s but corpus needs tier %s (grant: %s)", have, need, need.Grant())
	}
	return nil
}

// StepBudget returns the agent step budget for a task's complexity (design 047
// §7: complexity-scaled, not a fixed 200 like OpenAI CUA, because macOS tasks
// are mostly short). Complexity 0 or negative falls back to the floor.
func StepBudget(complexity int) int {
	const (
		floor   = 15 // shortest tasks still need room to settle and act
		perStep = 15 // each complexity unit buys this many agent steps
	)
	if complexity <= 0 {
		return floor
	}
	return floor + complexity*perStep
}
