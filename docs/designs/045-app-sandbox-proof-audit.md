# Cove App Sandbox Proof Audit

Date: 2026-05-28

This audit closes the non-mutating App Sandbox proof lane from
`docs/designs/045-app-sandbox.md`. The proof is not a claim that every cove
command is safe inside Apple's App Sandbox. It records that the current
architecture has enough working primitives to stop adding read-only surface and
move to an explicit product architecture decision.

## Decision

Cove should proceed with Architecture C for CLI-oriented App Sandbox work:

> The normal CLI remains the coordinator that resolves user intent, staged
> bookmarks, and short control paths, then launches a sandboxed run-worker child
> for sandbox-sensitive command execution.

Architecture B, where a sandboxed app is only a GUI/launcher and the
unsandboxed CLI remains the main product, remains a packaging fallback. It is
not the roadmap target for the current proof lane because it would avoid the
hard command-boundary questions instead of proving them.

Mutating VM lifecycle work remains outside this completed lane. `run`,
`install`, `up`, provisioning, helper installation, disk resizing, and
shared-folder mutation need a separate security design before they can move
from fail-closed denial to sandboxed execution.

## Evidence

The full App Sandbox smoke sweep passed:

```bash
COVE_APP_SANDBOX_MACGO_SMOKE=1 go test -count=1 -run 'TestAppSandbox' -v .
```

Result:

```text
ok  	github.com/tmc/cove	120.090s
```

The sweep included:

- `TestAppSandboxEntitlementFixture`
- `TestAppSandboxMacgoBundleSmoke`
- `TestAppSandboxMacgoBundleDoctorSmoke`
- `TestAppSandboxMacgoBundleServeSmoke`
- `TestAppSandboxMacgoBundleStateSmoke`
- `TestAppSandboxMacgoBundleStateDirGrantSmoke`
- `TestAppSandboxMacgoBundleHostPathDenialSmoke`
- `TestAppSandboxDirectoryGrantBoundarySmoke`
- `TestAppSandboxInstallMediaGrantBoundarySmoke`
- `TestAppSandboxRunWorkerSmoke`
- `TestAppSandboxRunWorkerStatusPreflightSmoke`
- `TestAppSandboxStatusWorkerDelegationSmoke`
- `TestAppSandboxRunWorkerListPreflightSmoke`
- `TestAppSandboxListWorkerDelegationSmoke`
- `TestAppSandboxRunWorkerImageListPreflightSmoke`
- `TestAppSandboxImageListWorkerDelegationSmoke`
- `TestAppSandboxRunWorkerImageInspectPreflightSmoke`
- `TestAppSandboxImageInspectWorkerDelegationSmoke`
- `TestAppSandboxBookmarkProbeSmoke`
- `TestAppSandboxDurableBookmarkStorageSmoke`
- `TestAppSandboxMacgoBundleBookmarkConsumeSmoke`
- `TestAppSandboxMacgoBundleSocketAndSubprocessSmoke`

The sweep skipped only separately gated smokes:

- `TestAppSandboxSmoke` and `TestAppSandboxDoctorSmoke`, which require
  `COVE_APP_SANDBOX_SMOKE=1`.
- `TestAppSandboxMacgoBundleScratchBootSmoke`, which requires
  `COVE_APP_SANDBOX_MACGO_BOOT_SMOKE=1`.
- `TestAppSandboxPowerboxPromptSmoke`, which requires
  `COVE_TEST_POWERBOX_UI=1`.

## Covered Primitives

Startup and detection:

- The sandboxed macgo bundle starts without `Trace/BPT trap`.
- `security status` reports `apple app sandbox: true` and the app container
  home under `~/Library/Containers/com.tmc.cove/Data`.
- `doctor host` handles sandbox restrictions by warning on subprocess checks
  that are not meaningful inside the sandbox.

State mapping and fail-closed access:

- `COVE_STATE_DIR` maps state into an explicit root and `list` can read VMs
  from that root.
- Ambient VM root access fails closed with a bookmark/Powerbox grant-required
  error instead of falling through to host paths.
- Mutating host access paths fail closed under App Sandbox for disk resize,
  shared-folder mutation, provisioning, helper installation, and install/up
  mutation boundaries.

Bookmarks and Powerbox substrate:

- App-scoped security-scoped bookmarks can be created, stored, resolved,
  started, read through, and stopped.
- Missing grants return typed grant-required errors naming the required key.
- Media and directory grants can be staged and consumed without lifting the
  broader mutation denial boundary.

Worker handoff:

- The unsandboxed parent can launch a sandboxed worker child and pass a typed
  JSON handoff over a short Unix socket.
- Descriptor handoff over `SCM_RIGHTS` is proven by `__run-worker probe`.
- Single-bookmark VM metadata is proven by `status-preflight`.
- VM-root directory metadata is proven by `list-preflight` and
  `COVE_APP_SANDBOX_DELEGATE_LIST=1`.
- Image-root metadata is proven by `image-list-preflight` and
  `COVE_APP_SANDBOX_DELEGATE_IMAGE_LIST=1`.
- Multi-bookmark metadata is proven by `image-inspect-preflight` and
  `COVE_APP_SANDBOX_DELEGATE_IMAGE_INSPECT=1`, resolving both image-root and
  VM-root grants in one sandboxed child.

Entitlements:

`internal/autosign/app_sandbox.entitlements` contains only:

- `com.apple.security.app-sandbox`
- `com.apple.security.files.bookmarks.app-scope`
- `com.apple.security.files.user-selected.read-write`
- `com.apple.security.network.server`
- `com.apple.security.network.client`
- `com.apple.security.virtualization`

No broad Downloads, Documents, Desktop, absolute-path, automation, or temporary
exception file entitlements are present.

## Stop Conditions

This proof lane is complete when:

- Full `COVE_APP_SANDBOX_MACGO_SMOKE=1` App Sandbox smokes pass.
- Non-mutating delegated commands prove status, list, image list, and image
  inspect.
- Missing grants fail closed.
- Mutating surfaces fail closed.
- The project records Architecture C as the next design target.

All five conditions are satisfied by this audit.

## Remaining Work

Do not add more read-only delegation only to increase surface area. New App
Sandbox work should start from one of these separate design questions:

- How should mutating VM lifecycle commands acquire and persist grants?
- Which process owns Virtualization.framework VM execution in a fully
  sandboxed product?
- Which sandboxed flows require real UI Powerbox prompts instead of pre-staged
  bookmark store entries?
- Whether scratch-boot proof should become a required release gate rather than
  a separately gated smoke.
