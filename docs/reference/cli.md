---
title: CLI Reference
---
# CLI Reference

## Global Flags

These flags apply to most commands:

| Flag | Default | Description |
|------|---------|-------------|
| `-vm <name>` | active VM or `default` | VM name to operate on |
| `-vm-dir <path>` | `~/.vz/vms/` | Directory for VM files |
| `-verbose` | false | Verbose output |
| `-pprof <addr>` | | Enable pprof diagnostics (e.g., `6060`) |

---

## install

Install macOS or Linux into a new VM.

```
cove install [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-ipsw <path>` | | Path to IPSW restore image (downloads latest if empty) |
| `-linux` | false | Install Linux instead of macOS |
| `-distro <name>` | ubuntu | Linux distro: ubuntu, debian, fedora, alpine |
| `-desktop` | false | Use Ubuntu Desktop ISO (implies `-linux`) |
| `-nested` | false | Enable nested virtualization for Linux guests on supported hosts |
| `-iso <path>` | | Path to ISO image for Linux EFI boot |
| `-cpu <n>` | 2 | Number of CPUs |
| `-memory <n>` | 4 | Memory in GB |
| `-disk-size <n>` | 64 | Disk size in GB |
| `-force` | false | Force install even if VM disk exists (destroys existing data) |
| `-provision-user <name>` | | Username for auto-provisioned user |
| `-provision-password <pw>` | | Password for auto-provisioned user |
| `-vzscripts <list>` | | Comma-separated recipes to run after install |

```bash
cove install
cove install -ipsw ~/restore.ipsw -cpu 4 -memory 8
cove install -linux -provision-user ubuntu -provision-password secret
```

---

## run

Boot and run a VM.

```
cove run [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-gui` | true | Show VM display in a window |
| `-headless` | false | Run without GUI window |
| `-no-resume` / `-cold-boot` | false | Discard saved suspend state |
| `-recovery` | false | Boot into macOS recovery mode |
| `-linux` | false | Run a Linux VM |
| `-nested` | false | Enable nested virtualization for Linux guests on supported hosts |
| `-cpu <n>` | 2 | Number of CPUs |
| `-memory <n>` | 4 | Memory in GB |
| `-display <spec>` | | Display config: WxH[@PPI] or preset (4k, 1080p, 720p, retina) |
| `-network <mode>` | nat | Network mode: nat, bridged:\<iface\>, vmnet, filehandle, none |
| `-v <mount>` / `-vol <mount>` | | Host directory mount: /host[:tag][:ro\|rw] (repeatable) |
| `-usb <path>` | | USB storage: /path/to/disk.img[:ro] (repeatable) |
| `-rosetta` | true | Enable Rosetta x86-64 translation (Linux VMs) |
| `-clipboard` | true | Host-guest clipboard sharing |
| `-serial <dest>` | stdout | Serial output: stdout, none, or file path |
| `-proxy <url>` | | Configure guest HTTP/HTTPS proxy |
| `-unattended` | false | Fully unattended install + setup |
| `-boot-commands <file>` | | Path to vzscript automation file |
| `-boot-args <args>` | | Boot arguments (e.g., `serial=3 -v`) |
| `-vnc <addr>` | | Start VNC server on port (e.g., `:5901`) |
| `-vnc-password <pw>` | | VNC server password |
| `-vnc-bonjour <name>` | | Bonjour service name for VNC |
| `-gdb <addr>` | | Attach GDB debug stub (e.g., `:1234`) |
| `-gdb-listen-all` | false | Listen on all interfaces for GDB |
| `-sandbox-level <level>` | | Research isolation: minimal or strict |
| `-pcap <path>` | | Write PCAP when using `-network filehandle` |
| `-disposable` | false | Run from a disposable linked clone |
| `-launch-order <mode>` | window-first | GUI startup order: window-first or start-first |
| `-runtime-profile <mode>` | full | macOS device profile: full or minimal |
| `-apple-log` | false | Stream Apple unified logs |
| `-apple-log-predicate <pred>` | | Custom NSPredicate for `-apple-log` |
| `-recover-identity` | false | Reset VM identity files if metadata missing |
| `-auto-mount-volumes` | true | Auto-mount tagged volumes via agent |
| `-auto-upgrade-agent` | false | Auto-upgrade guest agent on version mismatch |
| `-automation-backend <mode>` | auto | UI automation: auto, framebuffer, or window |
| `-automation-capture-backend <mode>` | | Override screenshot backend: auto, framebuffer, or window |
| `-automation-input-backend <mode>` | | Override input backend: auto, direct, or window |
| `-debug-ocr` | false | Save OCR debug screenshots |
| `-save-compress` | false | Compress suspend state |
| `-save-encrypt` | false | Encrypt suspend state |
| `-force-dfu` | false | Start macOS VM in DFU mode |
| `-iboot-stage1` | false | Stop in iBoot stage 1 |
| `-iboot-stage2` | false | Stop in iBoot stage 2 |

