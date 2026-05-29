# guibench provider (backend) interface

The benchmark engine is substrate-agnostic. Everything that touches a real
machine — provisioning a fresh isolated environment per task, running the agent
loop, discarding the environment — lives behind one small Go interface,
`guibench.Backend`. The cove VZ-fork backend is the **reference** implementation;
a third party can supply a `Backend` over a different macOS substrate and run the
same corpus unchanged (design [047](../../designs/047-gui-agent-benchmark-harness.md)
§9 slice 7).

This is deliberately minimal — two interfaces and one helper, no plugin system,
no registry. The swappable seam is exactly the part that needs hardware; the
schema, getters, metrics, scoring, versioning, and egress logic are pure and
shared.

## The interface

```go
// Backend is the swappable substrate a corpus runs on.
type Backend interface {
	// Acquire provisions a fresh, hermetic environment for one task and returns
	// a Session bound to it. image is the substrate-specific base to fork from
	// (a cove image ref for the reference backend). Acquire must not reuse an
	// environment across tasks.
	Acquire(ctx context.Context, image string) (Session, error)

	// MaxTier reports the highest privilege tier this backend's environments
	// satisfy (TierA < TierB < TierC). The engine refuses a corpus whose MaxTier
	// exceeds this, rather than letting a Tier-B/C getter silently read denied
	// or stale state on an under-granted image.
	MaxTier() Tier
}

// Session is one task's hold on a hermetic environment.
type Session interface {
	// Probe reads live state off the environment for the getters. It is the
	// same Probe Tier-A getters already use.
	Probe() Probe

	// RunAgent runs the computer-use agent against instruction with the given
	// step budget and returns the agent's final answer.
	RunAgent(ctx context.Context, instruction string, budget int) (string, error)

	// Close discards the environment. Discarding a RAM-overlay fork is the
	// reset, so a Backend has nothing to roll back.
	Close() error
}

// CanRun reports whether b can score every task in tasks without an
// under-granted getter (corpus MaxTier must not exceed the backend's).
func CanRun(b Backend, tasks []*Task) error
```

`Probe` is the existing getter transport (`Exec`, `ReadFile`, `OCRAllText`); a
backend's `Session.Probe()` returns whatever satisfies it. The reference backend
returns a `guibench.ClientProbe` wrapping a live `*controlclient.Client`; a test
or alternate substrate returns its own implementation (`guibench.FakeProbe` is
the in-memory one used in unit tests).

## The contract a Backend must honor

For results to be citable, a `Backend` must guarantee:

1. **Hermetic per-task isolation.** `Acquire` returns an environment that has
   seen no prior task's state; one fresh environment per task, never reused,
   never soft reset. The reference backend gets this from the RAM-overlay
   ephemeral fork (see [guarantees.md](guarantees.md) §1).
2. **Exactly the corpus's grants, no more.** `MaxTier` reports the grant level
   the environment carries; the engine calls `CanRun` to refuse a corpus that
   needs more (see [guarantees.md](guarantees.md) §2).
3. **No reachable gold reference.** Egress is denied except where a task's
   `network_allow` explicitly permits it (see [guarantees.md](guarantees.md) §3).

## The run loop (what the engine does with a Backend)

For each selected task, for each of N runs:

```
session, _ := backend.Acquire(ctx, task.Image)   // fresh fork
defer session.Close()                             // discard fork = reset

params := task.Params(seed)                       // materialize a variation
runSetup(session.Probe(), task.Config, params)    // ordered setup steps
budget := guibench.StepBudget(task.Complexity)    // complexity-scaled
answer, _ := session.RunAgent(ctx, guibench.Materialize(task.Instruction, params), budget)
// postconfig flush happens inside the getters (defaults/sqlite) or via guibench.Flush
score, _ := guibench.Evaluate(session.Probe(), task, params, answer)
```

`guibench.Evaluate` is the pure verifier half: it runs the result (and optional
expected) getters through the `Probe`, then combines the metrics. A backend never
implements scoring — it only supplies the `Probe` and the agent loop.

## The reference VZ-fork backend

The reference backend (cove's own) implements `Acquire` by exec'ing the cove
binary to fork an ephemeral RAM-overlay VM:

```
cove run -fork-from <image> -fork-name <vm> -ephemeral -auto-upgrade-agent
```

then connecting a `controlclient.Client` to the fork's control socket, wrapping
it in `guibench.ClientProbe` for `Session.Probe()`, and driving the provider via
`internal/agentsandbox.Run` for `Session.RunAgent`. `MaxTier` reports the grant
level baked into the base image and verified by `cove doctor` before save.
`Close` stops the fork; the RAM overlay vanishes.

A third party who already has a way to fork/provision/discard a macOS VM and run
a computer-use agent against it implements the same three `Session` methods over
their substrate and reuses the entire schema, verifier, scoring, and versioning
stack.

## Why not more abstraction

The design rule is "do not over-engineer — a minimal Go interface + doc is
enough." The benchmark's value is the corpus, the verifier, and the isolation
guarantees, all of which are substrate-independent and live in pure Go. The only
thing that legitimately varies by substrate is fork/agent/discard, which is
exactly the three `Session` methods plus `Acquire`/`MaxTier`. Anything more (a
plugin loader, a config DSL, a process boundary) would add surface without adding
a capability a third party needs.
