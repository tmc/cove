# Design 044: Integration Harness Goal

Status: Goal prompt.
Date: 2026-05-25

## Current State

The integration harness is useful but too permissive for runtime storage work.

Strengths:

- `integration_test.go` builds and ad-hoc signs a fresh test binary with the
  virtualization entitlement before running VM tests.
- macOS and Linux integration suites share the same `testVM` helpers and
  readiness checks.
- `cloneTestVM` and direct `CloneVM` calls provide APFS copy-on-write isolation
  for destructive tests.
- `integration_runtime_surface_test.go` already exercises live runtime surfaces
  such as capabilities, disk list, disk resize, USB list, and shared folder
  pause/resume behavior.

Gaps:

- `disk-resize-live` accepts host backing-image growth as success. It does not
  prove that the guest sees the larger physical disk, that the APFS container
  grew, or that `/` has more usable space.
- The resize test skips when `disk list` reports a generic storage device. That
  can hide regressions in attachment introspection.
- There is no stopped-VM end-to-end test for `cove disk resize`.
- Guest storage assertions are not captured as structured artifacts, so failures
  like APFS `-69519` require manual reconstruction from terminal output.
- Skip policy is mixed: missing prerequisites and feature regressions can both
  look like skipped tests.

## Goal Prompt

Use this as the implementation goal:

```text
/goal Improve cove's integration harness so runtime features are accepted by
observable host and guest state, not by proxy checks.

Context:
- Repo: /Volumes/tmc/go/src/github.com/tmc/vz-macos.
- Current harness has signed integration binary helpers, VM readiness checks,
  APFS linked-clone isolation, and runtime-surface subtests.
- A real disk resize exposed a missed failure mode: the host disk image grew,
  but macOS APFS expansion failed because Recovery was after the root APFS
  container and the new free space was after Recovery.

Deliverables:
1. Add a small integration scenario framework for feature tests. Each scenario
   must declare preflight, setup, action, host assertions, guest assertions,
   cleanup, and artifact capture. Keep it in the existing integration test
   package unless a clearer internal package boundary already exists.
2. Add explicit harness tiers:
   - quick: normal unit tests, no VM.
   - runtime-smoke: starts or attaches to one signed-binary VM and checks
     control/agent surfaces.
   - destructive-clone: runs only against APFS-linked clones and never mutates
     the named base VM.
   - guest-state: requires guest agent/root daemon and asserts guest-visible
     OS state after runtime mutations.
3. Strengthen disk resize coverage:
   - Disk list must report disk 0 as a disk image with path and file size for a
     normal cove-managed VM. Do not skip this after preflight succeeds.
   - Test stopped `cove disk resize <vm> <size>` against an isolated clone.
   - Test live `ctl disk resize 0 <size>` against an isolated clone.
   - Assert host file size, guest physical disk size, APFS container size, and
     `df -k /` before and after when the guest is macOS.
   - Add a deterministic fixture or live preflight for the layout where
     Recovery follows the root APFS container. The accepted behavior is either
     successful native handling or a clear, early error that says Recovery
     blocks APFS expansion; ambiguous success is a failure.
   - Keep Linux and Windows behavior separate from macOS APFS expansion.
4. Add artifact capture for failing scenarios:
   - control request/response JSON.
   - VM owner binary path and signature status.
   - host disk image path and sizes.
   - guest `diskutil list`, `diskutil apfs list`, `diskutil info /`,
     `df -k /`, and raw stderr/stdout for resize commands.
   - VM run log path.
   Use a temp artifact directory by default and a flag/env override for durable
   captures.
5. Tighten skip/fail policy:
   - Skip only for missing external prerequisites such as no base VM, no IPSW,
     no guest agent, insufficient free host disk, or unsupported host OS.
   - Once preflight says a scenario is applicable, regressions must fail.
   - Destructive tests must prove they are operating on a clone before mutating
     disk, USB, snapshot, shared-folder, or VM config state.
6. Document exact commands:
   - quick unit gate.
   - runtime smoke gate.
   - disk resize acceptance gate.
   - full macOS and Linux integration gates.
   Include required env vars and whether the base VM must be running or stopped.

Acceptance:
- `go test ./...` passes.
- A focused non-VM test covers scenario planning/preflight decisions.
- The focused disk resize integration command either passes against a suitable
  macOS clone or fails with a saved artifact directory that identifies the exact
  missing external prerequisite or unsupported APFS layout.
- The old host-only resize success path is gone; disk resize acceptance requires
  guest-visible state or a documented unsupported-layout failure.
- Documentation gives an operator enough commands to reproduce a resize
  acceptance run without reading the test code.
```

## First Implementation Slice

Start with the disk resize scenario. It is small enough to validate the harness
shape and important enough to prevent the known APFS regression from recurring.

Recommended first slice:

1. Add `-integration.artifacts` and an artifact helper that creates one
   directory per scenario and logs the path on failure.
2. Add helpers for guest command capture through the agent/root daemon.
3. Replace `disk-resize-live` with a scenario that records host and guest
   storage state before and after resize.
4. Add stopped-VM clone resize coverage for `cove disk resize`.
5. Update `docs/reference/cli.md` or a new runbook with the focused command.

## Focused Commands

Normal source gate:

```sh
go test ./...
```

Runtime disk resize acceptance against a macOS base VM:

```sh
VZ_TEST_VM=cove-test \
go test -tags integration -run 'TestIntegration/runtime-surface/disk-resize' . \
  -integration.headless \
  -integration.artifacts "$TMPDIR/cove-integration-artifacts"
```

The named macOS base VM should be stopped at clone time for destructive clone
storage tests. The harness may start the isolated clone and must not mutate the
named base VM.

Full macOS integration gate:

```sh
VZ_TEST_VM=cove-test \
go test -tags integration -run TestIntegration . -integration.headless
```

Full Linux integration gate:

```sh
VZ_TEST_LINUX_VM=vz-linux-test \
go test -tags integration -run TestLinuxIntegration . -integration.headless
```
