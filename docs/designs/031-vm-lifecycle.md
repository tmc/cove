# Design 031: VM Lifecycle Policy

**Status:** Draft  
**Author:** Travis Cline  
**Date:** 2026-05-05

## Problem

cove already tracks VM state, guest-agent health, and per-run metrics, but it
does not have a first-class VM lifecycle policy. Operators can start a VM, use
it, and leave it running indefinitely. That is fine for a workstation, but it
is a bad default for shared hosts, CI runners, and long-lived research VMs that
should stop when they are no longer active.

The gap is not just "add a timeout". We need three independent stop signals:

1. **Idle timeout**: stop when the guest agent has not pinged for a while.
2. **Maximum age**: stop when the VM has been running too long, regardless of
   activity.
3. **Run budget**: stop after a bounded number of guest command executions.

Those signals need to be visible, persisted per VM, and enforced from the
runtime loop with a clean stop path and telemetry.

## Decision Summary

- Add a per-VM policy file at `~/.vz/vms/<name>/policy.json`.
- Model the policy as three optional thresholds:
  - `idleTimeout`
  - `maxAge`
  - `runBudget`
- Persist the policy in JSON so the CLI and runtime read the same state.
- Enforce policy from the runtime lifecycle loop while the VM is running.
- Emit one metric event when a policy causes shutdown:
  - `event_type: vm_policy_stop`
  - `extra.reason: idle | max_age | run_budget`
- Stop the VM cleanly first by requesting shutdown through the existing VM
  stop path. Do not hard-stop as the normal policy action.

## Existing Ground

Design 027 established the disk policy pattern: a small policy type, explicit
defaults, simple table tests, and a callsite helper that keeps behavior visible.
Design 030 did the same for the GHA executor cache: a policy surface with a
frozen storage contract, a local file shape, and explicit docs for what is and
is not part of the MVP.

VM lifecycle policy should follow that shape:

- keep the policy small and local to one VM directory
- use JSON on disk, not a hidden database
- keep the runtime enforcement separate from CLI parsing
- make the stop reason observable in metrics and logs

## Policy Model

The policy file contains three thresholds:

| Field | Type | Meaning |
|---|---|---|
| `idleTimeout` | duration | Stop when the last successful daemon ping is older than this threshold. |
| `maxAge` | duration | Stop when the VM age exceeds this wall-clock duration. |
| `runBudget` | integer | Stop after this many guest command executions. |

The policy is sparse. Omitted fields are disabled. A policy file with all
fields omitted is equivalent to "no policy".

Example file:

```json
{
  "idleTimeout": "30m",
  "maxAge": "24h",
  "runBudget": 100
}
```

The runtime should treat each field independently. A VM may have only an idle
timeout, only a max-age cap, only a run budget, or any combination of the
three.

## Storage Contract

Store the policy in the VM directory:

```text
~/.vz/vms/<name>/policy.json
```

The file is managed by a small `internal/vmpolicy` package. That package owns:

- path resolution
- load/save/clear
- default policy values
- merge helpers for CLI updates

The file should be written atomically. Read failures from missing files should
be treated as "no policy" rather than as hard errors.

## CLI Surface

Add `cove policy` with the following forms:

```text
cove policy <vm> show
cove policy <vm> clear
cove policy <vm> idle 30m
cove policy <vm> max-age 24h
cove policy <vm> run-budget 100
```

`set` may be accepted as an alias for the update forms, but the terse forms
above are the primary user-facing shape.

Behavior:

- `show` prints the current policy in readable form.
- `clear` removes `policy.json`.
- the update forms load the current policy, update one field, and save it back.

Exit codes:

- `0` on success
- `2` for usage errors
- nonzero wrapped errors for filesystem or parse failures

The CLI should refuse invalid thresholds early:

- durations must parse with Go's `time.ParseDuration`
- run budget must be a positive integer
- unknown fields should produce a usage error

## Runtime Enforcement

Enforcement belongs in the VM lifecycle loop, not in the CLI.

The runtime already has an agent-health ticker that observes successful pings.
That same loop can also check policy thresholds while the VM is running.

The stop decision should be based on:

- `idleTimeout`: `now - lastPing > idleTimeout`
- `maxAge`: `now - vmStartedAt > maxAge`
- `runBudget`: `execCount >= runBudget`

Precedence matters only for the emitted reason. The runtime should stop on the
first threshold that is clearly exceeded and emit that reason.

Suggested reason order:

1. `run_budget`
2. `max_age`
3. `idle`

That order makes the budgeted stop deterministic when multiple thresholds are
already true.

The runtime should do the following when a policy trips:

1. emit `vm_policy_stop`
2. request a clean stop through the existing VM stop path
3. prevent repeated stop requests for the same threshold event

The runtime should not busy-loop after a policy trip. Once the stop request is
in flight, the monitor can exit or mark the VM as already stopping.

## Metrics

Emit a JSONL event with:

- `event_type: vm_policy_stop`
- `status: ok`
- `extra.reason`
- `extra.policy_path`
- `extra.idle_timeout`
- `extra.max_age`
- `extra.run_budget`

Use the existing `metrics.jsonl` per-run file so policy stops show up next to
other lifecycle events.

## File-Level Shape

Expected implementation surface:

- `docs/designs/031-vm-lifecycle.md`
- `internal/vmpolicy/`
- `policy_cli.go`
- `runtime_lifecycle.go`
- `control_socket.go` or a small shared lifecycle helper if needed
- `cli_help.go`
- tests alongside the new code

Rough size:

- design doc: 120-180 LOC
- policy package: 150-220 LOC
- CLI + help wiring: 100-160 LOC
- lifecycle enforcement + tests: 180-280 LOC

## Test Plan

Unit tests should cover:

1. `Load` on a missing file returns the default/empty policy.
2. `Save` writes a stable JSON file and `Load` round-trips it.
3. merge/update logic keeps unrelated fields intact.
4. CLI show/set/clear handles good and bad input.
5. policy monitor fires for idle, max-age, and run-budget independently.
6. policy stop emits one metric and requests a clean VM shutdown.

## Non-Goals

- No hard-stop policy enforcement in the normal path.
- No global policy database.
- No UI for editing policies beyond the CLI.
- No automatic policy inheritance across forks in this slice.
- No per-command exemptions or temporary overrides.

## Open Questions

- Should policy inheritance follow image forks, disposable clones, or neither?
- Should the runtime write a final "policy cleared" event on stop?
- Should `show` emit JSON as an option for automation?

The MVP answer is "no" to the last two and "not yet" to inheritance. Keep the
first slice focused on local persistence and clean stop behavior.
