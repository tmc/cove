# Elevation without sudo verification

Date: 2026-05-05

## Commands

```bash
go test ./...
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

Result: passed.

## Coverage

`authorization_test.go` verifies that `PreWarm` dispatches
`AuthorizationCreate` off the registered UI thread. The previous direct-call
guard remains in place: `runElevatedManifestNative` still refuses to run on
the UI thread.

`runElevated` now dispatches the whole elevated manifest path to a worker when
called from the UI thread. This covers all typed elevation callers:

- `cove provision` / `cove inject` apply;
- `cove provision-agent`;
- `cove doctor --fix`;
- `cove up` after install.

`provision_test.go` verifies that normal non-root provisioning no longer prints
the stale sudo warning when native authorization is available. Restricted
contexts still print a manual-elevation warning.

`up_path_resolution_test.go` verifies that non-root macOS `cove up` can proceed
to the native admin dialog in normal contexts and only fails early when cove is
running in a context that cannot show that dialog.

## Live headed install

Not run in this pass. A full fresh macOS install and first boot requires GUI
admin approval and a disk-heavy VM flow. QA pane `212F363C` is the live
verification owner for:

```bash
~/go/bin/cove -vm mlxgo-fresh-headed2-20260505 provision -apply
~/go/bin/cove up -force -vm mlxgo-fresh-headed3-20260505 -user mlxqa -password mlxqa123 -gui -memory 8 -cpu 4 -no-shutdown
```

Expected behavior: no `sudo` is required. Cove should show the native macOS
admin dialog, apply provisioning after approval, and continue boot verification.

## Notes

There is no `CLAUDE.md` file in this worktree to update. The stale public docs
that described provisioning as sudo-required were updated to describe the
native macOS admin dialog instead.
