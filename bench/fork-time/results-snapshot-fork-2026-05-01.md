# cove fork benchmark — snapshot-seeded (Phase 2)

- Date: 2026-05-01T10:33-07:00
- Host: darwin/arm64, macOS 26.4.1, Apple M4 Max
- Cove: `cove dev` worktree at `vz-macos-f1-phase-2`, branch `codex/f1-phase-2`, base `f1f1036` (Phase 0 + uncommitted Phase 2)
- Mode: snapshot-seeded fork via `cove fork <parent> <child> -snapshot <name>`
- Runs: 1 (Validation #2 live smoke for design 013 Phase 2)

## Outcome: A2 (cold-boot fallback)

Snapshot restore was rejected by VZ; fork fell back to cold boot. **A2 is the expected behavior in current Phase 2** — see Root cause below.

| Parent | Snapshot | Child | Fork wall | A2 cold-boot fork→agent | Result |
|---|---|---|---:|---:|---|
| `overlay-fresh-20260429-165003` | `phase2-smoke` (1.50 GiB vmstate) | `phase2-smoke-child` | 1.36 s | 73 s | A2 cold-boot, agent reachable |

## Detailed timings

- **Fork wall-clock (CoW + aux + vmstate copy)**: 1.36 s real / 0.25 s user / 1.11 s sys
  - disk.img: APFS clonefile (instant, CoW)
  - aux.img: 33 MB byte-copy
  - suspend.vmstate: 1.5 GB byte-copy from `<parent>/snapshots/phase2-smoke.vmstate` (verified byte-identical via shasum: `470b3509…`)
- **Restore attempt**: failed within first ~1 s of `cove run`
  - Error: `domain=VZErrorDomain code=12: invalid argument` from `RestoreMachineStateFromURL`
  - vmstate moved aside as `suspend.vmstate.broken-20260501T173156Z` (forensic copy preserved)
- **Cold boot to agent reconnected**: 73 s wall-clock
  - Detection method: structured log line `agent-health: reconnected … downtime=1m10.04s` from cove process
  - Note: agent identity persisted across the cold boot (same vsock CID + token; agent inside guest restarted as part of OS restart)

## Root cause: machine.id regeneration on fork

`fork.go:83` calls `CloneVM` with `CopyMachineID: false` (correct for plain fork — siblings get distinct Mac identities to avoid conflicts when both boot from cold). For snapshot-seeded fork, this rotates the child's `machine.id` (specifically the ECID inside the binary plist), which makes the snapshot's saved Mac identity diverge from the child's runtime identity. VZ's `RestoreMachineStateFromURL` then rejects the restore with `invalid argument`.

Diff (parent vs child machine.id, ECID region):

```
parent: …TECID 137d fdf7 2397 60f2 a7…
child:  …TECID 1400 0000 0000 0000 00 b4 0b6f d8fb cd53 …  (regenerated)
```

This is intentional Phase 2 behavior (per dispatch STOP-AND-ASK #2 resolution: no `-recover-identity` auto-apply, `printForkUsage` documents that siblings boot as the same Mac for plain fork). The snapshot-seeded fork inherits the same identity-rotation policy.

## A1/A2 design implications

Design 013 frames A1 (instant resume from snapshot) vs A2 (cold-boot fallback). In current Phase 2, **A1 is unreachable** because every fork rotates machine.id, which alone is sufficient to fail the restore — independent of `suspendConfigFingerprint` matching (which would only matter for the host-side suspend.json check, not VZ's own state validation).

The dispatch's A1/A2 dichotomy was incomplete; in practice, A2 is the only outcome today. This is acceptable for Phase 2 ship — see Recommendations.

## Verification of Phase 2 implementation

Despite A2 firing, the Phase 2 implementation is working correctly:

- `ForkVMWithSnapshot` correctly stages aux.img + suspend.vmstate (byte-exact copy from snapshot).
- Lineage is recorded in child's config.json (`ParentVM`, `ParentSnapshot`, `ForkedAt`).
- Phase 0 run.lock interaction works: parent must be stopped before snapshot fork (test `TestForkVMWithSnapshot_RejectsRunningParent` covers this).
- The CoW + copy fork itself is fast (1.36 s, dominated by 1.5 GB vmstate copy).
- A2 fallback path engages cleanly: the broken vmstate is moved aside and cold boot proceeds without operator intervention.
- Total fork→reachable on A2 (74 s wall-clock end-to-end) is comparable to plain `cove fork` cold-boot (no instant-resume win, but no regression either).

## Recommendations / follow-ups

1. **Reach A1 via opt-in identity preservation** — add `cove fork --preserve-identity` (or fold into `-snapshot` semantically) that calls `CloneVM` with `CopyMachineID: true`. Document the trade-off (sibling Mac identity collision risk if both run cold) and require parent stopped + auto-detect concurrent-boot conflicts. Owner: future Phase 2.5 or Phase 3 dispatch.
2. **Update design 013** — drop the A1/A2 dichotomy framing for current Phase 2; reframe as "snapshot-seeded fork (best-effort, currently always cold-boots)" until #1 ships. Note A1 path is gated on identity preservation.
3. **Strengthen unit tests** — add `TestForkVMWithSnapshot_PreservesParentMachineIDWhenRequested` (red until #1) and a test that asserts current default rotates machine.id (lock current behavior in).
4. **CLI help text update** — `printForkUsage` snapshot section should call out "child boots cold from clean shutdown of snapshot state" instead of implying instant resume.

## Cleanup

After this run:

- Child VM `phase2-smoke-child`: cold-booted successfully, agent reconnected, then SIGTERMed to free resources. `~/.vz/vms/phase2-smoke-child/` will be removed before commit.
- Parent VM `overlay-fresh-20260429-165003`: stopped. Snapshot `phase2-smoke` will be deleted before commit.
- `suspend.vmstate.broken-…` artifact will not be retained (forensic evidence captured in this doc).

## Cross-references

- Design 013 (`docs/designs/013-fork-resume-fast-path.md`): Phase 2 spec
- Phase 0 PR `#2` (run.lock) — merged at `f1f1036`
- Phase 2 PR — to be opened on `codex/f1-phase-2` (dispatch authorization: push + open, do not merge)
- Implementor session `20196240`, conductor `0AA1EC69`
