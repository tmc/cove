# Soft-reset empirical result

**Status**: shipped (Phase D orchestrator)
**Author**: cove team
**Date**: 2026-04-27
**Supersedes**: the soft-reset positioning assumptions in `012-product-roadmap-2026.md` and `014-roadmap-update-post-v0.1.md`
**Cross-references**:
- `bench/soft-reset/results-20260427.md` — matrix output
- `bench/soft-reset/README.md` — probe protocol
- `SAFETY.md` — user-facing safety posture
- `docs/research/tcc-via-user-agent.md` — TCC/FDA research note
- `RELEASE-NOTES-v0.1.1.md` — v0.1.1 TCC correction

## Decision

Do not position per-eval user-account soft reset as an isolation primitive.
It is, at best, a throughput optimization for low-risk workloads that can
tolerate residue inside a warm guest.

For privacy-critical evals, the supported isolation primitive remains:

```bash
cove fork <parent> <child>
```

or restore/fork from a known clean VM state, then discard the child VM after
the run.

## Run Summary

- Host: Apple M4 Max, macOS 26.4.1 build 25E253.
- Parent VM: `hermes-mlx-go-60g-v10`, stopped before fork.
- Disposable VM: `b6-soft-reset-20260427`.
- Guest: macOS 26.3 build 25D125.
- Agent path: daemon agent only for destructive probes.
- Raw local logs: `/tmp/E883-b6-soft-reset-raw-20260427.json` and
  `/tmp/E883-b6-followup-20260427.json`.

The user-session agent timed out on a TCC directory probe before the matrix.
That is the same Full Disk Access limitation documented for v0.1.1, not a new
finding. The matrix therefore used daemon-agent probes and records TCC as a
known limit.

## Matrix

| Concern | Result | Evidence | Product Meaning |
|---|---|---|---|
| TCC residue | `limit` | Daemon root could not inspect TCC-protected paths; user-agent probe timed out. | Soft reset cannot claim TCC isolation until cove has an FDA grant story and a repeatable TCC probe. |
| System Keychain residue | `fail` | A generic password added to `/Library/Keychains/System.keychain` persisted after eval-user deletion. | System Keychain is global VM state. User deletion does not clean it. |
| Apple Account throttling | `limit` | Not run; no bounded Apple Account test credentials or policy budget were available. | The roadmap must not publish Apple Account lifecycle throughput claims. |
| GlobalPreferences leakage | `fail` | A value written under `/Library/Preferences/com.tmc.cove.b6` remained readable after attempted user deletion. | System preferences are global VM state. |
| FileVault SecureToken cycle | `limit` | FileVault was off; test user reported SecureToken disabled. | SecureToken lifecycle remains unvalidated and cannot support soft-reset positioning. |
| Orphaned LaunchDaemon residue | `fail` | A test plist in `/Library/LaunchDaemons` remained after eval-user deletion. | Privileged launchd state is outside the eval-user lifecycle. |

The run produced zero passes, three fails, and three limits. `014` set the
pivot trigger at three or more fail/hard-limit outcomes. This run crosses that
threshold.

## Additional Finding: User Deletion Was Not Clean

`sysadminctl -deleteUser` did not provide a reliable cleanup boundary in the
disposable VM. The probe saw:

- user records still present after delete attempts;
- a home directory left behind;
- `dscl . -delete /Users/<test-user>` returning `eDSPermissionError`.

This is more important than any one residue class. If cove cannot guarantee
that the eval user itself was deleted, then a warm-guest reset loop cannot be
sold as a clean eval boundary.

## Positioning Change

Use this hierarchy in product docs and examples:

1. **Hard isolation**: fork or restore a VM, run the eval, then discard the VM.
2. **Operational reset**: reboot or restore a named snapshot inside a disposable
   VM lineage.
3. **Soft reset**: delete/recreate users only for low-risk throughput tuning,
   never as a privacy or correctness guarantee.

Do not use "thousands per hour" or similar soft-reset throughput language until
a later matrix produces at least four passes and no unmitigated global-state
fails. The current honest claim is that cove can fork a stopped 60 GB VM in
132-140 ms on this host, as recorded in `bench/fork-time/results-20260427.md`.
Boot-to-agent throughput remains a separate benchmark.

## Follow-up Work

1. Keep `SAFETY.md` language that privacy-critical evals must use VM fork or
   restore rather than UID recycling.
2. Add a future `cove doctor` TCC/FDA probe before relying on user-agent file
   access to protected guest paths.
3. If soft reset is revisited, build a first-class harness that:
   - provisions test users with a known admin credential path;
   - records Directory Services operations separately from residue probes;
   - runs FileVault-on and FileVault-off variants;
   - uses bounded, dedicated Apple Account test credentials only when policy
     permits.
4. Treat the OpenAI Agents SDK adapter as fork-first for privacy-sensitive
   examples.

## Phase D (Final): Orchestrator

The final implementation surface is a single orchestrator command:

```bash
cove softreset run-all <vm-ref> [--json] [--filter=filesystem,network,memory,process] [--timeout=60s]
```

Input:

- VM ref: a named VM resolved through the cove VM registry.
- Probe selection: all probes by default, with `--filter` for a comma-separated
  subset.
- Output format: JSON by default for `run-all`; `--json` is accepted
  explicitly for scripts.
- Timeout: a whole-run budget, defaulting to 60 seconds.

Output:

- Per-probe status, runtime, error text, and evidence.
- Total runtime.
- Aggregate isolation score from 0 to 100.

The score is an empirical summary, not a security proof: pass is full credit,
limit is half credit, and fail or timeout is zero credit. A score below 100
means the selected reset path did not fully isolate the selected probe suite.

Probe ordering is fixed unless a future probe declares otherwise:

1. Filesystem attributes.
2. Process table.
3. Network.
4. Memory.

Filesystem runs before network because filesystem teardown can disrupt local
socket state and logs. Memory runs last because it is the most invasive probe
and may alter shared temporary state used by other probes. The orchestrator
sequences probes instead of running them concurrently so residue from one
probe is visible in the report and does not hide another probe's result.

Each probe intentionally leaves traces while it is armed. The orchestrator's
job is to correlate whether the chosen reset operation clears those traces,
not to make the probes non-destructive. If any probe leaks across a fork, then
design 013 or this design has a real bug: the empirical validation would show
that fork-from plus boundary profiles are not isolating the state they claim
to isolate.

## Non-goals

This doc does not say soft reset is useless. It says soft reset is not an
isolation boundary. It may still be useful for fast inner-loop work where the
same operator owns all data and accepts warm-guest residue.
