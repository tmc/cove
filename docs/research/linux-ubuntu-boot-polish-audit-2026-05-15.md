---
title: Linux and Ubuntu Boot Polish Audit
status: Draft
date: 2026-05-15
---

# Linux and Ubuntu Boot Polish Audit

This audit compares the Linux/Ubuntu boot and provisioning path with the more
polished macOS provisioning path. It uses the source tree plus a live
`ubuntu-gui-kvm` run started with:

```bash
./cove -vm ubuntu-gui-kvm -display 1280x720 up -linux -desktop -nested \
  -cpu 4 -memory 8 -user ubuntu -password ubuntu \
  -vzscripts kvm-test -no-shutdown
```

Host support for this run was present: macOS 26.5 on Apple M4 Max. The VM was
created and entered the Ubuntu Desktop autoinstall path using the cached Desktop
ISO.

The install completed and the normal boot verified:

- nested virtualization was enabled on the post-install boot;
- GUI was headed and window capture was ready;
- `vz-agent.service` started and accepted daemon execs;
- `/dev/kvm` existed as `root:kvm`;
- `loginctl` showed an active `ubuntu` session on `seat0`;
- `kvm-test` completed after installing QEMU/libvirt packages.

## What Works

- `cove up -linux -desktop -nested` maps to a real install-then-boot pipeline.
- Ubuntu Desktop defaults are bumped to 4 CPUs, 8 GB RAM, and at least 40 GB
  disk.
- The installer uses direct kernel boot when it can extract the ISO kernel and
  initrd, avoiding the Subiquity "Continue with autoinstall?" prompt.
- The generated autoinstall data is comprehensive: explicit storage layout,
  removable EFI boot path, NetworkManager netplan, SSH enablement, daemon agent
  systemd unit, OEM desktop user creation, GNOME Initial Setup suppression, GDM
  autologin, and keyring prompt cleanup.
- Nested virtualization is deliberately disabled during install and enabled on
  the first normal boot. That matches the observed code comment about installer
  stability with richer CPU feature exposure.

## Polish Gaps

### Installer Progress Is Much Less Legible Than macOS

The macOS GUI install path has a progress window, explicit lifecycle phases,
and Setup Assistant automation logs. The Linux path starts the VM and then can
go quiet for long periods while Subiquity runs. During the live run, the user
visible output stopped after:

```text
Starting virtual machine...
VM started successfully
```

For Ubuntu Desktop, that quiet period can be many minutes. There is no
operator-facing progress model comparable to macOS download, prepare, install,
first boot, provisioning, and verification phases.

Suggested fix: add a Linux install watcher that reports coarse phases from the
installer serial/console or disk artifacts, even if the exact Subiquity progress
is unavailable.

### Expected Installer-Agent Absence Looks Like a Failure

During the live install, cove printed:

```text
Note: vz-agent is not running in this VM.
```

That message is true but poorly timed for the install VM: the daemon agent is
not expected to be reachable until after installation and the normal boot. The
message suggests re-provisioning, which is wrong advice during first install.

Suggested fix: when `installVM && linuxMode` is active, suppress this warning or
replace it with "guest agent will be verified after install completes".

### Proxy Setup Runs Too Early

The live run also printed:

```text
warning: restore guest proxy: guest agent not ready before proxy setup: context deadline exceeded
```

Again, this is an expected installer-phase condition presented as a generic
runtime warning. macOS provisioning has more phase-aware fallbacks and status
messages.

Suggested fix: skip proxy restoration during Linux installer boots, or gate the
warning on a normal-boot phase where the agent should already exist.

The same warning reappeared on a later normal boot even though the daemon agent
became healthy shortly afterward:

```text
warning: guest proxy restore failed; manual recovery needed: deadline_exceeded: context deadline exceeded
```

That makes the message too strong. It should say whether proxy restore is a
required boot gate, a retryable background task, or a non-fatal feature restore.

### No Linux GUI Login Watchdog

