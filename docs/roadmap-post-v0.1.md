# cove post-v0.1 roadmap

Navigation aid for everything deferred from v0.1.0. Not a design doc.

Source of truth for release strategy: `docs/designs/011-beat-lume-roadmap.md`.

## Already-deferred (parked on the `next` branch)

Full reproduction and root-cause hypotheses live in
`docs/blockers-next.md` on the `next` branch
(`git show next:docs/blockers-next.md`). See "Branch hygiene" below
for the plan to surface these as a draft PR rather than a long-lived
staging branch.

1. **`cove up` fresh install produces no disk.** Install reports 100%,
   then provisioning fails because `~/.vz/vms/<name>/` never
   materialized. Path-resolution mismatch between install target and
   post-install `stopVMAndInject`.
2. **`cove pull` rejects upstream lume-format images.** trycua/ghcr
   images ship `disk.img` as tar-split parts with a different media-type
   annotation schema than cove's LZ4-chunk importer expects. Tar-split
   importer not implemented.

## 0.2 candidates (not yet started)

Branch off `main`, open a (draft) PR per item, merge back to `main` when
green. "Suggested branch" column is just a name hint.

| Item | Motivation | Acceptance | Suggested branch |
|------|------------|------------|------------------|
| Fix `cove up` fresh install | Blocker #1 above; `cove up` is the headline UX | `cove up -user X -ipsw Y` reaches desktop on a machine with no prior VM | `fix/up-fresh-install` |
| Lume-format pull importer | Blocker #2; lume interop is a public benchmark | `cove pull ghcr.io/trycua/…` extracts a bootable disk | `feat/lume-tarsplit-pull` |
| `serve` VM discovery scope | Known wart: `~/.vz/<name>/control.sock` VMs are invisible to HTTP gateway | `GET /v1/vms` returns hermes alongside `~/.vz/vms/*` VMs | `fix/serve-discovery-scope` |
| `ctl agent-exec --` parsing (ROADMAP #28) | `ctl agent-exec -- ls` passes `--` as argv[0] | `--` terminates option parsing; `ls` is the exec target | `fix/agent-exec-dashdash` |
| `snapshot save` async LRO | 9 GB `.vmstate` save racks up i/o timeouts on the sync path | `ctl snapshot save` returns an operation ID; poll `/v1/operations/<id>` | `feat/snapshot-save-async` |
| LaunchAgent mode for vz-agent (ROADMAP #22) | In progress; unblocks FDA/TCC + VirtioFS from guest agent | `vz-agent -mode agent` runs on port 1025 in user session; `UserExec` RPC round-trips | `feat/launchagent-mode` |
| Agent auto-upgrade on boot (ROADMAP #19) | Stale guest agents silently break RPCs | Host compares version on connect, replaces `/usr/local/bin/vz-agent` if older | `feat/agent-auto-upgrade` |

## Half-baked code on `main` (clean up before or after 0.2)

### `POST /v1/vms` create-VM stub — **RECOMMEND: keep as-is**

- **Where:** `serve_gateway.go:395-421` — `handleCreateVM`
- **What's wrong:** Creates an LRO, then immediately fails it with
  `code="not_implemented"` referencing a defer doc.
- **Why keep:** It's honest and documented. Proves LRO plumbing
  end-to-end (clients can exercise the operations API against a stub).
  Removing the route would force HTTP clients to guess what 0.2 will
  look like.
- **Cost of keeping:** ~25 loc dead handler. Worth it.

### Connect-RPC scaffolding (14 `CodeUnimplemented` returns) — **RECOMMEND: defer decision to 0.3**

- **Where:** `proto/agentpbconnect/agent.connect.go:428-476` (Agent, 13
  methods), `:572-576` (UserAgent, 2 methods)
- **What's wrong:** Generated Connect-RPC handlers from an experimental
  migration (ROADMAP: "Control socket → gRPC migration, #20, deferred").
  All return `CodeUnimplemented`. Dead weight in generated code.
- **Why defer:** It's *generated*, so it doesn't rot from neglect — it
  rots only if the `.proto` moves. Killing it means also deleting
  `proto/agent.proto` → and the migration plan is still on the roadmap.
- **Cost of keeping:** ~160 loc generated, invisible to users, zero
  binary size impact (tree-shaken).
- **When to revisit:** 0.3, bundled with the actual control-socket →
  Connect migration.

### Hardcoded `Status: "running"` in VM listing — **RECOMMEND: fix before tag**

- **Where:** `serve_gateway.go:388`
- **What's wrong:** `GET /v1/vms` reports every registered VM as
  `"running"`, even if paused/stopped.
- **Kill-vs-fix:** Fix — either query `g.routes[name].status()` or
  strip the `Status` field entirely. Stripping is ~2 loc and more
  honest than lying. Fixing properly is ~15 loc (needs per-route status
  accessor, may not exist today).
- **Recommended minimal fix:** Strip the field. `GET /v1/vms` becomes
  `{"vms": [{"name": "..."}]}`. Clients that need status call
  `GET /v1/vms/<name>/status`.
- **Time:** ~5 minutes + a test update.

## 0.3+ themes (high-level only)

From `docs/designs/011-beat-lume-roadmap.md`:

- **0.3 — Build and caching moat.** `cove build` with content-addressed
  cache keys, APFS block-diff cache, secrets tmpfs, canonical build
  examples. Strongest differentiator vs lume.
- **0.4 — Shared-host and CI hardening.** External secret providers
  (`1password://`, `vault://`, `sops://`), provenance/artifact metadata,
  shared-host auth hardening, agent fleet hygiene, optional browser
  display bridge.

## Branch hygiene

Trunk-based with draft PRs for parked work. Solo maintainer; a long-lived
`next` staging branch is overhead without payoff (rebases against `main`,
two places to look for "current state").

- **`main`** = trunk. All work lands here. Tags cut from `main`.
- **Feature branches** off `main`. Open PR (draft if not ready) → merge
  back to `main` when green.
- **Parked work** = a draft PR that doesn't get merged yet. The PR is the
  visible "this is deferred" signal; the branch lives on `origin` but
  isn't merged.

### What happens to the existing `next` branch

`next` currently holds two real reproductions in `docs/blockers-next.md`
plus the smoke-test history. To migrate:

```bash
git push origin next                            # if not already there
gh pr create --base main --head next --draft \
  --title "deferred from v0.1: cove up + lume-format pull" \
  --body-file docs/blockers-next.md
```

The PR sits open as the bookmark for those blockers. When 0.2 work picks
up either item, branch off `main` (not `next`), reference the draft PR,
and close the draft once superseded.

If you actively prefer the staging-branch model (e.g., 0.2 grows to
multiple intertwined features that want to stabilize together before
hitting `main`), the original proposal — `next` as merge target,
feature branches off `next`, `next` → `main` for the release — is a
known-good fallback. Don't reach for it preemptively.
