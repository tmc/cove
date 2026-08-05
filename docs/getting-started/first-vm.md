---
title: "Tutorial: provision and snapshot your first VM"
description: Build a macOS VM from nothing, run a command inside it, snapshot it, and delete it - the four things every other cove workflow is made of.
icon: graduation-cap
---
# Tutorial: provision and snapshot your first VM

This tutorial builds one macOS VM from nothing, runs a command inside the
guest, saves a snapshot, and deletes the VM. It takes about 15 minutes, most
of which is the macOS installer running unattended.

Every other cove workflow — CI runners, agent sandboxes, dev environments — is
assembled from these four moves. Do this once and the rest of the docs read as
variations.

You need an Apple Silicon Mac. The VM cove builds here uses a 64 GB disk image,
but it is sparse — this run consumed about 6 GB of real disk, plus 1.4 GB for
the snapshot in step 5.

If you have not installed cove yet, do [Install cove](install.md) first.

## 1. Check the Mac before you build anything

```sh
cove doctor host
```

```text
Host readiness: warn
  PASS  apple-silicon: running on darwin/arm64
  PASS  macos-version: 26.6
  PASS  virtualization-entitlement: cove binary has virtualization entitlement
  PASS  apple-app-sandbox: not active for this process
  PASS  disk-capacity: 419.2 GB free under /Users/tmc/.vz
  PASS  state-writable: /Users/tmc/.vz writable
  PASS  network: active interface anpi2
  WARN  helper: privileged helper installed but socket is not present; it may be stopped.
  PASS  xcode-cli: /Applications/Xcode-rc.app/Contents/Developer
```

Your version numbers, free space, and interface name will differ.

Fix any `FAIL` before continuing. A `WARN` on `helper` is worth fixing now:

```sh
sudo cove helper install
```

The helper is what lets cove write provisioning files into the guest disk as
root. Without it, provisioning stops and waits for a macOS admin-password
dialog — and in a non-interactive shell (CI, `ssh`, `tmux`) there is no dialog
to approve, so the run hangs. Installing the helper once avoids that.

## 2. Build the VM

```sh
cove up -vm docs-tutorial -user tutorial
```

`cove up` runs three steps in order: install macOS, provision the user, boot.
Omitting `-password` makes cove prompt for the guest password instead of
leaving it in your shell history.

```text
=== Step 1/3: Installing macOS ===
=== macOS Installation ===
Loading restore image...
Configuring virtual machine...
Starting installation...
[==============================] 100.0%
=== Installation Complete ===

Stopping VM...
VM stopped.

Provisioning VM disk...
Staging provisioning for "tutorial"...
Provisioning files applied; guest user will be verified after boot.
Detaching /dev/disk27...

=== Step 2/3: Provisioning (already done) ===

=== Step 3/3: Booting VM ===
Starting virtual machine...
VM started successfully
Control socket: /Users/tmc/.vz/vms/docs-tutorial.covevm/control.sock
```

The installer step is the slow one. The device node in `Detaching /dev/disk27`
varies per run.

If you have already downloaded a restore image, point at it and skip the
download:

```sh
cove up -vm docs-tutorial -user tutorial -ipsw ~/.vz/cache/RestoreImage.ipsw
```

Defaults worth knowing: 2 CPUs, 4 GB memory, 64 GB disk, GUI window on. Add
`-headless` for no window.

## 3. See what cove created

```sh
cove list
```

```text
VMs:
  NAME                 OS     STATE      UPTIME  NOTE                     SIZE     CREATED
  docs-tutorial        macOS  running    8m52s   owner pid=43696 cove up  64.0 GB  2026-08-04
```

A VM is a directory. Everything cove knows about it lives there:

```sh
ls ~/.vz/vms/docs-tutorial/
```

```text
autologin.json  control.sock    hw.model        quotas.json
aux.img         control.token   mac.address     runtime.json
config.json     disk.img        machine.id      snapshots
```

`disk.img` is the guest disk and `aux.img` its auxiliary storage. `hw.model`,
`machine.id`, and `mac.address` are the virtual machine's identity — the guest
notices if they change. `control.sock` and `control.token` are how other
processes drive this VM while it runs.