```bash
cove run
cove run -headless -cpu 4 -memory 8
cove run -display 4k -v ~/projects
cove run -linux -rosetta -serial /tmp/serial.log
cove run -recovery -no-resume -gui -usb ~/recovery.img
```

---

## up

Install, provision, and boot in one command.

```
cove up [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-user <name>` | | Username (required for macOS, optional for Linux) |
| `-password <pw>` | | Password (prompts if empty) |
| `-vzscripts <list>` | | Comma-separated recipes to run after boot |
| `-ipsw <path>` | | Path to IPSW (downloads latest if empty) |
| `-force` | false | Force install over existing VM |
| `-gui` | true | Show VM display |
| `-headless` | false | Run without GUI |
| `-cpu <n>` | 2 | Number of CPUs |
| `-memory <n>` | 4 | Memory in GB |
| `-disk-size <n>` | 64 | Disk size in GB |
| `-no-shutdown` | false | Leave VM running after vzscripts complete |
| `-vm <name>` | | VM name |
| `-linux` | false | Install Linux instead of macOS |
| `-distro <name>` | ubuntu | Linux distro: ubuntu, debian, fedora, alpine |
| `-desktop` | false | Use Ubuntu Desktop (implies `-linux`) |
| `-nested` | false | Enable nested virtualization for Linux guests on supported hosts |
| `-rosetta` | true | Enable Rosetta translation support for Linux |
| `-pprof <addr>` | | Serve pprof diagnostics (e.g., `6060`) |
| `-automation-backend <mode>` | auto | UI automation: auto, framebuffer, or window |
| `-automation-capture-backend <mode>` | | Override screenshot backend: auto, framebuffer, or window |
| `-automation-input-backend <mode>` | | Override input backend: auto, direct, or window |
| `-v` | false | Verbose output |

```bash
cove up -user me
cove up -user me -vzscripts homebrew,golang
cove up -user me -ipsw ~/restore.ipsw -cpu 4 -memory 8
cove up -linux -user tmc -password secret
cove up -linux -desktop -user me
```

---

## provision

Provision a VM with a user account, auto-login, and guest tools. Previously named `inject`.

```
cove provision [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-user <name>` | | Username (required) |
| `-password <pw>` | | Password (prompts if empty) |
| `-admin` | true | Make user an admin |
| `-skip-setup-assistant` | false | Skip Setup Assistant |
| `-auto-login` | true | Enable automatic login |
| `-no-auto-login` | false | Disable automatic login |
| `-plist` | false | Create user plist directly (advanced) |
| `-uid <n>` | 501 | User ID for plist mode |
| `-ssh-key <path>` | | SSH public key for authorized_keys |
| `-xcode-cli` | false | Install Xcode Command Line Tools |
| `-agent` | true | Inject vz-agent gRPC daemon |
| `-guest-tools` | true | Inject SPICE guest tools |
| `-enable-sshd` | false | Enable SSH on first boot |
| `-bootstrap-recovery` | true | Two-user recovery bootstrap |
| `-no-bootstrap-recovery` | false | Disable recovery bootstrap |
| `-stage-only` | false | Stage files only, no disk mount |
| `-apply` | false | Apply previously staged files |
| `-v` | false | Verbose output |

```bash
sudo cove provision -user testuser -skip-setup-assistant
cove provision -user testuser -password secret -stage-only
sudo cove provision -apply
```

---

## provision-agent

Inject only the vz-agent daemon (no user provisioning).

```
sudo cove provision-agent
```

---

## doctor / verify

Diagnose VM health: provisioning, agent, and file ownership.

```
cove doctor [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-v` | false | Verbose output |
| `-fix` | false | Attempt to fix issues automatically |
| `-tcc-path` | first non-system `/Volumes` mount | Guest path to use for the Full Disk Access probe |

```bash
cove doctor
cove doctor --fix
cove doctor --tcc-path /Volumes/work
```

---

## ctl

Interact with a running VM's control socket.

```
cove ctl [options] <command> [args...]
```

| Option | Default | Description |
|--------|---------|-------------|
| `-socket <path>` | auto-detected | Control socket path |
| `-timeout <dur>` | 10s | Command timeout |
| `-o <file>` | | Output file for screenshots |
| `-raw` | false | Output raw JSON response |
| `-wait <dur>` | | Wait/retry for agent commands |
| `-token <str>` | | Auth token (default: VM control.token) |

