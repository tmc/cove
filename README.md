# cove

macOS VMs that suspend, snapshot, and script.

[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/Platform-Apple%20Silicon-000000?logo=apple&logoColor=white)](https://developer.apple.com/documentation/virtualization)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

cove is a CLI for creating and managing macOS and Linux virtual machines on Apple Silicon using Apple's Virtualization.framework. Pure Go, cgo-free ([purego](https://github.com/ebitengine/purego)).

Cirrus migration? Start with [Cove for Cirrus CI migration](docs/landing/cirrus-displacement.md), then check the citable [competitive proof table](docs/strategy/proof.md).

## Install

```bash
brew install tmc/tap/cove
```

Or from source:

```bash
go install github.com/tmc/vz-macos@latest
```

See [INSTALL.md](INSTALL.md) for first-run requirements, IPSW reuse, and the macOS virtualization license note.

## Quick Start

```bash
cove up -user myuser                    # install + provision + boot (one command)
```

Or step by step:

```bash
cove install                            # download IPSW and install macOS
cove inject -user myuser                # provision user, skip Setup Assistant
cove run                                # boot with native GUI window
```

On first launch, cove auto-signs itself with the required Virtualization.framework entitlements. No manual `codesign` step needed.

## License and Apple Virtualization Limits

cove is MIT-licensed. macOS guests still run under Apple's macOS Software License Agreement: the current [macOS Tahoe 26 SLA](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf) section 2B(iii) permits up to two additional virtualized macOS instances on each Apple-branded computer you own or control, for the listed development, testing, macOS Server, or personal non-commercial purposes. Cove does not bypass or expand that Apple limit; fleet capacity is hardware capacity.

This is a product note, not legal advice. Read the applicable Apple SLA for the macOS version you run: <https://www.apple.com/legal/sla/>. See [License and Virtualization Limits](docs/reference/license-comparison.md) for the cove, Lume, Tart, Orchard, and tart-guest-agent comparison.

## Features

### Suspend and Resume

VMs suspend to disk on quit and resume where they left off. Cold boot with `-no-resume`.

### Snapshots

VM state snapshots and APFS copy-on-write disk snapshots. Checkpoint before risky changes, restore in seconds.

```bash
cove disk-snapshot save before-update
cove disk-snapshot restore before-update
```

### VZScript Engine

Declarative recipes for guest VM configuration. Built on [rsc.io/script](https://pkg.go.dev/rsc.io/script) with guest-agent and OCR commands.

```bash
cove vzscript list                      # list built-in recipes
cove vzscript run homebrew golang       # install Homebrew, then Go (deps resolved)
cove vzscript run ./custom.vzscript     # run a custom script
```

Guest commands: `guest-exec`, `guest-shell`, `guest-cp`, `guest-write`, `guest-read`.
UI commands: `ocr-click`, `ocr-wait`, `type`, `key`, `click`, `screenshot`.

### SIP Management

Disable or enable System Integrity Protection with automated recovery boot.

```bash
cove sip disable-auto -user admin -password secret -confirm
cove run -recovery -gui -unattended -boot-commands ~/.vz/vms/default/sip-disable.vzscript
```

### Guest Agent

A vsock gRPC agent injected into the guest at install time. Execute commands, transfer files, manage proxy settings, share clipboard -- all without SSH.

```bash
cove run -clipboard -proxy http://192.168.64.1:8080
```

### Agent Sandbox

Run OpenAI, Anthropic, Gemini, or Vertex computer-use loops against fresh local
VM forks with replay artifacts and provider auth checks.

```bash
cove agent-sandbox doctor --provider anthropic
cove agent-sandbox run --provider anthropic --image agentkit/macos-base:latest --task "Describe the desktop."
```

Start with the [quickstart](docs/agent-sandbox/quickstart.md), then use the
[provider matrix](docs/agent-sandbox/provider-matrix.md),
[cookbook](docs/agent-sandbox/cookbook.md), and
[benchmark harness](bench/agent-sandbox-providers/results-20260505.md).

### Native GUI Window

macOS-native window with toolbar, menu bar, and frame persistence per VM. Multi-display support with resolution presets.

```bash
cove run -display 4k
cove run -display 1920x1080 -display 1024x768
```

### Linux VMs

Ubuntu Server with cloud-init automated install. EFI boot, Virtio GPU, serial console, Rosetta x86-64 translation.

```bash
cove install -linux
cove run -linux -gui -rosetta
```

### Shared Folders

VirtioFS volume mounts with runtime hot-add.

```bash
cove run -share ~/projects -share /data:ro
```

## Comparison

| | cove | [Lume](https://github.com/trycua/lume) | [Tart](https://github.com/cirruslabs/tart) | [UTM](https://mac.getutm.app) |
|---|---|---|---|---|
| Language | Go (purego) | Swift | Swift | Swift/Obj-C |
| Suspend/resume | Yes | No | No | Yes |
| VM state snapshots | Yes | No | No | Yes |
| Disk snapshots (APFS COW) | Yes | No | No | No |
| Script engine | VZScript (rsc.io/script) | No | No | No |
| Guest agent | vsock gRPC | vsock gRPC | No | SPICE agent |
| SIP management | Automated | No | No | Manual |
| Unattended provisioning | Disk injection + OCR | Cloud-init | Packer | Manual |
| Linux VMs | Yes | Yes | Yes | Yes (QEMU) |
| x86 guests | No | No | No | Yes (QEMU) |
| GUI | Native AppKit | Electron | None | Native AppKit |
| Control API | Unix socket (protobuf JSON) | HTTP REST | None | AppleScript |
| Open source | MIT | MIT | Fair Source 0.9 | Apache-2.0 |

## Usage Examples

### One-Command Setup

```bash
# Install, provision, and boot with vzscripts
cove up -user dev -vzscripts homebrew,golang
```

### Headless CI Runner

```bash
cove install -ipsw ~/cache/restore.ipsw
sudo cove inject -user ci -password secret -skip-setup-assistant
cove run -headless -cpu 4 -memory 8
```

Self-hosted GitHub Actions or GitLab runner inside a long-lived VM:

```bash
GH_REPO=tmc/cove GH_TOKEN=<reg-token> \
  cove vzscript run github-runner

GITLAB_URL=https://gitlab.com GITLAB_TOKEN=<token> \
  cove vzscript run gitlab-runner
```

### Tailscale Mesh Access

```bash
TS_AUTHKEY=tskey-auth-... cove vzscript run tailscale
# VM joins your tailnet with --ssh; reach it from anywhere.
```

### Control a Running VM

```bash
TOKEN=$(cat ~/.vz/vms/default/control.token)
echo '{"type":"status","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
echo '{"type":"screenshot","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

### Recovery and SIP

```bash
cove sip disable-auto -user admin -password secret -confirm
cove run -recovery -no-resume -gui -unattended \
  -usb ~/.vz/vms/default/recovery-disk.img \
  -boot-commands ~/.vz/vms/default/sip-disable.vzscript
```

## Architecture

cove uses Apple's Virtualization.framework through [purego](https://github.com/ebitengine/purego) for cgo-free Objective-C interop. VMs are stored in `~/.vz/vms/<name>/` with disk images, identity files, and a Unix domain control socket.

### Project Structure

```
cove/
├── main.go                     # CLI entry point, subcommand routing
├── macos.go                    # macOS VM configuration and lifecycle
├── linux.go                    # Linux VM configuration
├── installer.go                # macOS installation from IPSW
├── linux_installer.go          # Cloud-init based Linux installation
│
├── provision.go                # Core provisioning types and orchestration
├── provision_cli.go            # inject/provision CLI handling
├── provision_mount.go          # Disk mount/unmount for injection
├── provision_launchdaemon.go   # LaunchDaemon plist generation
├── provision_autologin.go      # kcpassword + loginwindow auto-login
│
├── control_socket.go           # Unix socket server for VM control
├── control_client.go           # Programmatic control client
├── ctl.go                      # ctl subcommand CLI
│
├── screenshots.go              # CGWindowListCreateImage capture
├── screen_detection_ocr.go     # OCR-based UI state detection
├── ocr.go                      # Vision framework OCR bindings
│
├── vzscript.go                 # VZScript engine (rsc.io/script)
├── vzscript_apply.go           # VZScript CLI and runner
├── vzscripts/                  # Built-in recipes (.vzscript)
│
├── agent_inject.go             # Cross-compile and inject guest agent
├── agent_client.go             # Agent client API
├── cmd/vz-agent/               # In-guest agent daemon (vsock gRPC)
│
├── snapshots.go                # VM state + disk-level snapshots
├── sip.go                      # SIP management
├── up.go                       # "up" command orchestrator
├── boot_commands.go            # Boot command DSL parser
├── unattended.go               # Unattended install orchestrator
│
├── proto/                      # Protobuf definitions (agent + control)
├── internal/autosign/          # Auto-signing with entitlements
└── swift/VZControl/            # Swift package for control socket client
```

## Requirements

- Apple Silicon Mac (M1/M2/M3/M4)
- macOS 12.0+ (Monterey or later)
- Xcode Command Line Tools

## Feature Maturity

| Maturity | Features |
|----------|----------|
| GA | install, run (auto-suspend on quit, resume on next run), provisioning (inject), vzscripts |
| Beta | snapshots, guest agent, clipboard sharing, shared folders, Linux guests, OCI push/pull, VM fork/restore, `cove compact`, local content-addressed store, `cove build` for local VM-directory bases (cache-aware execution, `# secret:` tmpfs, compaction) |
| Experimental | `cove build` registry-base execution and registry cache import/export (planning-only), UTM import, memory balloon, Windows stub |

## Security

- **Control socket**: per-VM bearer token, owner-only permissions (`0600`)
- **Guest agent**: unencrypted gRPC over vsock, scoped to host-VM boundary
- **Entitlements**: auto-signed on first launch with `com.apple.security.virtualization` and `com.apple.security.hypervisor`
- **Safety posture**: see [SAFETY.md](SAFETY.md) for trust boundaries, known limitations, and audit guidance.

## Contributing

```bash
git clone https://github.com/tmc/vz-macos
cd cove
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
./cove run
```

Run tests:

```bash
go test -short ./...
make release-check    # vet + test + goreleaser snapshot
```

## License

MIT -- see [LICENSE](LICENSE).

## References

- [Apple Virtualization Framework](https://developer.apple.com/documentation/virtualization)
- [purego](https://github.com/ebitengine/purego)
- [Code-Hex/vz](https://github.com/Code-Hex/vz) (CGO reference implementation)
