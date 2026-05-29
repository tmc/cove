# guibench example external corpus

A minimal, self-contained corpus that exercises the [task-schema
spec](../task-schema.md) end-to-end. It is the template a third party copies to
author their own native-macOS computer-use corpus on cove (design
[047](../../../designs/047-gui-agent-benchmark-harness.md) §9 slice 7).

These tasks are illustrative, not part of the scored cove-original corpus. They
are deliberately **all Tier A** (no TCC grant), so they run on a fresh fork with
no base-image provisioning — the simplest possible starting point.

## Tasks

| File | Domain | Getter (tier) | Metric | Demonstrates |
|---|---|---|---|---|
| `finder-create-folder.json` | Finder | `exec` (A) | `file_exists` | A parameterized task: `{FOLDER}` drawn from a pool, setup cleans the path, the verifier is computed from the same param. |
| `settings-appearance.json` | Settings | `defaults` (A) | `plist_equals` | The cfprefsd-routed `defaults` getter (§7 flush discipline) with an `options.expected` gold literal. |
| `safari-infeasible.json` | Safari | `exec` (A) | `infeasible` | An infeasible task: success = the agent's terminal answer is `FAIL`. |

## Run it

Validate (no VM, runs in CI):

```
cove bench gui validate -corpus docs/benchmarks/guibench/example-corpus
```

Print the versioned manifest:

```
cove bench gui manifest -corpus docs/benchmarks/guibench/example-corpus
```

Score it (needs Apple-Silicon hardware, a base image, and provider API keys):

```
cove bench gui run -corpus docs/benchmarks/guibench/example-corpus -provider <p>
```

## Guarantees

The corpus inherits cove's [fork-isolation and privilege-tier
guarantees](../guarantees.md): every task runs in a fresh ephemeral RAM-overlay
fork (no cross-task leak), the getters are honestly classified by the grant they
need (all Tier A here), and gold references stay host-side (the appearance task's
gold value is the `options.expected` literal, never reachable from the guest).

## Authoring notes

- Keep getters at the lowest tier that works. Tier A needs no provisioning; Tier
  B/C requires a pre-granted base image verified by `cove bench gui image-check`.
- Parameterize anything memorizable: put the variable in a `schema` pool and
  compute the gold value from the same param (the `finder-create-folder` pattern).
- For an appearance/preference check, read through the `defaults` getter (live
  `cfprefsd` value), never a stale on-disk `.plist`.
- An infeasible task carries `"infeasible": true` and the `infeasible` metric;
  its `result` getter is a placeholder (the metric scores the agent's answer, not
  guest state).
