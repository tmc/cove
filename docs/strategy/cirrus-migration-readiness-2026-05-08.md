---
title: Cirrus migration readiness audit
date: 2026-05-08
audience: operators planning to leave Cirrus before 2026-06-01
companion: docs/migrations/from-cirrus.md
---

# Cirrus migration readiness audit (2026-05-08)

Cirrus CI shuts down **2026-06-01** — 24 days from today. This doc audits
cove's *currently shipped* surface against the documented Cirrus surface a
typical `.cirrus.yml` user depends on. Strategic positioning lives in
[competitive-2026-05.md](competitive-2026-05.md); this is operator-facing.

## Method

Surface inventory via `git log` + `grep` against `origin/main` at this
worktree's base. Every "yes" cell cites a SHA or `file:line`. The migration
walkthrough lives at [`docs/migrations/from-cirrus.md`](../migrations/from-cirrus.md)
([`0533c1f`](../migrations/from-cirrus.md), [`fee2aa4`](../migrations/from-cirrus-checklist.md));
this doc is the readiness gap report behind it.

## Direct equivalents

| Cirrus surface | cove today | Evidence |
|---|---|---|
| `.cirrus.yml` task → run a script in a fresh VM | `.github/actions/cove-action` (composite) wraps `cove run -fork-from <image> -ephemeral` | `0985377` `8bd473e`; `.github/actions/cove-action/action.yml:1-72` |
| `container: image: …` (Linux) | `cove image build/list/rm/inspect/push/load` (private OCI) | `image_cli.go`, `image_push_load.go`; design 024 |
| `macos_instance: image: …` | local cove macOS image built from a maintained parent VM | `docs/migrations/from-cirrus.md:107-115` |
| Per-task isolation (fresh root) | APFS clonefile fork-per-job, `-ephemeral` teardown | `99b3732` (Phase 3 `-fork-from`); `eacbf5e` (`vm tree --orphans`); design 013 §5 |
| `script:` (shell) | guest agent `ExecAttach v3` (bidi stdin, resize, signal) | `agent_control_attach.go`, `shell.go`; design 023 Slice 3 |
| Persistent worker labels | GitHub `runs-on: [self-hosted, macOS, ARM64, cove]` + `cove fleet add` | `fleet_cli.go`; `docs/quickstart/fleet.md` |
| Task logs (`task-id`-scoped) | `cove runs list/show/export` over `~/.vz/runs/<run-id>/` | `030767c` `e98b54f`; `runs_cli.go:24-119` |
| Image registry push/pull | `cove image push` / `pull` (OCI via oras-go, Docker auth reuse) | `image_push_load.go`; design 024 Slice 1 (`8a106dc`) |
| Image freshness gate | `cove image verify --strict --newer-than <dur>` | `26380b8` `6f0d396`; `image_verify.go` |
| Runner preflight | `cove action doctor` and `cove action prepare-image` | `7fafe40` `6470615` `14260b3` `9e6253a`; `internal/action/{doctor,prepare}.go` |
| Per-task metrics (start/end/exit) | run JSONL + optional OTLP exporter | `8318fa7` `3f6c144` `c390eb9`; `internal/metrics/` |
| Network mode controls | `--net nat|bridged:<iface>|host-only|none|vmnet|filehandle` and named policies (`offline`, `packages`, `host-services`, `lan`, `open`) | `671754a` `1ac32f9`; `networking.go:24-85` |

## Partial

| Cirrus surface | cove today | What's missing | Effort |
|---|---|---|---|
| Whole-VM cache (`cache-key` / `fingerprint_script`) | `cove-action` accepts `cache-key` + `cache-paths`; local cache image restore/save | No content-addressed fingerprint; key must be host-computed (e.g. `hashFiles()`) | S |
| Artifacts upload | `~/.vz/runs/<run-id>/` exists; `cove runs export <id> --format gha-summary|json|tar` | No first-class **guest → host** artifact copy-out; user must `cove ctl cp` or include in script | M |
| Matrix tasks | GitHub Actions `strategy.matrix` selects per-row image | No native cove matrix expander; relies entirely on the scheduler | S (docs only) |
| Cron / scheduled tasks | GitHub Actions `schedule` triggers the workflow | cove has no built-in cron; `coved` daemon (`394b812` `42714c0`) schedules image GC, not user tasks | S (docs only) |
| Network audit | per-mode policy + `cove network logs tail` | No `cove network audit <run-id>` command; pcap available on `filehandle` mode only | M |
| Background webhook / event triggers | `coved` webhook event subscriber (`33bcf38`) | Not wired to "run image X on push"; operator must script it | M |

## Missing or workaround

