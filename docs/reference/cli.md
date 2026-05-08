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
| `-distro <name>` | ubuntu | Linux distro: ubuntu, debian, fedora, alpine, nixos |
| `-nixos` | false | Install NixOS (implies `-linux -distro nixos`) |
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
| `-shell` | false | Attach the host terminal to a guest shell after boot (Linux only; mutually exclusive with `-headless`). Output-only in v0.2 -- see [Linux VMs > Guest Shell](../features/linux.md#guest-shell--shell). |
| `-cpu <n>` | 2 | Number of CPUs |
| `-memory <n>` | 4 | Memory in GB |
| `-display <spec>` | | Display config: WxH[@PPI] or preset (4k, 1080p, 720p, retina) |
| `-network <mode>` / `--net <mode>` | nat | Network mode: nat, bridged:\<iface\>, host-only, none |
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
| `--port-forward <host:guest>` / `--pf <host:guest>` | | Forward host TCP to a guest vsock port (repeatable) |
| `-disposable` | false | Run from a disposable linked clone |
| `-fork-from <ref>` | | Boot a fresh VM forked from a parent VM name or local image ref (`<name>` or `<name>:<tag>`); see [`cove image`](#image). Auto-bundles per-run artifacts (`manifest.json`, `events.jsonl`, `stdout.log`, `stderr.log`, `screenshots/`) under `~/.vz/runs/<run-id>/` for post-mortem inspection. |
| `-ephemeral` | false | Mark a forked VM as disposable: removed on stop and swept by `cove gc`. Required for ephemeral CI runners; see [design 024](../designs/024-cove-runner-images.md). |
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
cove run -linux -shell                         # pipe a guest shell to the host terminal
cove run -fork-from macos-base -name worker-1  # fork from a stopped VM
cove run -fork-from macos-runner:14.5 -ephemeral # ephemeral CI runner from an image
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
cove provision -user testuser -skip-setup-assistant
cove provision -user testuser -password secret -stage-only
cove provision -apply
```

---

## provision-agent

Inject only the vz-agent daemon (no user provisioning).

```
cove provision-agent
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

## image

Local pre-baked VM image store at `~/.vz/images/<name>/<tag>/`. Snapshots a stopped VM bundle (manifest + clonefile-backed disk + identity files) so `cove run -fork-from <image-ref> -ephemeral` can spawn disposable VMs from a saved baseline. Slice 2 adds OCI registry push/pull with `oras-go`; tarball `push`/`load` remain available as operator transport. See [design 024](../designs/024-cove-runner-images.md).

```
cove image build -from <vm> -tag <name[:tag]>
cove image list
cove image inspect <name[:tag]> [-json]
cove image verify <name[:tag]> [-strict] [-json] [-quiet] [-newer-than <duration>]
cove image push <name[:tag]> <file> [-gzip]
cove image load <file> [-tag <name[:tag]>] [-force]
cove image gc [-dry-run] [-yes] [-older-than <duration>]
cove image prune [-dry-run] [-yes] [-older-than <duration>]
cove image tag <src> <dst>
cove image history <ref> [-json]
cove image search <query>
cove image rm <name[:tag]>
```

| Subcommand | Description |
|------------|-------------|
| `build -from <vm> -tag <ref>` | Snapshot a stopped VM into the image store. The disk is APFS-clonefiled (no copy). vmstate is excluded; cold-boot only. |
| `list` | Show stored images with size + creation time + source VM. |
| `inspect <ref> [-json]` | Print manifest (size, sha256, base image, created-at, hw.model fingerprint) plus the live downstream fork list. `-json` emits a stable schema for tooling. |
| `verify <ref> [-strict] [-json] [-quiet] [-newer-than D]` | Check freshness, provenance, and layout. Warns on stale or legacy manifests; `-strict` turns missing `execattach.v3` into a failure; `-quiet` prints only on failure for CI; `-newer-than` fails stale images such as `24h` or `7d`. |
| `push <ref> <file> [-gzip]` | Tar an image directory to a single file (atomic temp + rename). `-gzip` compresses; the load side sniffs `.gz` / `.tgz` automatically. Pass `-` as the file to stream the tarball to stdout (refuses a TTY). |
| `load <file> [-tag <ref>] [-force]` | Extract a tarball into the image store. Tar entries are restricted to `manifest.json`, `disk.img`, `aux.img`, `hw.model`, `machine.id` (TypeReg only); zip-slip / symlink / oversize entries are refused before any filesystem write. `-tag` rewrites the manifest's name+tag on import; `-force` overwrites an existing ref. `ParentImage` is **not** preserved across hosts -- a loaded image becomes a fresh root for forks on the destination. Pass `-` as the file to read the tarball from stdin (refuses a TTY); gzip framing is auto-detected via magic bytes. |
| `gc [-dry-run] [-yes] [-older-than D]` | Sweep images with zero live forks. `-dry-run` plans only; `-yes` skips the confirmation prompt; `-older-than` filters by manifest `createdAt`. Re-checks fork count immediately before deletion to close the planning -> remove TOCTOU window. |
| `prune [-dry-run] [-yes] [-older-than D]` | Prune unused images through the v0.4 image lifecycle UX. |
| `tag <src> <dst>` | Add a local image tag for an existing image ref. |
| `history <ref> [-json]` | Show local image ancestry/history information. |
| `search <query>` | Search local images by name/tag metadata. |
| `rm <ref>` | Delete an image. Refuses while any forked VM still references the image (`ParentImage` on the child's `config.json` is the gate). |

```bash
cove image build -from macos-base -tag macos-runner:14.5
cove image inspect macos-runner:14.5 -json
cove image push macos-runner:14.5 /tmp/macos-runner.tar.gz -gzip
cove image load /tmp/macos-runner.tar.gz -tag macos-runner:imported
cove image gc -dry-run -older-than 168h
cove image tag macos-runner:14.5 macos-runner:latest
cove image history macos-runner:latest
cove image search runner
cove image list
cove run -fork-from macos-runner:14.5 -ephemeral -name worker-1
cove image rm macos-runner:14.5
cove image push macos-runner:14.5 - | ssh other-mac cove image load -
```

---

## agent-sandbox

Run a computer-use provider loop in a fresh fork from a local image.

```
cove agent-sandbox run --provider openai|anthropic|gemini|vertex --image <ref> --task <prompt> [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--provider <name>` | | Provider loop: `openai`, `anthropic`, `gemini`, or `vertex` |
| `--image <ref>` | | Local image ref to fork for the run |
| `--task <prompt>` | | Provider task prompt |
| `--screenshot-dir <dir>` | `~/.vz/runs/<run-id>/screenshots` | Directory for provider screenshots |
| `--max-steps <n>` | 25 | Maximum provider tool-call rounds |
| `--vm <name>` | generated | Ephemeral fork name |

The command writes `~/.vz/runs/<run-id>/replay/` with numbered screenshots,
OCR text, control events, final answer, and a `metrics.jsonl` symlink.

```bash
cove agent-sandbox run --provider anthropic --image macos-agent:latest --task "Describe the desktop."
cove agent-sandbox run --provider gemini --image macos-agent:latest --task "Open Safari and read the title."
```

---

## action

Preflight helpers for the private GitHub Actions executor.

```
cove action doctor [--json]
cove action prepare-image <ref> [--json] [--force] [--ttl <duration>]
```

| Subcommand | Description |
|------------|-------------|
| `doctor [--json]` | Check host-side action prerequisites: signed `cove` binary, virtualization entitlement, `~/.vz` disk capacity, network interface listing, optional fork image agent metadata, and writable run artifact root. Read-only. |
| `prepare-image <ref> [--json] [--force] [--ttl D]` | Check that a local image ref exists, can be forked for action jobs, has a current guest agent, can run shell commands through the agent, has runner dependencies, has enough disk headroom, and has no stale forks. Fresh images skip repeated checks unless `--force` is set; `--ttl` controls freshness. |

With `--json`, the command prints a machine-readable preflight result instead of
operator text. The JSON result includes an overall `ok` value and per-check
status records suitable for CI gating.

Exit codes:

| Code | Meaning |
|---:|---|
| `0` | All required checks passed. |
| `1` | One or more checks failed. |
| `2` | Warning-only result, such as low but still usable free disk space. |

Examples:

```bash
cove action doctor
cove action doctor --json
cove action prepare-image macos-runner:14.5 --ttl 24h
cove action prepare-image ubuntu-runner --json
```

---

## runs

Inspect and export local run artifacts under `~/.vz/runs/<run-id>/`. Run metrics
are read from `~/.vz/runs/<run-id>/metrics.jsonl`; each line is one JSON event.
See [Run Metrics](../features/metrics.md) and [Runs UX](../features/runs-ux.md).

```
cove runs list [--limit N] [--since DURATION] [--status ok|fail|all] [--json]
cove runs show <run-id-prefix> [--json]
cove runs export <run-id-prefix> --format json|gha-summary|tar
```

| Subcommand | Description |
|------------|-------------|
| `list [--limit N] [--since DURATION] [--status ok\|fail\|all] [--json]` | List recent runs. Fields: run-id prefix, `image_ref`, `vm_name`, `status`, `total_duration_ms`, `exit_code`, `started_at`. |
| `show <run-id-prefix> [--json]` | Show one run by unique run-id prefix. Fails if the prefix matches no run or more than one run. |
| `export <run-id-prefix> --format json\|gha-summary\|tar` | Export one run. `json` emits structured data, `gha-summary` emits Markdown for `GITHUB_STEP_SUMMARY`, and `tar` writes a gzip tar archive to stdout. |

`runs show` and `runs export` use prefix matching against local run directory
names. Ambiguous prefixes fail instead of guessing; pass more run-id characters
to select a single run.

```bash
cove runs list --limit 20 --since 24h --status all
cove runs list --json
cove runs show 20260505
cove runs export 20260505 --format gha-summary >> "$GITHUB_STEP_SUMMARY"
cove runs export 20260505 --format tar > cove-run.tar.gz
```

---

## fleet

Register trusted Mac hosts and route selected commands over SSH. Fleet commands
operate on hosts you control; they do not create a hosted queue. For Cirrus
migration context, see [Fleet Quickstart](../quickstart/fleet.md) and
[Migrate from Cirrus](../migrations/from-cirrus.md).

```
cove fleet add <name> <ssh-target> [--root <path>]
cove fleet ls
cove fleet rm <name>
cove --fleet=<name> <command> [args...]
cove fleet vm list [--json]
cove fleet image list [--json]
cove fleet ps [--json] [--watch]
cove fleet image push <ref> <dst-host>
cove fleet image pull <ref> <src-host>
cove fleet image sync <ref> <src-host> <dst-host>
cove fleet run --policy=least-loaded [run flags...]
```

| Subcommand | Description |
|------------|-------------|
| `add <name> <ssh-target>` | Register a trusted remote Mac reachable by SSH. |
| `ls` / `list` | List registered fleet hosts. |
| `rm` / `remove <name>` | Remove a fleet host registration. |
| `--fleet=<name> <command>` | Route supported `ctl`, `shell`, `cp`, `logs`, `list`, `vm list`, `image list`, and `run` commands to a remote host. |
| `vm list [--json]` | Aggregate VM lists across registered hosts. |
| `image list [--json]` | Aggregate local image lists across registered hosts. |
| `ps [--json] [--watch]` | Aggregate running VM/process state across registered hosts. |
| `image push <ref> <dst-host>` | Stream a local image ref to another fleet host. |
| `image pull <ref> <src-host>` | Pull an image ref from another fleet host. |
| `image sync <ref> <src-host> <dst-host>` | Copy an image ref between two fleet hosts. |
| `run --policy=least-loaded` | Place a run on the least-loaded registered host. |

```bash
cove fleet add mini-1 mini-1.local
cove fleet vm list
cove fleet image push macos-runner:latest mini-1
cove fleet run --policy=least-loaded -fork-from macos-runner:latest -ephemeral
```

---

## daemon / coved

`coved` is the user-session coordinator for lifecycle enforcement and image GC.
The one-shot `cove daemon` command starts, stops, or queries that daemon.

```
coved
cove daemon start [-coved <path>]
cove daemon stop
cove daemon status [--json]
```

| Command | Description |
|---------|-------------|
| `coved` | Start the host-side coordinator daemon. |
| `cove daemon start [-coved <path>]` | Start `coved`, optionally from an explicit binary path. |
| `cove daemon stop` | Stop the user-session daemon. |
| `cove daemon status [--json]` | Show daemon reachability, lifecycle enforcement, and image-GC counters. |

```bash
coved
cove daemon status
cove daemon status --json
```

---

## policy and quota

Lifecycle policies and resource quotas are stored per VM.

```
cove policy <vm> show
cove policy <vm> clear
cove policy <vm> idle <duration>
cove policy <vm> max-age <duration>
cove policy <vm> run-budget <duration>
cove policy <vm> set idle=<duration> max-age=<duration> run-budget=<duration>
cove quota <vm> show
cove quota <vm> cpu <n>
cove quota <vm> memory <gb>
cove quota <vm> disk <gb>
```

| Command | Description |
|---------|-------------|
| `policy <vm> show` / `vm policy show` | Show idle, max-age, and run-budget policy for a VM. |
| `policy <vm> clear` | Clear the VM lifecycle policy. |
| `policy <vm> idle|max-age|run-budget <duration>` | Set one lifecycle policy threshold. |
| `policy <vm> set ...` | Set multiple lifecycle policy fields at once. |
| `quota <vm> show` | Show durable CPU, memory, and disk quota intent. |
| `quota <vm> cpu|memory|disk <n>` | Update quota intent; disk quota applies the APFS quota wrapper. |

```bash
cove policy ci-runner idle 30m
cove policy ci-runner run-budget 8h
cove quota ci-runner cpu 4
cove quota ci-runner memory 8
```

---

## storage

Read-only census of cove disk usage under `~/.vz/`, plus a persisted
operator-set budget. Phases 1-2 of design 040.

```
cove storage census                      # JSON
cove storage census -human               # fixed-width table
cove storage census -top N               # surface N newest items per category (default 10)
cove storage budget get [-human]         # show the persisted storage budget
cove storage budget set -target SIZE [-warn PCT] [-hard PCT]
cove storage budget clear                # remove the persisted storage budget
```

The walker reports per-category byte sums for `vms`, `images`, `runs`,
`cache`, `build-scratch`, and `store`. Missing category directories report
zero rather than failing, so a fresh install reads as expected. The on-wire
schema uses bytes; human rendering converts to GB.

`storage budget set -target` accepts a decimal byte count or a binary
shorthand (`KB`/`MB`/`GB`/`TB`, where `1 KB = 1024 B` to match every other
size in cove). `-warn` and `-hard` are tripwire percentages of the target
(default 80 and 95). The budget is persisted at `~/.vz/storage-budget.json`.
When a budget is configured, `cove storage census` surfaces the target,
remaining headroom, and a `[WARN]` or `[HARD]` marker once usage crosses
the corresponding tripwire.

Census is read-only and never mutates state. Eviction and pinning land in
later phases of design 040.

---

## logs / cp / diff / forward / network logs

v0.4 adds small operator commands for moving data, reading logs, comparing
images, and exposing selected ports.

```
cove logs <vm> [-f|--follow]
cove cp <host-path> <vm:/guest/path>
cove cp <vm:/guest/path> <host-path>
cove diff <ref-a> <ref-b> [-json]
cove forward <vm> <hostport>:<vmport>
cove forward <vm> -reverse <vmport>:<hostport>
cove forward <vm> udp:<hostport>:<vmport>
cove network logs <vm> [-f]
```

| Command | Description |
|---------|-------------|
| `logs <vm> [-f\|--follow]` | Tail guest logs through the agent/control path. |
| `cp` | Copy a file host-to-guest or guest-to-host using `vm:/absolute/path` syntax. |
| `diff <ref-a> <ref-b> [-json]` | Compare local image manifests/layers. |
| `forward` | Forward TCP/UDP between host and guest; `-reverse` exposes guest-to-host direction. |
| `network logs <vm> [-f]` | Tail network policy audit events. |

```bash
cove logs ubuntu-runner -f
cove cp ./artifact.txt ubuntu-runner:/tmp/artifact.txt
cove cp ubuntu-runner:/etc/os-release ./os-release
cove diff macos-runner:old macos-runner:new -json
cove forward dev 8080:80
cove forward dev -reverse 3000:8080
cove network logs dev -f
```

Computer-use bridges that drive a running VM through the control socket
ship as Python helpers under `adapters/`. See
[Anthropic Computer Use](../examples/anthropic-computer-use.md) and
[Gemini Computer Use](../examples/gemini-computer-use.md) for end-to-end
walkthroughs.

---

## shell

Docker-shaped exec into a running VM via the per-VM control socket. Default command: `bash -l`. Stdin remains read-only in this release (Slice 3 / v0.3 will ship bidirectional stdin). See [design 023](../designs/023-cove-shell-exec-ux.md).

```
cove shell <vm> [-- <argv>...]
```

- Forwards SIGWINCH to `agent-exec-resize` on each terminal resize.
- Detaches the main cove SIGINT handler so Ctrl-C reaches the guest, not the host.
- Propagates the guest exit code.
- Friendly errors for VM-not-running, bad token, agent unreachable.

```bash
cove shell my-vm                                # interactive bash -l
cove shell my-vm -- ls /tmp                     # one-shot
cove shell my-vm -- /bin/bash -c 'echo hi >&2; exit 7'
```

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
| `tree [--json] [--orphans] [--reachable-from <ref>]` | Print fork lineage. `--orphans` lists only VMs whose parent is missing. `--reachable-from <image-ref>` shows VMs forked from the given image as a one-hop tree rooted at the image (mutually exclusive with `--orphans`; compatible with `--json`). |
| `config export <path>` | Export framework config snapshot |
| `config import <path>` | Import framework config snapshot |
| `shared-folder ...` | Manage shared folders |

```bash
cove vm tree
cove vm tree --orphans
cove vm tree --reachable-from macos-runner:14.5
cove vm tree --reachable-from macos-runner:14.5 --json
```

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
| `pending [vm]` | List configured folders not mounted in the running guest |
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
| `--cache-from <ref>` | | Reserved for registry cache import (repeatable) |
| `--cache-to <ref>` | | Reserved for registry cache export (repeatable) |
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

Registry cache import/export is not implemented yet. Builds that pass
`--cache-from` or `--cache-to` fail before planning instead of silently ignoring
the remote cache ref.

Scripts may declare `# secret:` names for host environment variables that must
exist before guest execution starts. During the step, declared values are written
as `0600` files under `/tmp/cove-secrets/<NAME>` in a guest tmpfs or RAM disk,
then unmounted after the script finishes. Linux guests fail closed if swap cannot
be disabled before secrets are mounted. Use `# cache-env:` only for non-secret
cache inputs; names that look like tokens, passwords, secrets, or keys emit a
warning.

Build compaction modes are step-local: `fast` skips guest cleanup, `targeted`
clears common churn paths before the diff, and `thorough` runs the full
agent-aware free-space compactor.

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
| `fork` | CoW-fork a VM with a fresh identity (`cove fork <parent> <child>`) |
| `bench` | Normalize benchmark evidence into reports and run metrics |
| `pit` | Experimental point-in-time save, restore, run, and swap |
| `softreset` | Run destructive soft-reset probe matrix |
| `store` | Manage the local content-addressed OCI blob store |
| `status` | Show running VM status |
| `helper` | Manage the privileged helper (install, uninstall, status) to skip per-run sudo prompts |
