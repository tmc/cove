---
status: Draft
date: 2026-05-05
---

# cove v0.3.1 release notes

v0.3.1 is the operator-release after the build/cache GA. The focus moved from
raw build execution to the surrounding surfaces that make cove usable as a CI
substrate and a private runner platform:

- run metrics and run history
- network policy and auditability
- agent-sandbox packaging
- image freshness and provenance
- GitHub Actions runner support
- Cirrus migration guidance

This release stays pragmatic. It does not change the public/private posture of
the repo, and it does not introduce a public OCI channel.

## Operator UX

### `cove runs list`, `cove runs show`, `cove runs export`

The run history surface now turns `~/.vz/runs/<run-id>/` into a first-class
operator view.

`cove runs list` shows recent runs with image ref, VM name, status, duration,
and exit code. `cove runs show <run-id-prefix>` expands one run into its timing
and artifact details. `cove runs export <run-id-prefix>` packages a run for
inspection or transport.

This is the visible layer on top of the metrics work that landed in v0.3.0.
The important difference is that operators no longer have to dig through the
run directory by hand just to answer basic questions like "what ran?" or "why
did it fail?"

### `cove image verify`

`cove image verify <ref>` adds an explicit freshness gate for local and pulled
images. It checks that the manifest parses, the image layers are complete, the
disk matches the recorded size, the agent advertises `execattach.v3`, and the
cove build identity matches the current binary.

The command reports `PASS`, `WARN`, or `FAIL`, with `--json` for automation and
`--strict` for workflows that want missing `execattach.v3` to fail instead of
warn.

This matters because cove now relies on image reuse in more places: runner
images, fork-from flows, and CI preflight. A stale image should fail loudly
before it becomes a job failure that looks like a runtime bug.

### `cove action doctor` and `cove action prepare-image`

The private GitHub Actions executor gained preflight commands for host and
image readiness.

`cove action doctor` checks the host-side prerequisites: signed binary,
entitlement, free space, network helper, and writable run artifacts.

`cove action prepare-image <ref>` checks that a local image can actually serve
as a runner base: current agent, runnable shell, runner dependencies present,
enough disk headroom, and no stale forks.

These commands are small, but they matter. They turn "works on my runner" into
repeatable setup validation before a workflow starts failing at runtime.

### `cove image build` provenance

Runner images now record provenance in `manifest.json`: cove commit, agent
commit, agent features, build recipe, source image, build time, default
network, and default sandbox.

That provenance is what makes `cove image verify` useful. It also gives
operators a way to answer "what exactly is this image?" without opening the VM.

## CI

### GitHub Actions executor Slice 2 cache reuse

The private cove GitHub Actions executor added Slice 2 cache reuse. Cache keys
map to local cove images, restore hits fork from the cached image, and first
writer wins on save. The cache stays local to the trusted runner host.

The cache is intentionally not a shared mutable VM. It is another image layer
under the same fork-per-job model.

### Private action runner packaging

The `cove-action` surface now has a sharper separation between host preflight,
image preflight, and the job itself. That makes the runner setup easier to
validate before the workflow is allowed to consume it.

### T71, T72, T73, T74

The CI-adjacent releases around this cut are the main story:

- `cove action doctor` and `cove action prepare-image`
- `cove runs list`, `show`, and `export`
- `cove image verify`
- unified `cove agent-sandbox run`

Those surfaces are meant to make cove easier to adopt as a private runner
platform without turning it into a hosted service.

## Adapters

### Unified agent-sandbox run

`cove agent-sandbox run` now provides one command for the supported provider
surface. It is the packaged computer-use entrypoint for OpenAI, Anthropic,
Gemini, and related adapter flows.

The point of the command is not novelty. It is to give the user one place to
start an agent-backed fork, without having to remember which provider-specific
wrapper to run first.

### Adapter replay recording

The sandbox adapter work also picked up replay recording for agent sandbox
sessions. That gives a practical debugging trail when a provider tool flow does
not do what the operator expected.

### Interactive exec verification

ExecAttach interactive verification landed as part of the shell and agent
surface hardening. The interactive path is now treated as a real contract, not a
best-effort UI convenience.

## Networking

### Network policy v1

`cove run` and `cove up` gained a small, explicit network policy surface:

- `nat`
- `bridged:<iface>`
- `host-only`
- `none`
- `vmnet`
- `filehandle`

That made network behavior something the operator can choose, instead of a
hidden implementation detail.

### Network audit and named policies

The policy surface grew from raw modes into named policy parsing and audit
logging. That matters for repeatability: a CI job can now say what kind of
network it expects, and the host can record what it actually got.

### Network policy v2

Named policies arrived after the initial mode surface. The intent is to make
policy selection less ad hoc and more readable in workflows and release notes.

## Docs

### Image transport and provenance docs

The image docs were updated to reflect the shipped OCI transport and the new
image verification flow. The important thing here is the contract, not the
mechanism: image transport is now explicit, and freshness is not implied.

### Release note and design documentation

The release prep also picked up the following drafts and specs:

- design 030 for GHA executor Slice 2 cache reuse
- the Cirrus migration draft
- the competitive matrix against Lume and Cirrus

Those documents are not product features, but they explain the direction of the
release: private runner workflows, explicit image hygiene, and a migration path
for teams displaced by Cirrus shutdown.

### Quickstart and reference updates

The quickstart and reference docs were tightened around the surfaces that now
ship:

- agent-sandbox quickstart
- networking reference
- GitHub Actions executor reference
- CLI reference for `runs`, `image verify`, and related commands

## Compatibility and limits

- The repo remains private.
- No public OCI image channel ships here.
- Public Marketplace packaging is still not the release boundary.
- Legacy images still inspect and list, but they show up as legacy in verify
  output.
- `cove image verify` is a gate, not a repair tool.

## Summary

v0.3.1 is the release where cove becomes easier to operate as a private runner
platform. The core build/cache surface was already in place. This cut adds the
things operators need around it: run history, image freshness, CI preflight,
network policy, and a clearer migration path for people leaving Cirrus.

