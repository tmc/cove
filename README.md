# vz-macos

macOS and Linux VM management using Apple's Virtualization.framework via [purego](https://github.com/ebitengine/purego) (cgo-free).

## Features

- Install macOS from IPSW restore images
- Install Linux with cloud-init automated setup
- Non-interactive provisioning via disk injection
- Control socket for programmatic VM management
- Screenshot capture and screen state detection
- Guest agent over vsock (gRPC)
- vzscript engine for automated VM configuration
- Clipboard sharing, shared folders, GUI toolbars

## Quick Start

```bash
# Build
go build -o vz-macos .

# One-command setup: install + provision + boot
./vz-macos up -user testuser

# Or step-by-step:
./vz-macos install                                         # auto-downloads IPSW
./vz-macos inject -user testuser -skip-setup-assistant     # provisions user
./vz-macos run                                             # boots with GUI
```

On first launch, `vz-macos` checks whether the binary already has the
Virtualization.framework entitlements it needs. If not, it ad-hoc signs the
binary with the embedded entitlements and re-execs automatically. No manual
`codesign --entitlements ...` step is required.

## Linux VMs

```bash
# Install Ubuntu Server (auto-downloads ISO)
./vz-macos install -linux -provision-user ubuntu -provision-password secret

# Run
./vz-macos run -linux -gui
```

By default Linux install/provisioning includes `vz-agent`. Use `-no-agent` only
to skip agent installation during Linux install/provisioning. It does not
change the capabilities of an already-created VM.

## Guest Proxy

`-proxy` configures guest system `http` and `https` proxy settings after boot.
The guest agent must be available over vsock.

```bash
# macOS guest
./vz-macos run -proxy http://192.168.64.1:8080

# Linux guest
./vz-macos run -linux -proxy http://192.168.64.1:8080
```

Linux writes:
- `/etc/environment.d/99-vz-macos-proxy.conf`
- `/etc/profile.d/99-vz-macos-proxy.sh`

macOS applies proxy settings with `networksetup` through the guest user agent.
On clean shutdown, `vz-macos` restores the previous proxy state best-effort.

Troubleshooting:
- Linux stopped-VM preflight rejects if `config.json` says the agent was not requested. Run `vz-macos provision-agent` or reinstall without `-no-agent`.
- macOS may report an unknown preflight and continue to a runtime probe. That is expected when the VM is stopped and only injection state is known.
- If proxy restore fails, the VM directory retains `.proxy-state.json`. Boot the VM again with the same `-proxy` setting and stop it cleanly, or restore the guest proxy manually before removing the state file.

## Requirements

- Apple Silicon Mac (M1/M2/M3/M4)
- macOS 12.0+ (Monterey or later)
- Xcode Command Line Tools (`codesign`, used by the automatic first-launch signing step)

## Project Structure

```
main.go              CLI entry point and VM configuration
macos.go             macOS VM creation and lifecycle
installer.go         macOS installation from IPSW
linux.go             Linux VM support with EFI boot
linux_installer.go   Cloud-init based Linux installation
provision*.go        Disk injection provisioning system
password.go          Password hashing and auto-login
control_socket.go    Unix socket server for VM control
screenshots.go       Screen capture via CGWindowListCreateImage
screen_detection.go  UI state detection heuristics
agent_*.go           Guest agent client and control
vzscript*.go         Scripted VM configuration engine
cmd/vz-agent/        Guest-side gRPC daemon
proto/               Protocol buffer definitions
```

## Feature Maturity

| Maturity | Features |
|----------|----------|
| GA | install, run, provisioning (inject), vzscripts |
| Beta | snapshots, agent, clipboard sharing |
| Experimental | UTM import, memory balloon, Windows stub |

## Security Model

- **Control socket** requires a per-VM bearer token from `~/.vz/vms/<name>/control.token` and uses owner-only socket/file permissions (`0600`).
- **Agent gRPC** is unencrypted, designed for use within the VM boundary over vsock. Do not expose the agent port outside the host.
- **Full Disk Access (FDA)** may be required for certain disk operations when using `inject` or `verify`.
- **UTM import** is limited to Apple-backend macOS bundles (.utm with QEMU backends are not supported).

## References

- [Apple Virtualization Framework](https://developer.apple.com/documentation/virtualization)
- [purego](https://github.com/ebitengine/purego)
- [Code-Hex/vz](https://github.com/Code-Hex/vz) (CGO reference implementation)

## Release

Before tagging a release, run:

```bash
make release-check
```

This runs `go test -short`, `go vet`, and a local Goreleaser snapshot build without using your local `go.work`.
