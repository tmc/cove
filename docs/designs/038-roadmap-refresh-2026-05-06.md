# R6 roadmap refresh

Status: accepted for roadmap update
Date: 2026-05-06
Notebook: `79a32e96-8e1c-4e89-9385-20193e3a8209`

## Context

The v0.2.1, v0.3.0, and v0.4.0 release drafts are nearly ready, but the last
few implementation waves expanded public-facing surface faster than the product
story narrowed. The strongest current cove wedge is still local-first Apple
Silicon VM execution: APFS-backed fork/restore, vsock guest control, private
runner images, and agent adapters.

Two recent work clusters needed a roadmap correction:

- `coved` observability added useful daemon metrics, lifecycle counters,
  Prometheus output, webhooks, and a local web UI, but it risked looking like a
  browser control plane.
- Cirrus migration docs added useful operator guidance, but README, landing,
  issue-template, and release-copy surfaces made the private docs read like a
  public marketing launch before the name and release gates were settled.

## Decisions

1. Keep `coved` metrics and event plumbing. They are host-local operator
   observability, not a replacement for the native VM GUI or per-VM control
   socket.
2. Make the `coved` web UI opt-in. Metrics stay enabled on localhost by
   default; the browser UI is preview/status-only and requires `ui_addr` in
   `~/.vz/cove.toml`.
3. Treat webhook delivery failures as real errors. Non-2xx responses must
   retry and be logged instead of silently succeeding.
4. Keep technical Cirrus migration docs: the walkthrough, checklist, and
   migration doctor recipe. Park public launch surfaces until release, privacy,
   and name gates are cleared.
5. Add a v0.5 planning row set focused on stabilization, package boundaries,
   and product-name resolution. Do not use v0.5 to add another broad product
   surface before the existing surfaces are coherent.
6. Add a public-surface design checkpoint. Any batch that adds two or more
   user-visible public surfaces must have a short design note before landing.

## Non-Decisions

- This refresh does not choose the final public product name.
- This refresh does not cut release tags.
- This refresh does not publish a public registry, tap, signed image channel,
  or hosted CI claim.
- This refresh does not remove the technical Cirrus migration material.

## Release Note Rules

The v0.4.0 notes should say:

- `coved` is local observability and lifecycle coordination.
- `coved` web UI and webhooks are preview/operator surfaces, not the control
  plane.
- Cirrus docs are guidance and examples, not an automatic `.cirrus.yml`
  migration engine or hosted queue replacement.
- Public OCI distribution and curated image channels remain deferred until the
  name and release gates clear.

## Follow-Up Work

- Split command routing and daemon status surfaces out of the package-main
  monolith as v0.5 stabilization work.
- Keep AI, cloud, and vendor SDK dependencies behind adapters or command
  packages.
- Add stricter proto and CLI compatibility gates before the next public-facing
  batch.
- Re-run the roadmap notebook before v0.5 is planned, using only current repo
  sources and refreshed external trademark/market facts.