That path is a symlink: cove stores the VM as `docs-tutorial.covevm` and links
`docs-tutorial` to it, so either name works. Deleting the directory deletes the
VM; there is no other state.

## 4. Run a command inside the guest

```sh
cove shell docs-tutorial -- sh -c 'sw_vers; whoami; uname -m'
```

```text
ProductName:		macOS
ProductVersion:		26.5
BuildVersion:		25F71
root
arm64
```

`cove shell` reaches the guest through the guest agent over vsock, not SSH, so
it works before the guest has networking or an SSH server. Anything after `--`
runs inside the VM.

Write something so the next step has state to capture:

```sh
cove shell docs-tutorial -- sh -c 'echo hello-from-tutorial > /tmp/marker.txt; cat /tmp/marker.txt'
```

```text
hello-from-tutorial
```

## 5. Snapshot the running VM

```sh
cove -vm docs-tutorial snapshot save tutorial-checkpoint
```

```text
snapshot 'tutorial-checkpoint' saved
```

Note where `-vm` sits: **before** the subcommand. `cove snapshot -vm <name>`
puts the flag after the subcommand, where it is not parsed as the VM selector,
and the command acts on your active VM instead. This bites on every subcommand
that takes `-vm`.

```sh
cove -vm docs-tutorial snapshot list
```

```text
NAME                 SIZE    CREATED
tutorial-checkpoint  1.4 GB  2026-08-04 20:18
```

The snapshot is VM state — memory and CPU — written next to the disk:

```sh
ls -lh ~/.vz/vms/docs-tutorial/snapshots/
```

```text
-rw-r--r--  1 tmc  staff   224B Aug  4 20:18 tutorial-checkpoint.json
-rw-------  1 tmc  staff   1.4G Aug  4 20:18 tutorial-checkpoint.vmstate
```

Restore it later with `cove -vm docs-tutorial snapshot restore tutorial-checkpoint`.
Restoring returns the guest to this exact moment, `/tmp/marker.txt` included.
That is the primitive behind disposable CI runners and agent sandboxes: build
once, snapshot, and roll back instead of reinstalling.

## 6. Delete the VM

Stop it first. cove refuses to delete a running VM, and tells you how to
proceed:

```sh
cove rm docs-tutorial
```

```text
error: cannot delete VM "docs-tutorial": it is currently running
  request stop: cove ctl -vm docs-tutorial request-stop
  check status: cove list
  if still running: cove ctl -vm docs-tutorial stop
  then retry: cove vm delete docs-tutorial
```

Ask the guest to shut down cleanly:

```sh
cove ctl -vm docs-tutorial request-stop
```

```text
stop requested (ACPI power button sent)
```

That sends the ACPI power button and returns immediately — the guest still
needs a moment to shut down, so poll `cove list` until it reports `stopped`.
If the guest ignores the request, `cove ctl -vm docs-tutorial stop` halts it.

Once it is stopped, delete it:

```sh
cove rm docs-tutorial
```

```text
Deleting VM 'docs-tutorial'...
VM deleted.
```

This removes the VM directory and its snapshots. Confirm it is gone:

```sh
cove list
```

## What you just used

| Move | Command | Where to go deeper |
| --- | --- | --- |
| Check the host | `cove doctor host` | [Fix a VM that will not start](../guides/troubleshooting.md) |
| Build a VM | `cove up` | [Provision a guest](../guides/provisioning.md) |
| Run guest commands | `cove shell` | [Talk to the guest agent](../features/guest-agent.md) |
| Capture state | `cove snapshot` | [Snapshot and roll back a VM](../features/snapshots.md) |

## Next steps

- Automate the provisioning you just did by hand with
  [vzscripts](../features/vzscript.md).
- Turn this VM into a [macOS CI runner](../examples/ci-runner.md) or an
  [agent sandbox](../agent-sandbox/quickstart.md).
- Understand what cove is doing underneath in
  [How cove works](../architecture/overview.md).
