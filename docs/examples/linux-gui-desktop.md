# Linux GUI Desktop

This example installs Ubuntu Desktop with the Desktop ISO/OEM path, boots the
installed VM, and opens the GUI.

```sh
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove

./cove -vm linux-gui-debug -display 1280x720 up -linux -desktop -user debug -password debug -gui
```

For the recommended GUI KVM machine on supported M3/M4 hosts, enable nested
virtualization on the post-install boot, run the KVM verification recipe, and
leave the VM running:

```sh
./cove -vm ubuntu-gui-kvm -display 1280x720 up -linux -desktop -nested \
  -cpu 4 -memory 8 -user ubuntu -password ubuntu \
  -vzscripts kvm-test -no-shutdown
```

The `-desktop` path defaults to the OEM installer. Use
`-desktop-installer=server` to force the older Server ISO plus
`ubuntu-desktop` package path.

The install phase keeps nested virtualization disabled for installer stability.
The first normal boot enables it, and `kvm-test` verifies `/dev/kvm`, KVM module
visibility, and nested QEMU/libvirt package installation.

On the 2026-05-03 validation run, with disk image attachments using
`Cached + None` during install:

- Ubuntu Server install powered off in about 6m43s and reached a booted,
  agent-connected system in about 7m44s.
- Ubuntu Desktop OEM reached the installed desktop in about 14m32s.
- Host `iostat` showed install-write bursts above 300 MB/s, so the old
  17 MB/s sync bottleneck was no longer the dominant limit.

The validated desktop screenshot was captured at:

```sh
/tmp/linux-gui-debug-SUCCESS.png
```

The root daemon agent should be reachable after first boot:

```sh
./cove -vm linux-gui-debug ctl agent-exec --daemon whoami
```

Expected output:

```text
root
```

If the GUI opens on GNOME Initial Setup instead of the provisioned user's
desktop, rebuild from a commit that includes the OEM user late-command fix and
reinstall the VM.