| Cirrus surface | Workaround today | Effort to ship native |
|---|---|---|
| **GitHub Actions annotations** (`::error`, `::warning`, file/line) emitted from inside the guest | Print plain text; GHA renders as logs only | **M** |
| **Public Marketplace action** (`uses: cirrus-actions/...` analogue) | Private composite action at `.github/actions/cove-action`; copy/paste per repo | **L** — gated by privacy gate (cove repo private) |
| **Cirrus secrets** (`ENCRYPTED[…]` URI) → guest env | `cove-action` `secrets:` input is **reserved and rejected** (`action.yml:48-49,95-99`); operator pipes plain `env:` | **L** — secret lifecycle, redaction, and lease design needed |
| **Hosted queue** (Cirrus picks a Mac for you) | Operator owns the runner host (or fleet); GitHub `runs-on` labels do scheduling | **L** — by design, cove is operator-owned. Not a blocker; a scope decision. |
| **Multi-OS hosted CI** (Linux x86_64 / Windows runners under same `.cirrus.yml`) | cove is Apple Silicon only | **L** — out of scope per `docs/strategy/non-goals.md` |
| **Public macOS/Linux image catalog** (Tart-style GHCR images) | Operator builds locally with `cove up` + `cove image build` | **L** — gated by privacy gate; design 024 Slice 3 deferred |
| **Cirrus HTTP cache server** (CIRRUS_HTTP_CACHE_HOST) | Whole-VM `cache-key` + `cove image verify --newer-than` | **M** — would need a content-addressed blob server |
| **`only_if:` rich expression filters** | GitHub `if:` expressions on the workflow step | **S** (docs only) |
| **Auto-cancel on push** | GitHub `concurrency: cancel-in-progress: true` | **S** (docs only) |
| **Cosign-signed images / SLSA provenance** | Local provenance fields in image manifest (`26380b8`); no public signature channel | **L** — defers with public registry decision |

### Missing(L) blocker count: **5**

1. Public Marketplace action (privacy gate)
2. Cirrus-style secrets → guest env (lifecycle + redaction)
3. Hosted queue (out-of-scope by design — listed for completeness only)
4. Public image catalog (privacy gate)
5. Cosign-signed images / SLSA provenance public channel

Items 3 and 5 are scope/posture decisions, not engineering work. Items 1, 2, 4 are the real shipping cost; 1 and 4 unblock once the privacy gate flips, leaving **secrets (item 2)** as the only purely engineering-bound L blocker. Note: GitHub Actions annotations from-guest is sized **M**, not L — included above as a UX polish item.

## Recommended migration steps

For each `.cirrus.yml` task class, in this order:

1. **Inventory.** Run `find . \( -name .cirrus.yml -o -name .cirrus.yaml \) -print` and classify each task as container / macos_instance / persistent_worker / matrix / cron. (Step 1 of [`from-cirrus-checklist.md`](../migrations/from-cirrus-checklist.md).)
2. **Pick a cove host.** One trusted Apple Silicon Mac per matrix dimension. Confirm with `cove action doctor`.
3. **Build one runner image per task class.** `cove up` → install tools → `cove image build -from <vm> -tag acme/runner:<class>`.
4. **Gate freshness.** `cove image verify --strict --newer-than 168h <ref>` and `cove action prepare-image <ref> --ttl 24h` in workflow preflight.
5. **Translate `task:` → workflow step.** Use the private `./.github/actions/cove-action` composite (`docs/migrations/from-cirrus.md:20-31`).
6. **Translate caches.** Move `fingerprint_script` to GitHub `hashFiles()`; pass via `cache-key:`. Sensitive caches stay on the trusted host.
7. **Translate secrets.** Slice 1 does **not** support guest-bound secrets — keep secrets out of the guest environment until the secret slice ships, or pipe via plain `env:` for non-sensitive smoke values only. Cirrus `ENCRYPTED[…]` URIs cannot be lifted.
8. **A/B run.** Same commit, both workflows, compare exit code + test summary + `metrics.jsonl` for one soak period.
9. **Capture artifacts.** Until guest copy-out lands, end each script with explicit `cove ctl cp` or upload `~/.vz/runs/<run-id>/` as a workflow artifact.
10. **Cut over and keep `.cirrus.yml` until 2026-06-01.** Cirrus deletes itself on that date; until then it is your rollback.

## Bottom line

Cove's runner-shaped surface is **functionally complete for ~80% of `.cirrus.yml` task shapes** as of `d0877b8` (origin/main, 2026-05-08). The gaps that block migration are concentrated in:

- **Secrets** (the only purely-engineering L blocker) — operators with `ENCRYPTED[…]` URIs must defer or refactor.
- **Privacy gate** — public action / public image catalog can't ship while the cove repo is private.
- **Annotations + guest artifact copy-out** — UX polish, sized M; workarounds work today.

Operators planning a 2026-06-01 cutover should start at step 1 today.
