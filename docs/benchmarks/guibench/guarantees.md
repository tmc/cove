# guibench guarantees for third-party corpora

What cove guarantees to a third party who runs their own native-macOS
computer-use corpus on it (design [047](../../designs/047-gui-agent-benchmark-harness.md)
phase 3, the reusable-harness wedge). Two guarantees carry the benchmark's
credibility: **per-task fork isolation** and **honest privilege tiers**.

## 1. Fork isolation: every task sees a fresh whole-OS state

**Guarantee.** Every task runs in its own ephemeral RAM-overlay fork of the base
image. No task ever observes another task's state, and nothing a task does
survives into the next task.

**Mechanism.** The reference backend forks a stopped macOS VM with APFS
clonefile and mounts the child's writes onto a RAM overlay; on shutdown the
overlay is discarded (design [013](../../designs/013-vm-fork.md) Model B). The
"throw everything away" property of the overlay *is* the reset — there is no
roll-back step that could be skipped or get it wrong. One fresh fork per task,
always; **no soft reset** (design [015](../../designs/015-soft-reset.md) closed
soft reset as an isolation primitive).

**Why this is stronger than the field.** Every other benchmark has a weaker
reset: WebArena leaks server state across all 812 tasks (`require_reset` is false
for all of them); AndroidWorld snapshots a per-app data dir, not the whole OS;
WindowsAgentArena reverts a 30 GB golden image; OSWorld recreates a cloud
instance. cove gives true per-task whole-OS hermetic state, and a stopped-VM fork
costs ~130–140 ms (`bench/fork-time/`), so the isolation that is expensive
elsewhere is the cheap default here.

**The one caveat you must respect — shared SEP identity.** Cloned siblings share
the base image's Secure Enclave / iCloud identity unless regenerated. Any task
touching Apple ID sync, iCloud Drive, Find My, Keychain, or FairPlay would be
corrupted by duplicate identities across concurrent forks. **v1 rule:** exclude
iCloud/Keychain/Apple-ID tasks, or run the existing `-recover-identity` flag per
fork for any task class that needs a distinct identity (design 047 §6).

**What you can rely on the fork to reset** (the design treats this as a *tested
invariant*, not an assumption): `cfprefsd`-cached preferences, the Launch
Services database, `TCC.db`, the Spotlight index, and app SQLite/WAL files. The
RAM-overlay model makes this hold by construction (all writes vanish), and slice
2's fork-reset proof verifies it empirically before the corpus relies on it.

## 2. Privilege tiers: honest about what a fresh fork can read

**Guarantee.** A getter is classified by the exact TCC grant it needs, and the
harness refuses to score a corpus on a base image that lacks the grant — rather
than letting a getter *silently fail* and read the corpus as "agents fail at
macOS."

**The trap.** A naive "non-Apple-Events getters need no grant" assumption is
wrong on modern macOS. Reading a protected app's SQLite store (Mail, Messages,
Safari history) or many `~/Library` paths requires **Full Disk Access**, which a
fresh VM does not have, and the read otherwise returns nothing with no error.
cove's own `cove doctor` TCC/FDA probe exists precisely because this access
silently fails.

**The tiers** (`guibench.Tier`, `Tier.Grant()`):

| Tier | Grant | Getters | What a fresh fork can do |
|---|---|---|---|
| **A** | none | `exec`, `file`, `defaults`, `screen_ocr` | Runs on any fresh fork, no provisioning. |
| **B** | Full Disk Access | `sqlite`, `protected_file`, `tccdb` | Needs FDA baked into the base image. |
| **C** | Apple Events + Accessibility | `applescript`, `accessibility` | Needs AE + AX automation grants — **independent TCC services from FDA** (granting FDA does NOT grant Apple Events). |

**How the guarantee is enforced.** `guibench.MaxTier(tasks)` computes the highest
tier any getter in your corpus requires. The base image is provisioned with
exactly those grants and the grant state is verified by `cove doctor` before the
image is saved — grants are baked into the image, never done per run. The
`cove bench gui image-check` flow (and, in code, `guibench.CanRun(backend,
tasks)`) refuses to run a corpus whose `MaxTier` exceeds what the image carries.

**AX-tree is the reliable synchronous probe.** Tier-B SQLite/file reads are
FDA-gated *and* subject to async-flush staleness; the Accessibility tree reports
live UI state directly and synchronously. For GUI-state goals, prefer the Tier-C
`accessibility` getter over a brittle OCR/pixel verifier.

## 3. Contamination defense: gold references stay host-side

**Guarantee.** During scoring a task runs **fully offline** by default, so the
agent cannot fetch a gold-reference file and replay it into the path the verifier
checks (the Berkeley RDI exploit that reached a 73% bogus score on OSWorld).

**Mechanism.** Gold references live host-side in the verifier (typically the
`options.expected` literal computed from the task's own parameters), never in a
location the guest can reach. `guibench.TaskEgress(task)` derives the lockdown:
deny-all by default, or a `task-allow` policy carrying exactly the
`network_allow` domains a task genuinely needs. `EgressLockdown.CheckPolicy`
confirms the applied cove network policy matches the lockdown before the agent
runs.

## 4. Versioning: a number is comparable only against the same verifier

**Guarantee.** A scored result records the corpus version, the verifier version,
the schema version, and the cove commit, so two numbers are comparable only when
they were produced by the same corpus and verifier.

**Mechanism.** `guibench.CorpusVersion(tasks)` digests task identities + scoring
shape; `guibench.VerifierVersion()` digests the metric and getter surface;
`guibench.BuildManifest` pins both plus the public/held-out partition. Verifier
brittleness is the field's dominant failure mode (OSWorld-Verified fixed 300+
verifier bugs), so the corpus and verifier are **versioned together** and every
metric is a pure, VM-free, table-driven-tested function.

## 5. How to run your own corpus

1. **Author tasks** to the [task-schema spec](task-schema.md). Start from the
   [example corpus](example-corpus/) and `cove bench gui validate -corpus <dir>`
   in CI until clean.
2. **Decide your tier.** If every getter is Tier A (the example corpus is), a
   fresh fork needs no provisioning. For Tier B/C, provision the base image with
   FDA and/or AE+AX and verify with `cove bench gui image-check`.
3. **Swap the backend if you are not on cove's VZ fork.** The engine is
   substrate-agnostic behind the `guibench.Backend` interface (`Acquire` →
   `Session` with `Probe`/`RunAgent`/`Close`, plus `MaxTier`); the VZ-fork
   backend is the reference implementation. A third party supplies a `Backend`
   over a different macOS substrate and runs the same corpus unchanged. See
   [provider-interface.md](provider-interface.md).
4. **Score and report** with `cove bench gui run` / `report`, then
   `verify-bundle` to stamp the tier. Only maintainer-executed runs with matching
   corpus + verifier versions are citable as verified headline numbers.
