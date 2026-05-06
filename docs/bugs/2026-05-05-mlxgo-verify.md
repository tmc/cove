# mlxgo fresh VM verification

Date: 2026-05-05

## Code-level checks

The stopped-disk apply path now records every staged file whose manifest owner
is `root:wheel` and passes all of those paths to the elevated child for
post-copy verification. A successful apply can no longer be reported if any
LaunchDaemon, provisioning script, `kcpassword`, or guest-agent file remains
owned by the host user.

Regression coverage:

```bash
go test ./...
```

The non-root test suite checks the manifest-to-verification target list. The
root-only e2e test in `provision_inject_e2e_test.go` creates two ownership
fixtures and verifies that a later bad target fails even when the first target
is already `root:wheel`.

## Local build gate

```bash
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
go test ./...
```

These gates passed on 2026-05-05.

## Stuck VM recovery

The failing VM was not booted for live verification in this pass. The recovery
path is stopped-disk only:

```bash
cove -vm mlxgo-fresh-nodev-20260505 ctl stop
cove -vm mlxgo-fresh-nodev-20260505 doctor --fix
cove -vm mlxgo-fresh-nodev-20260505 doctor
cove -vm mlxgo-fresh-nodev-20260505 run -headless -memory 8 -cpu 4
```

Expected stopped-disk result before reboot:

```text
Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist: root:wheel
private/var/db/vz-provision.sh: root:wheel
usr/local/bin/vz-agent: root:wheel
Library/LaunchDaemons/com.github.tmc.vz-macos.vz-agent.plist: root:wheel
```

On the next boot, the provisioning LaunchDaemon should run, create `mlxqa`,
write `/var/log/vz-provision.log`, and write
`/private/var/db/.vz-provisioned`.
