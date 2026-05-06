# Helper detach routing verification

Date: 2026-05-05

## Commands

```bash
go test ./...
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

Result: passed.

## Coverage

`helper_test.go` covers the new helper `force_detach` safety checks:

- accepts a detach request only when the requested `/dev/diskN` matches the
  device currently attached for the requested `disk.img`;
- rejects device mismatches;
- falls back from `hdiutil detach -force` to `diskutil unmountDisk force`.

`provision_mount_test.go` covers the user-facing behavior:

- when normal detach fails and the helper is installed, cove calls the helper
  and does not print a manual `hdiutil detach` command;
- when the helper is not installed, cove keeps the manual fallback.

## Live QA

Not rerun in this API session. QA pane
`212F363C-4E95-4696-A2A9-0FF564E09F79` owns the live retry on the stuck
`mlxgo-fresh-headed2-20260505` VM:

```bash
~/go/bin/cove -vm mlxgo-fresh-headed2-20260505 provision -apply
```

Expected behavior: if normal auto-detach reports a busy disk, cove routes the
force detach through `cove-helper`. With the helper installed and reachable, it
must not print `hdiutil detach` or `sudo` recovery commands.
