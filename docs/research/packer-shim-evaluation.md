---
title: Packer shim evaluation
status: Draft
date: 2026-05-05
---

# Packer shim evaluation

## Question

Should cove ship a Packer plugin shim, or should the idea be formally sunset?

## Sources

- Packer plugin docs:
  https://developer.hashicorp.com/packer/docs/plugins/creation
- Packer post-processor docs:
  https://developer.hashicorp.com/packer/docs/plugins/creation/custom-post-processors
- Packer Tart integration:
  https://developer.hashicorp.com/packer/integrations/cirruslabs/tart
- Tart repository:
  https://github.com/cirruslabs/tart
- cove image transport docs:
  [`docs/designs/024-cove-runner-images.md`](../designs/024-cove-runner-images.md)

## Evidence

### Audience size

The overlap is narrow. The plausible users are people who already run Packer
pipelines on Apple Silicon, want Virtualization.framework-backed images, and
are willing to adopt cove instead of Tart. That is a real audience, but it is a
slice of a slice. cove's active operator investment is currently higher-value in
fork/restore, CI runners, agent sandboxes, Linux guest support, and daemon /
fleet control.

### Effort

Packer plugins are Go binaries loaded through the plugin SDK. The cleanest
surface here would be a post-processor, not a new builder, but a post-processor
would mostly be a wrapper around `cove image build` semantics rather than a new
native image pipeline.

A credible post-processor shim is still not free:

- plugin scaffolding and RPC wiring
- config schema and validation
- artifact handoff from the builder to cove
- tests for happy-path and missing-cove failures
- release and compatibility maintenance with the Packer SDK

That is small compared to a full builder, but it is still a permanent plugin
surface.

### Maintenance burden

Packer plugin development is not just a one-off binary. The SDK and plugin
distribution model are explicit, versioned surfaces. That is a reasonable
ecosystem, but it is another compatibility axis for cove to maintain even
though cove already has a direct local image flow.

### Existing alternatives

Tart already ships the official Packer integration for Apple Silicon image
builds:

- Tart docs expose a Packer integration page.
- HashiCorp documents Tart as a Packer integration.
- Tart's repository explicitly advertises the Packer plugin.

That makes Tart the existing answer for users who already want a Packer builder
on Apple Silicon.

### Strategic value

cove already covers the main operator path directly:

- `cove image build`
- `cove image push|pull`
- `cove image verify`
- `cove run -fork-from <image-ref>`

Those commands already solve the image production and reuse problem without
forcing users through Packer. The product's current wedge is operator-owned VM
forking, CI runners, and sandboxed agent workflows. A Packer shim would mostly
duplicate the local-image boundary rather than unlock a new category.

## Recommendation

**Sunset the Packer plugin shim.**

Treat `cove image build` as the direct image path and leave Packer integration
to Tart. If a user wants to use Packer in a pipeline, the supported pattern is
to keep the builder on the Tart side and hand the result to cove via the
existing image and fork flow.

## Decision shape

- No shim plugin in cove.
- No new Packer builder or post-processor surface.
- Document the non-goal explicitly so the roadmap does not drift back to
  "maybe."

## References

- Packer plugin creation: https://developer.hashicorp.com/packer/docs/plugins/creation
- Packer post-processors: https://developer.hashicorp.com/packer/docs/post-processors
- Tart Packer integration: https://developer.hashicorp.com/packer/integrations/cirruslabs/tart
- Tart repository: https://github.com/cirruslabs/tart
