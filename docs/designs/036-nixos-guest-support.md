# Design 036: NixOS Guest Support

Cove should install NixOS as a first-class Linux guest with the same user
shape as the existing Linux path:

```
cove install -nixos
cove run -linux -distro nixos
```

The NixOS path uses an aarch64 NixOS ISO, boots it through EFI, and installs a
declarative system from a generated `configuration.nix`. The guest remains a
normal Virtualization.framework Linux VM after installation: same disk layout,
same per-VM control sockets, same agent transport, and the same daemon-less
fallback as Ubuntu, Debian, Fedora, and Alpine.

## Goals

- Download and cache the current NixOS aarch64 minimal ISO.
- Install through EFI boot on Apple Silicon.
- Generate a minimal `configuration.nix` from cove's provisioning flags.
- Enable SSH and virtio-vsock friendly modules for the guest agent path.
- Keep existing Linux install behavior unchanged for non-NixOS distros.

## Non-Goals

- No flake-based configuration in slice 1.
- No host-side hot rebuild when a NixOS config changes.
- No secret material in the generated store closure.
- No replacement of per-VM control sockets.

## Architecture

`cove install -nixos` is analogous to `cove install -linux`: it resolves the
VM directory, creates the Linux disk, downloads an installer ISO when needed,
boots the VM with EFI, and waits for the installed disk to become bootable.

The key difference is provisioning. Ubuntu uses cloud-init NoCloud data.
NixOS does not consume NoCloud as its native install contract. Cove instead
renders a `configuration.nix` and makes it available to the live ISO before
`nixos-install` runs:

1. Build a NixOS provision bundle containing `configuration.nix` and an
   `install-nixos.sh` script.
2. Attach that bundle as the NixOS equivalent of a cidata disk.
3. Boot the NixOS ISO through EFI.
4. Partition and mount the target disk at `/mnt`.
5. Write the generated configuration to `/mnt/etc/nixos/configuration.nix`.
6. Run `nixos-install --root /mnt --no-root-passwd`.

This keeps the NixOS install declarative. Cove does not mutate a finished
system through ad hoc shell commands during initial provisioning; the shell
script only prepares the target root and invokes `nixos-install` with the
generated configuration.

## Slice 1

Slice 1 ships the host-side shape:

- `internal/nixos` owns ISO metadata, cache naming, downloads, generated
  configuration text, and validation helpers.
- The install command accepts `-nixos`, which implies Linux mode and routes
  to the NixOS install path.
- The install path writes a seed bundle with `configuration.nix` and
  `install-nixos.sh` beside the VM and attaches it for the live installer.
- Tests cover template rendering, key-field validation, URL shape, gated URL
  reachability, flag parsing, and NixOS variant routing.

The first live-boot validation remains separate because it requires booting an
aarch64 NixOS ISO under Virtualization.framework on Apple Silicon.

## Configuration

The generated configuration is intentionally small:

- EFI boot loader configured for Virtualization.framework.
- DHCP networking and OpenSSH enabled.
- Root user enabled with the provisioning password.
- Provisioned user added to `wheel` and `networkmanager`.
- Passwordless sudo for the provisioned user path.
- Virtio and vsock kernel modules available for the agent path.

The generated text is stable and inspectable. Users who want more can boot
the installed VM and switch to their own `/etc/nixos/configuration.nix`.

## Relationship to Existing Control Sockets

The NixOS guest support does not replace per-VM control sockets. It follows
the current parent/coordinator pattern:

- `cove run` owns the VM process.
- The per-VM socket remains the control plane for that VM.
- A future daemon may coordinate lifecycle policy across many VMs, but it will
  speak to existing VM owners rather than bypassing them.

This keeps daemon-less operation intact. If no daemon is running, current
commands still install and run VMs directly.

## Deferred Work

Slice 2 and later can add:

- Host-side detection of configuration drift and `nixos-rebuild switch`.
- Flake input support and lock-file aware rebuilds.
- Secret injection through tmpfs or another non-store path.
- A fully unattended live-ISO automation script once the Apple Silicon boot
  path is validated on hardware.

## Cross-references

- [`docs/quickstart/nixos.md`](../quickstart/nixos.md) for the operator-facing
  install and boot flow.
- [`docs/designs/037-linux-autoprov.md`](037-linux-autoprov.md) for the Linux
  Desktop provisioning work that shares the same guest-install boundary.
- [`docs/designs/033-cove-daemon.md`](033-cove-daemon.md) for the later
  daemon-coordination boundary that should not replace the per-VM socket.
