# cove bench gui — leaderboard mechanics (design 047 slice 6)

This documents the leaderboard *mechanics*: the versioned corpus, the held-out
split, the in-guest egress lockdown, the result-submission format, and the
maintainer-run verified tier.

> **Privacy / brand gate (load-bearing).** Public leaderboard *publication* is
> gated on the ROADMAP privacy/brand decision and is **NOT** done here. The cove
> repo is private; nothing in this slice publishes a public leaderboard, a public
> corpus, or any public URL. This file and the code it describes are the
> *mechanics only*. Do not publish until the gate clears (design 047 §9 slice 6,
> §12: "No public leaderboard until the privacy/brand gate clears").

## Versioned corpus + held-out split

A result is comparable only against another result produced by the same corpus
and verifier. Both are versioned together (design 047 §7):

- **`CorpusVersion(tasks)`** — a SHA-256 digest over each task's id, image,
  domain, and evaluator func+conj (the fields that decide *what* is scored and
  *how*). An instruction reword does not change it; a scoring change does.
- **`VerifierVersion()`** — a SHA-256 digest over the schema version, the
  registered metric names, and the supported getter kinds (the verifier's
  externally observable surface).
- **`cove_commit`** — recorded in the manifest because the verifier's Go
  implementation is pinned by the commit, not by the surface hash.

A **`Manifest`** (`internal/guibench/manifest.go`) pins all of these plus the
public/held-out task partition. Build and print it with:

```
cove bench gui manifest -corpus docs/benchmarks/guibench/example-corpus
```

### Public vs. held-out partition

A task joins the **held-out** partition by listing the subset name `held_out`
(`guibench.SubsetHeldOut`) in its `subset` field. Held-out tasks are reserved for
maintainer-run verified scoring and are not part of the public split, so an
external submitter cannot tune against them (design 047 §8, §11). Everything not
held out is **public**. The manifest records both partitions by task id.

## In-guest network-egress lockdown (contamination defense)

A Berkeley RDI analysis found an exploit agent could `wget` a gold-reference file
(embedded at a public URL inside the task config) into the exact path the
evaluator checks, reaching a bogus 73% score (design 047 §8). cove closes this
two ways:

1. **Gold references stay host-side in the verifier.** They never appear in the
   task config and are never reachable from the guest. A task carries only the
   *host names it is permitted to reach* (`network_allow`), never a gold value.
2. **Egress is denied by default during scoring.** The runner derives an
   `EgressLockdown` per task (`guibench.TaskEgress`):
   - A task with no `network_allow` runs **fully offline** — mapped to cove's
     `offline` network policy, the only policy that disables the guest's virtual
     network outright (`enforcement=virtual-network-disabled`).
   - A task that genuinely needs the network lists exactly the domains it may
     reach; only those pass (suffix-matched), and an arbitrary gold-reference
     host is still denied.

The wiring is `egressPolicyFor` (`bench_gui_verify_cli.go`), which produces a
cove `NetworkPolicy` (`network_policy.go`) satisfying `guibench.EgressPolicy`.
The runner's guard `EgressLockdown.CheckPolicy` confirms the applied policy
denies the gold host (and admits any allowlisted host) before the agent runs.

> The live proof that egress lockdown *actually blocks a running agent from
> wget-ing the gold file* requires operator Apple-Silicon hardware and is the
> live gate; the host-side policy wiring is unit-tested without a VM
> (`internal/guibench/egress_test.go`, `bench_gui_egress_test.go`).

## Result-submission format

An external party files a `submission.json` in a bundle directory. The schema
(`guibench.Submission`) records:

- `provider`, `model`, `agent_ref` — what was run (the `agent_ref` pins the
  submitted agent code a maintainer re-runs to verify).
- `corpus_version`, `verifier_version` — what it was scored against; these MUST
  match the manifest a verified run uses.
- `cove_commit`, `host`, `submitted_at` — provenance (design 047 §7).
- `tasks[]` — per-task `score` (mean over `runs`) and the individual pass@1
  `runs` so variance is recoverable.
- `tier` — see below; a submitter's own claim here is ignored.

`Submission.Validate` checks structure (schema version, identity fields, in-range
scores, no duplicate task ids, self-consistent mean) without touching a VM.

## Maintainer-run "verified" tier

Self-reported numbers are routinely dismissed (aggregators carry vendor
self-reports, not maintainer-verified numbers). A result is **verified** only
when a maintainer executed the run *and* the bundle's corpus and verifier
versions match the manifest (design 047 §11). Stamp a bundle with:

```
# maintainer-executed run, versions pinned -> verified
cove bench gui verify-bundle -corpus <corpus> -maintainer ./bundle

# anything else (no -maintainer, or version mismatch) -> unverified
cove bench gui verify-bundle -corpus <corpus> ./bundle
```

`verify-bundle` validates the bundle, checks it against the manifest the corpus
pins, stamps `tier` verified/unverified, and writes the stamp back into
`submission.json`. A submitter cannot self-stamp verified: `StampVerified`
overwrites the tier and only sets `verified` for a maintainer run over matching
versions.

## What is BLOCKED here (operator hardware / credentials)

- The **live egress-block proof** (an agent inside a fork cannot `wget` the gold
  file) — needs Apple-Silicon hardware + a forked guest.
- A **citable verified-tier run** (a real maintainer-executed scored bundle) —
  needs the full live matrix (hardware, TCC grants, provider API keys).

Both are the live gates of design 047 slice 6; the mechanics, schemas, wiring,
and their VM-free unit tests are complete and green.