macOS has OCR/screen driven Setup Assistant automation and a login-screen
watchdog that can type cached credentials if autologin does not land on the
desktop. Ubuntu Desktop relies on generated GDM/autologin files and GNOME
Initial Setup suppression. If GDM, AccountsService, or keyring behavior changes,
there is no Linux equivalent that detects "login screen instead of desktop" and
recovers with the cached password.

Suggested fix: reuse the existing screen classification and input stack for a
Linux GNOME login watchdog. It should run only for headed Ubuntu Desktop boots
with known credentials and should record screenshots when it intervenes.

### First-Boot Success Is Not a Strong Gate

The Linux path writes an installed marker after `verifyLinuxInstallBootable`,
then `up` proceeds to normal boot and optional vzscripts. The bootability gate is
useful, but it is not the same as "desktop reachable, autologin complete,
daemon agent reachable, KVM verified". macOS provisioning more often treats the
post-install desktop and user state as part of the operator contract.

Suggested fix: define a richer Ubuntu Desktop readiness gate:

- installed marker present;
- daemon agent reachable;
- provisioned user exists;
- headed GUI reaches GNOME desktop or a recoverable login screen;
- `kvm-test` passes when `-nested` was requested.

### Generated Provisioning Is Powerful But Opaque

The Ubuntu autoinstall late-command block now does a lot: bootloader repair,
kernel/initrd staging, cloud-init disabling, agent install, user mutation,
AccountsService edits, GDM config, and keyring deletion. This is effective but
hard to audit from CLI output. macOS provisioning has separate stage/apply
concepts and named recovery docs; Linux puts most of the operator-critical work
inside generated YAML.

Suggested fix: write a small install summary into the VM directory after seed
generation, for example `linux-provisioning-plan.txt`, listing the selected
variant, installer mode, user, autologin setting, agent inclusion, nested
install/runtime split, and expected verification commands.

### `kvm-test` Was Too Strict About Kernel Modules

The first `kvm-test` run failed even though `/dev/kvm` existed:

```text
error: kvm-test:6: guest-exec sh -lc 'lsmod | grep -E "kvm|kvm_arm"': exit code 1
```

On this Ubuntu boot, `/dev/kvm` is the stronger user-facing signal. The recipe
now accepts either a visible KVM module in `lsmod` or the KVM device node.

### Long-Running Install Has No Handoff Artifact

The live run has useful files in the VM directory (`cloud-init-data`,
`config.json`, `runtime.json`, `control.token`, `vmlinuz`, `initrd`), but there
is no single "what is happening now" artifact. If the install hangs, a later
agent has to infer state from process output and VM files.

Suggested fix: maintain `install-status.json` with phase, timestamps, ISO path,
boot mode, seed path, expected next phase, and last warning. This would also
make background/daemon UI easier later.

## Priority

1. Make installer-phase warnings phase-aware, especially agent and proxy
   warnings.
2. Add an Ubuntu Desktop post-boot readiness gate that distinguishes installed,
   agent-ready, desktop-ready, and KVM-ready.
3. Add a Linux GNOME login watchdog for headed desktop boots.
4. Add coarse Linux install progress/status artifacts.
5. Emit a generated provisioning summary next to the cloud-init data.

The current implementation is capable, but the user experience is less honest
than the macOS path when something takes a long time or lands in an intermediate
state. The main gap is not feature coverage; it is phase-aware reporting and
recoverability.

## Live Verification

Commands run on 2026-05-15:

```bash
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
go test ./...
./cove -vm ubuntu-gui-kvm -display 1280x720 up -linux -desktop -nested \
  -cpu 4 -memory 8 -user ubuntu -password ubuntu \
  -vzscripts kvm-test -no-shutdown
./cove -vm ubuntu-gui-kvm ctl gui status
./cove -vm ubuntu-gui-kvm ctl agent-exec --daemon \
  sh -lc 'whoami; test -e /dev/kvm && echo KVM_DEVICE=yes; ls -l /dev/kvm; loginctl list-sessions --no-legend || true'
./cove -vm ubuntu-gui-kvm vzscript run kvm-test
```

The final VM was stopped after verification so no foreground cove process was
left attached to this session.
