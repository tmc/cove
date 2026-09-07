# Default VM P0 validation

The repaired default cold-boots to the hello screen with the newly built binary:

```
/tmp/cove-atomic-20260906/cove -vm default run
```

No recovery, resume, boot-command, or boot-args overrides were supplied. Screenshot
`/tmp/cove-atomic-20260906/default-cold-boot.png` shows the rendered hello screen.
The log is `default-cold-boot.log` in the same directory.

The display test now calls a local wrapper that checks `initWithConfiguration:error:`,
uses `objc.SendIfResponds`, reports an unavailable selector with
`objc.UnrecognizedSelectorError`, and preserves NSError handling and hot-plug
validation. Other removed wrappers use the same pattern.

The absolute Apple checkout replacement was removed. go.mod pins the published
revision `4c51c1664813`, which includes the separate OCR commit `91f013bb6`.
The sibling checkout is clean. go.sum was updated by Go commands.

`go build ./...` passed. The runtime binary was signed with
`cmd/cove/vz.entitlements`; upstream moved this file from internal/autosign.

`go test ./...` compiled and then trapped in `TestPrivateAPI_NameGetSet` at
private_api_diagnostics_test.go:165, calling `_name` on a stopped VM. This is the
same native failure recorded before P0. The diagnostic file is unchanged from
origin/main, with SHA-256:

```
7f423cd4732a0ee3ab5afa152ec40467ab45e10850b1429a7066b54334d62b8a
```

`go test ./... -skip '^TestPrivateAPI_(NameGetSet|CrashContextMessage)$'` passed.
`go test ./cmd/cove -run '^TestPrivateAPI_CrashContextMessage$' -count=1` also
passed, so only NameGetSet remains unvalidated by a passing run. No tests were
weakened or permanently skipped. Logs are `tests.log`,
`tests-excluding-stopped-diagnostics.log`, and `crash-context.log` in the artifact
directory above. This uses P0's documented exception for a pre-existing unrelated
failure; the unrestricted suite is not green on this host.

The previous local history had 1,853 patch-equivalent commits already on the
remote and no unique patches. A backup branch and stash preserve the previous
state; work was reapplied to current origin/main in the main checkout. Upstream's
newer pointer delivery and protected-consent handling were preserved.