### Control Commands

| Command | Description |
|---------|-------------|
| `ping` | Test connection |
| `status` | VM state and capabilities |
| `capabilities` | Machine-readable protocol capabilities |
| `screenshot` | Capture VM screen |
| `screenshot -o file` | Save screenshot to file |
| `pause` | Pause VM |
| `resume` | Resume paused VM |
| `stop` | Force stop VM |
| `request-stop` | ACPI power button (graceful shutdown) |
| `network-info` | MAC address, guest IP, mode |

### GUI Commands

| Command | Description |
|---------|-------------|
| `gui status` | Headed or headless status |
| `gui open` | Show window for headless VM |
| `gui close` | Return to headless mode |
| `gui backend <mode>` | Set automation backend |
| `gui capture-backend <mode>` | Set screenshot backend |
| `gui input-backend <mode>` | Set input backend |

### Disk Commands

| Command | Description |
|---------|-------------|
| `disk list` | List runtime storage devices |
| `disk swap <attachment-id> <path>` | Hot-swap the backing file of an attached disk |
| `disk resize <path> <size-gb>` | Resize a disk image (in GB) |

### USB Commands

| Command | Description |
|---------|-------------|
| `usb list` | List USB controllers and devices |
| `usb attach-storage <path> [--ro]` | Hot-attach USB mass storage |
| `usb attach-host-service <id>` | Attach host USB by service ID |
| `usb attach-host-location <id>` | Attach host USB by location ID |
| `usb detach <name>` | Detach a runtime USB device by name |

### Memory Commands

| Command | Description |
|---------|-------------|
| `memory info` | Get memory balloon info |
| `memory set <GB>` | Set memory target |

### Snapshot Commands

| Command | Description |
|---------|-------------|
| `snapshot list` | List snapshots |
| `snapshot save <name>` | Save snapshot |
| `snapshot restore <name>` | Restore snapshot |
| `snapshot delete <name>` | Delete snapshot |

### Input Commands

| Command | Description |
|---------|-------------|
| `key <keycode> [down\|up]` | Send keyboard event |
| `mouse <x> <y> <action>` | Send mouse event (move\|down\|up\|click) |
| `text <string>` | Type text string |

### OCR Commands

| Command | Description |
|---------|-------------|
| `detect` | Detect screen state |
| `ocr [-region <spec>]` | Run OCR (spec: menu or x1,y1,x2,y2) |
| `click-text <text>` | Find text via OCR and click it |
| `click-menu <menu> <item>` | Click menu bar item |

### Agent Commands

| Command | Description |
|---------|-------------|
| `agent-connect` | Connect to guest agent |
| `agent-ping` | Ping guest agent |
| `agent-info` | Guest system info |
| `agent-exec <cmd> [args]` | Run command in guest |
| `agent-exec --daemon <cmd>` | Run as root |
| `agent-exec-stream <cmd>` | Stream command output |
| `agent-cp <host> <guest>` | Copy host to guest |
| `agent-cp -from-guest <guest> <host>` | Copy guest to host |
| `agent-read <path>` | Read guest file |
| `agent-write <path> <data>` | Write to guest file |
| `agent-shutdown [force]` | Graceful shutdown |
| `agent-reboot` | Reboot guest |
| `agent-sshd <on\|off\|status>` | Manage SSH |
| `agent-mount-volumes` | Mount VirtioFS volumes |
| `agent-status` | Agent health |

### Port Forwarding

| Command | Description |
|---------|-------------|
| `port-forward start <host:guest>` | Forward host TCP to guest vsock |
| `port-forward stop <hostPort>` | Stop a forward |
| `port-forward list` | List active forwards |

### Other Commands

| Command | Description |
|---------|-------------|
| `shared-folders-apply` | Reload shared folders into running VM |
| `boot-script <file>` | Execute a vzscript automation file |
| `setup-assist <user> <pass>` | Run Setup Assistant automation |
| `reset-password <user> <pass>` | Reset user password |
| `vnc status` | VNC server status |
| `debug-stub status` | Debug stub status |

```bash
cove ctl ping
cove ctl status
cove ctl screenshot -o screen.png
cove ctl agent-exec ls /tmp
cove ctl memory set 8
cove ctl disk list
```

---

## sip

SIP management.

```
cove sip <command> [flags]
```

| Command | Description |
|---------|-------------|
| `status` | Query SIP status via agent |
| `enable` | Show enable instructions |
| `disable` | Show disable instructions |
| `enable-auto` | Generate enable automation |
| `disable-auto` | Generate disable automation |
| `create-disk` | Create recovery tools disk |

