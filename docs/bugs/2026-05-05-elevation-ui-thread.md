# Elevation from the UI thread

Date: 2026-05-05

## Report

Live QA confirmed the previous authorization hang fix: cove no longer waits
forever inside Security.framework. The new failure mode is that headed
provisioning calls native elevation from the registered AppKit UI thread, so
the defensive guard returns:

```text
native authorization cannot run on the app ui thread; run provisioning from a worker goroutine
```

The guard is correct. The caller must move the elevation work off the UI
thread so the native macOS admin dialog can appear and provisioning can
complete without `sudo`.

## Elevation call sites

All privileged file writes route through the typed elevation path:

```text
runElevated -> runElevatedManifestNative -> cove __elevated-op
```

Callers:

```text
provision.go          applyStagedFiles: user creation, auto-login, guest tools
provision_mount.go    fixOwnershipWithSudoForVM: stopped-disk ownership repair
agent_inject.go       injectAgentOnly: guest agent daemon/user-agent install
provision_verify.go   doctor --fix: guest agent repair
```

`PreWarm` also calls Security.framework through `createAuthorization` before
the Data volume is mounted.

The macOS CLI process registers its main goroutine as the UI thread when it
drives AppKit. Headed install/provision flows can therefore reach `PreWarm` or
`runElevated` from that UI thread even though the actual Security.framework
calls must not run there.

## Fix

Keep a single elevation surface, but make it thread-agnostic for callers:

- `PreWarm` dispatches its `AuthorizationCreate` work to a worker when invoked
  from the registered UI thread.
- `runElevated` dispatches the whole native authorization and privileged
  manifest execution path to a worker when invoked from the registered UI
  thread.
- `runElevatedManifestNative` keeps its UI-thread refusal. It remains the
  safety net for accidental direct calls.

Normal interactive `cove provision`, `cove inject`, and `cove up` should now
show the native macOS admin dialog instead of requiring `sudo`.

Restricted environments still cannot show that dialog. In that case cove keeps
the manual elevation fallback, but the normal-path warning no longer tells
users to start with `sudo`.
