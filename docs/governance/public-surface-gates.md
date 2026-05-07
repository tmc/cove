# Public surface gates

Status: active
Date: 2026-05-06

Use this gate before landing changes that make cove look more public,
platform-like, or commercially launched than the current release plan supports.

## Design checkpoint trigger

Write a short design note before landing a batch that adds two or more of these
surfaces:

- README funnel or top-level quickstart;
- landing page, blog draft, public comparison, or migration campaign;
- GitHub issue template or discussion template;
- new top-level CLI command, public flag family, or help text section;
- daemon listener, webhook, browser UI, or network-exposed endpoint;
- public registry, signed image channel, tap, or installer channel.

The note should state the user problem, release target, default posture,
security boundary, docs surface, and the rollback plan if the surface proves too
broad.

## Name and distribution gate

Until the product-name decision is closed, do not ship:

- public curated image registry;
- signed public image channel;
- Homebrew tap launch copy;
- public landing page or blog that treats the current name as a cleared
  commercial mark.

Private docs and direct operator outreach can continue, but should avoid name
claims and hosted-service promises.

## Dependency boundary

AI, cloud, registry, and vendor SDK dependencies belong in adapters, command
packages, or narrowly-scoped internal packages. They should not become required
dependencies of VM lifecycle, disk, provisioning, or control-socket code.

When a dependency crosses that boundary, the design note must explain why the
standard library or an existing local package is insufficient.

## Release note honesty

Every release note claim must be backed by one of:

- a checked-in test or benchmark artifact;
- a specific commit hash;
- a local runbook with pass/fail output;
- a linked design decision that explicitly marks the feature as deferred or
  preview.

If a feature is private-only, preview, guidance-only, or known unreliable on a
guest family, say that in the release notes.
