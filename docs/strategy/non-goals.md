---
title: cove non-goals
status: Draft
date: 2026-05-05
---

# cove non-goals

This page records deliberate omissions so roadmap discussions do not drift back
to old ideas that were reviewed and rejected.

## Packer plugin shim

Packer plugin shim work is not being pursued.

Why:

- cove already has a direct image path: `cove image build`, `cove image push`,
  `cove image pull`, and `cove image verify`.
- Tart already ships the official Apple-Silicon Packer plugin integration.
- A cove plugin would mostly duplicate the local image boundary instead of
  unlocking a new operator workflow.
- The remaining cove roadmap is better spent on fork/restore isolation, CI,
  fleet, daemon, and guest-install stability.

Supported alternative:

- Use the Tart Packer plugin for the builder side.
- Hand the resulting artifact to cove through the existing image and
  fork-from flow.

This is the final decision for issue #241 unless the product direction changes
materially.
