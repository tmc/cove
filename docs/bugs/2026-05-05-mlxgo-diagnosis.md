# mlxgo fresh VM provisioning failure

Date: 2026-05-05

## VM

The failing VM was created with:

```bash
cove up -vm mlxgo-fresh-nodev-20260505 -user mlxqa -password mlxqa123 -headless -memory 8 -cpu 4 -no-shutdown
```

The run reported success, but the VM stopped at the login screen. The
`mlxqa` / `mlxqa123` login did not work, and the guest agent connection reset.

## Evidence

`tools/diagnose-fresh-vm.sh mlxgo-fresh-nodev-20260505` mounted the guest Data
volume read-only with APFS ownership enabled. The LaunchDaemon and provisioning
script were present but owned by the host user:

```text
-rw-r--r-- 501:20 .../Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist
-rwxr-xr-x 501:20 .../private/var/db/vz-provision.sh
-rw------- 501:20 .../private/etc/kcpassword
-rwxr-xr-x 501:20 .../usr/local/bin/vz-agent
-rw-r--r-- 501:20 .../Library/LaunchDaemons/com.github.tmc.vz-macos.vz-agent.plist
```

The first-boot completion artifacts were absent:

```text
missing private/var/log/vz-provision.log
missing private/var/db/.vz-provisioned
```

The auto-login preference existed and still pointed at the intended user:

```text
"autoLoginUser" => "mlxqa"
```

## Diagnosis

This is not a bad password or auto-login-only failure. The user creation daemon
never ran.

macOS launchd requires system LaunchDaemon plists to be owned by `root:wheel`.
The guest Data volume shows the provisioning LaunchDaemon as `501:20`, so
launchd ignores it during boot. With the daemon ignored, `/var/db/vz-provision.sh`
never creates the `mlxqa` account, never writes `/var/log/vz-provision.log`, and
never marks `/private/var/db/.vz-provisioned`.

The same ownership problem also applies to the staged guest-agent daemon, which
explains the reset agent connection.

## Recovery

Stop the VM and run a stopped-disk ownership repair before booting it again:

```bash
cove -vm mlxgo-fresh-nodev-20260505 ctl stop
cove -vm mlxgo-fresh-nodev-20260505 doctor --fix
cove -vm mlxgo-fresh-nodev-20260505 run -headless -memory 8 -cpu 4
```

`doctor --fix` should remount the Data volume with APFS ownership enabled and
repair the launchd-critical files to `root:wheel`. On the next boot, launchd
can load the provisioning daemon and create the `mlxqa` account.
