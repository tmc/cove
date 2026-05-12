# NixOS Quickstart

Install a NixOS guest:

```
cove install -nixos -vm nixos-dev
```

Cove downloads the NixOS 25.11 aarch64 minimal ISO into the shared VM cache,
creates a Linux disk, renders a small `configuration.nix`, and attaches a seed
ISO named `COVE-NIXOS` with:

- `configuration.nix`
- `install-nixos.sh`

The first slice boots the NixOS live ISO through EFI and makes the declarative
install bundle available to the live environment. From the live installer,
mount the `COVE-NIXOS` volume and run the installer:

```
sudo mkdir -p /mnt/cove-seed
sudo mount /dev/disk/by-label/COVE-NIXOS /mnt/cove-seed
sudo bash /mnt/cove-seed/install-nixos.sh /dev/vda
```

The script partitions the target disk, writes
`/mnt/etc/nixos/configuration.nix`, and runs:

```
nixos-install --root /mnt --no-root-passwd
```

After installation finishes, stop the installer VM and boot the installed
guest:

```
cove run -linux -distro nixos -vm nixos-dev
```

Run the base recipe after the guest agent is reachable:

```
cove vzscript run -os linux nixos-base
```

`nixos-base` installs `git`, `vim`, `htop`, `curl`, and `jq` through
`nixos-rebuild switch` when `/etc/nixos/configuration.nix` is writable, or
falls back to `nix-env`.

Live boot validation requires an Apple Silicon host and a NixOS installer run.
That validation is separate from this source slice.

## Nix flake packaging

`flake.nix` is present but still uses `lib.fakeHash` for the Go module vendor
hashes. On a host with Nix installed, update them by running:

```
nix build .#cove
nix build .#vz-agent
```

Each failed build prints the `vendorHash` Nix expects. Replace the matching
`lib.fakeHash` value with that hash, then rerun the build.
