---
title: FAQ
---
# FAQ

### What hardware do I need?

cove requires Apple Silicon (M1 or later). It uses Apple's Virtualization.framework, which only supports hardware virtualization on ARM64. Intel Macs are not supported.

### What macOS versions are supported?

The host must run macOS 12 (Monterey) or later. Guest VMs can run macOS 13 (Ventura) through macOS 15 (Sequoia). The guest version must be equal to or older than the host version.

### Does cove require root or sudo?

No normal cove workflow should require rerunning with `sudo`. macOS disk
provisioning needs administrator privileges for root-owned LaunchDaemon files,
so cove asks through the native macOS admin dialog when it applies those files.
All commands still start as your normal user.

### Why does provisioning ask for administrator approval?

launchd silently ignores LaunchDaemon plists that aren't owned by root:wheel
(uid=0, gid=0). Cove uses the native admin dialog to write those files with the
right ownership. See the [provisioning guide](../guides/provisioning.md) for
details.

### What is the default VM directory?

VMs are stored in `~/.vz/vms/`. Each VM gets its own subdirectory (e.g., `~/.vz/vms/default/`). Use `-vm <name>` to create or manage named VMs.

### Can I run multiple VMs at once?

Yes. Each VM needs a unique name. Run `cove run -vm work` in one terminal and `cove run -vm test` in another. Each gets its own directory, control socket, and window.

### Does suspend and resume save full VM state?

Yes. When you quit, cove saves CPU registers, memory contents, and device state to `suspend.vmstate`. On next launch, the VM resumes exactly where it left off with no boot sequence. See [Suspend & Resume](../features/suspend-resume.md).

### How much disk space do snapshots use?

Disk snapshots use APFS clonefile (copy-on-write). A snapshot is instant and initially consumes no extra space. Only blocks that change after the snapshot consume additional disk. VM state snapshots store full memory contents, so they use roughly the amount of RAM assigned to the VM. See [Snapshots](../features/snapshots.md).

### Can I use cove without a GUI?

Yes. Run `cove run -headless` to start the VM without opening a window. You can interact with it through the control socket, guest agent, or SSH.

### Does cove work with Docker?

No. Docker Desktop on macOS uses its own Linux VM (via Virtualization.framework or Hypervisor.framework). cove runs macOS and Linux VMs as standalone guests. They are separate virtualization layers and don't interact.

### Can I run x86 guests?

No. Apple's Virtualization.framework only supports ARM64 guests. If you need x86 guests on Apple Silicon, use [UTM](https://mac.getutm.app) which provides x86 emulation via QEMU.

### How does cove compare to Lume, Tart, and UTM?

See the [comparison page](comparison.md) for a detailed feature matrix. In short: cove focuses on scriptable macOS VMs with suspend/resume, snapshots, and VZScript automation. Lume targets AI agent orchestration via REST API. Tart targets CI/CD with OCI images. UTM supports x86 emulation.

### How do I share files between host and guest?

Use VirtioFS shared folders. Mount a host directory with `cove run -v ~/projects` and it appears in the guest as a mounted volume. See [Shared Folders](../features/shared-folders.md).

### Can I SSH into the VM?

Yes. Enable SSH via the guest agent (`cove ctl agent-sshd on`) or during provisioning. However, the guest agent provides command execution, file transfer, and clipboard sharing over vsock without any network configuration. See [Guest Agent](../features/guest-agent.md).

### What is VZScript?

VZScript is a declarative recipe engine for guest VM configuration. Recipes are txtar files with shell commands and embedded files that run via the guest agent. Built-in recipes cover common setups like Homebrew, Golang, Xcode CLI tools, and more. See [VZScript Engine](../features/vzscript.md).