```bash
cove sip status
cove sip disable-auto -user admin -password secret
```

---

## vzscript

Run guest-agent and UI automation scripts.

```
cove vzscript <command> [args...]
```

| Command | Description |
|---------|-------------|
| `list` | List built-in recipes |
| `show <recipe>` | Print recipe contents |
| `run [flags] <recipe...>` | Run one or more recipes |

Run flags:

| Flag | Default | Description |
|------|---------|-------------|
| `-v` | false | Verbose output |
| `-timeout <dur>` | 10m | Execution timeout |
| `-terminal` | false | Run in Terminal.app |
| `-auto-approve` | false | Auto-click Allow/OK dialogs |
| `-socket <path>` | | Control socket path (default: auto-detected) |
| `-daemon` | false | Route guest commands through daemon agent (root) |
| `-vm <name>` | | VM name (default: active VM or `default`) |
| `-parallel` | false | Run independent recipes concurrently |

```bash
cove vzscript list
cove vzscript show homebrew
cove vzscript run -v homebrew golang
cove vzscript run ./custom.vzscript
```

---

## snapshot

VM state snapshots.

```
cove snapshot <command> [args]
```

| Command | Description |
|---------|-------------|
| `list` | List snapshots |
| `save <name>` | Save current VM state |
| `restore <name>` | Restore a snapshot |
| `delete <name>` | Delete a snapshot |

---

## disk-snapshot

APFS copy-on-write disk snapshots.

```
cove disk-snapshot <command> [args]
```

| Command | Description |
|---------|-------------|
| `save <name>` | Save disk snapshot |
| `run <name>` | Boot a disposable clone from the snapshot |
| `list` | List disk snapshots |
| `restore <name>` | Restore disk snapshot |
| `delete <name>` | Delete disk snapshot |

---

## clone

Clone a VM.

```
cove clone <source> <destination> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-linked` | false | Create linked clone (APFS copy-on-write) |

---

## template

VM templates.

```
cove template <command> [args]
```

| Command | Description |
|---------|-------------|
| `save <name>` | Save VM as compressed template |
| `save-fast <name>` | Save as fast APFS clone template |
| `list` | List templates |
| `create <template> <name>` | Create VM from template |
| `delete <name>` | Delete template |

---

## vm

VM management.

```
cove vm <command> [args]
```

| Command | Description |
|---------|-------------|
| `set <name>` | Set active VM |
| `delete <name>` | Delete a VM |
| `rename <old> <new>` | Rename a VM |
| `export <name> <path>` | Export VM to tarball |
| `import <path> <name>` | Import VM from tarball |
| `config export <path>` | Export framework config snapshot |
| `config import <path>` | Import framework config snapshot |
| `shared-folder ...` | Manage shared folders |

---

## shared-folder

Manage shared folders for the active VM.

```
cove shared-folder <command> [args]
```

| Command | Description |
|---------|-------------|
| `list` | List configured folders |
| `status [mount-point]` | Check mount status |
| `add <host-path> [tag] [ro\|rw]` | Add a folder |
| `remove <tag-or-path>` | Remove a folder |
| `clear` | Remove all folders |
| `mount [mount-point]` | Mount in guest via agent |

---

## gc

Clean up disposable VM clones.

```
cove gc [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-dry-run` | false | Print without deleting |
| `-older-than <dur>` | | Only delete clones older than duration |

---

## serve

Run the HTTP and MCP gateway. Exposes VM control over HTTP (for multi-VM fleets and remote clients) and/or a stdio MCP server (for AI agent integrations such as Claude Code).

```
cove serve [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-http <addr>` | `:7777` (multi-VM) | HTTP listen address |
| `-mcp` | false | Serve MCP over stdio |
| `-token-file <path>` | | Auth token file (falls back to keychain) |
| `-per-vm-auth` | false | Require strict per-VM tokens instead of a master token |
| `-vms <allowlist>` | | Comma-separated list of VMs to expose |

```bash
cove serve -http 127.0.0.1:7777
cove serve --mcp
cove serve -http 127.0.0.1:7777 -per-vm-auth -vms ci-runner,dev-vm
```

---

## pull

Validate or pull an OCI image into a VM disk. Pull fetches the registry
manifest, streams verified LZ4 disk chunks into `disk.img.partial`, restores
macOS identity metadata, and atomically renames the verified disk into place.
Use `--dry-run` to validate the manifest and target without writing a disk.

```
cove pull <ref> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--as <name>` | inferred from ref | Destination VM name |
| `--dry-run` | false | Validate inputs without writing a disk |
| `--manifest <path>` | | Local OCI manifest JSON instead of fetching the registry |

