# Authorization hang verification

Date: 2026-05-05

## Commands

```bash
go test ./...
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

Result: passed.

## Coverage

`authorization_test.go` covers the three cove-level authorization blockers:

- `AuthorizationCreate` no-prompt watchdog;
- `AuthorizationCreate` prompt-visible watchdog;
- `AuthorizationExecuteWithPrivileges` no-prompt watchdog.

It also verifies that `runElevatedManifestNative` refuses to run from the
registered UI thread. That keeps the headed install event loop from being the
thread that waits on native authorization.

`blocks_test.go` covers the stop warning filter:

- suppresses only `VZErrorDomain` code 4 with the stopped-to-stopping state
  transition text;
- rejects other domains, other VZ codes, and other transitions.

## Live headed install

Not run in this pass. A full:

```bash
cove up -vm mlxgo-fresh-headed2-r2 -user mlxqa -password mlxqa123 -gui -memory 8 -cpu 4 -no-shutdown
```

requires a real GUI authorization approval and a disk-heavy macOS install. QA
should run it from a normal terminal with the installed `~/go/bin/cove`.

Expected behavior:

1. After install, the already-stopped VZ stop race does not print the cosmetic
   warning.
2. During provisioning, the admin authorization prompt appears promptly.
3. If authd or SecurityAgent wedges before a prompt appears, cove returns a
   clear `AuthorizationCreate wedged after 1m30s` or
   `AuthorizationExecuteWithPrivileges wedged after 1m30s` error instead of
   hanging indefinitely.
4. If the prompt is visible but not approved, cove continues waiting up to the
   existing 15 minute prompt budget.

## Stuck VM recovery

For `mlxgo-fresh-headed2-20260505`, kill the stuck `cove up` process, then
retry with the updated binary from a normal terminal:

```bash
pkill -f 'cove up -vm mlxgo-fresh-headed2-20260505'
~/go/bin/cove -vm mlxgo-fresh-headed2-20260505 disk-detach
~/go/bin/cove up -vm mlxgo-fresh-headed2-20260505 -user mlxqa -password mlxqa123 -gui -memory 8 -cpu 4 -no-shutdown -force
```

If the old disk is worth preserving first, snapshot or copy the VM directory
before using `-force`.
