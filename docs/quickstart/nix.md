# Nix Quickstart

This page is about installing the Nix package manager in a macOS guest. For a
NixOS Linux guest, see [NixOS Quickstart](nixos.md).

Install Nix in the selected macOS VM:

```
cove vzscript run nix
```

The `nix` recipe uses the official multi-user Nix installer for macOS:

```
sh <(curl --proto '=https' --tlsv1.2 -L https://nixos.org/nix/install) --daemon
```

The recipe runs the installer as root through the daemon agent, then verifies
the install as an admin user by sourcing:

```
/nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh
```

It also runs `nix run nixpkgs#hello` with `nix-command` and `flakes` enabled
as a smoke test.

This is separate from `cove install -nixos`, which creates a Linux VM running
the NixOS operating system.