```bash
cove pull ghcr.io/example/macos-sequoia:15.2 --dry-run
cove pull ghcr.io/trycua/macos-sequoia-vanilla:latest --as sequoia --dry-run --manifest manifest.json
```

---

## push

Plan or push a VM disk as an OCI image. Push compresses non-zero disk chunks as
LZ4 OCI layers, skips sparse zero chunks, uploads missing blobs, and publishes
the manifest tag. The source can be a VM name or an existing VM directory. Use
`--dry-run` to inspect the plan without uploading.

```
cove push <vm|dir> <ref> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--base <ref>` | | Base image for delta push |
| `--chunk-size <mb>` | 512 | Chunk size in megabytes |
| `--dry-run` | false | Print the plan without uploading |
| `--lume-compat` | false | Emit dual annotations for Lume interop |
| `--additional-tag <tag>` | | Additional tag to publish (repeatable) |
| `--manifest-out <path>` | | Write OCI manifest JSON to path |

```bash
cove push dev-vm ghcr.io/me/dev-vm:v1
cove push ~/.vz/build-scratch/20260430T120000Z-deadbeef ghcr.io/me/dev-vm:v1 --dry-run
cove push dev-vm ghcr.io/me/dev-vm:v2 --base ghcr.io/me/dev-vm:v1
cove push dev-vm ghcr.io/me/dev-vm:v2 --lume-compat --additional-tag latest
cove push dev-vm ghcr.io/me/dev-vm:v1 --dry-run --manifest-out manifest.json
```

---

## compact

Reclaim unused guest blocks on the VM disk. Agent-aware: runs `fstrim` on Linux guests and `diskutil secureErase freespace 0 /` on macOS guests. Fails cleanly if the guest agent is disconnected.

```
cove compact [options] [vm]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-vm <name>` | active VM | Target VM name |

```bash
cove compact
cove compact dev-vm
cove compact -vm dev-vm
```

---

## build

Build VM images from vzscript steps with content-addressed cache keys. `--dry-run`
prints the resolved plan, cache keys, and local cache hits without booting a
scratch VM. Non-dry-run execution currently requires a local VM directory as the
base; registry bases remain planning-only until base materialization lands.

```
cove build <name> --base <ref> --script <step> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--base <ref\|dir>` | | Base OCI image reference or local VM directory |
| `--script <name\|path>` | | Built-in vzscript recipe or .vzscript path (repeatable) |
| `--tag <ref>` | | Output OCI image tag (repeatable) |
| `--push` | false | Push output tags after build |
| `--dry-run` | false | Print the resolved build plan and cache keys only |
| `--no-cache` | false | Re-run every step instead of restoring cached layers |
| `--cache-from <ref>` | | Registry cache source (repeatable) |
| `--cache-to <ref>` | | Registry cache destination (repeatable) |
| `--keep-intermediate` | false | Leave scratch VMs behind for debugging |
| `--chunk-size <mb>` | 512 | Chunk size in MiB |
| `--compact <mode>` | targeted | Compaction mode: fast, targeted, or thorough |
| `--store-dir <dir>` | `~/.vz/store` | Content store directory |

```bash
cove build macos-workstation --base ghcr.io/me/base@sha256:... --script homebrew --dry-run
cove build macos-agent --base ~/.vz/base-vm --script ./agent.vzscript --tag ghcr.io/me/macos-agent:v1
cove build macos-agent --base ~/.vz/base-vm --script ./agent.vzscript --tag ghcr.io/me/macos-agent:v1 --push
```

Non-dry-run registry-base builds fail with
`cove build: non-dry-run requires local VM base directory`. `--push` requires at
least one `--tag` and pushes the reported final VM directory after a successful
local-base build.

Scripts may declare `# secret:` names for host environment variables that must
exist before guest execution starts. During the step, declared values are written
as `0600` files under `/tmp/cove-secrets/<NAME>` in a guest tmpfs or RAM disk,
then unmounted after the script finishes. Linux guests fail closed if swap cannot
be disabled before secrets are mounted. Use `# cache-env:` only for non-secret
cache inputs; names that look like tokens, passwords, secrets, or keys emit a
warning.

---

## Other Commands

| Command | Description |
|---------|-------------|
| `list` | List all VMs |
| `clean` | Clean VM directory |
| `version` | Print version |
| `network` | Network mode help |
| `rosetta` | Rosetta management (status, install, setup) |
| `agent-upgrade` | Upgrade guest agent |
| `disk-detach` | Force-detach VM disk image |
