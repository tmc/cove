---
name: cove
description: "Manages macOS and Linux VMs on Apple Silicon via Apple's Virtualization.framework. Create, run, suspend/resume, snapshot, provision, and automate VMs. Control via Unix socket, guest agent, or VZScript recipes."
when_to_use: "User mentions VMs, virtual machines, macOS VM, cove, wants to create/run/manage a VM, snapshot a VM, run scripts inside a VM, automate macOS setup, or interact with a running VM via the control socket or guest agent."
allowed-tools: Bash(*), Read, Glob, Grep, Write
argument-hint: "[command] [args...]"
---

# cove — macOS VM Manager

macOS VMs that suspend, snapshot, and script. Pure Go, cgo-free.

## Commands and Flags

```
!`cove --help 2>&1 | head -80`
```

## Interpreting $ARGUMENTS

| Argument | Action |
|----------|--------|
| (empty) | Run `cove --help`, ask what to do |
| `up` | Install + provision + boot in one command |
| `install` | Download IPSW and install macOS |
| `run` | Boot an existing VM |
| `inject` or `provision` | Inject user/agent into VM disk |
| `ctl` | Control a running VM (screenshot, type, click, exec) |
| `vzscript` | Run automation recipes |
| `sip` | Manage System Integrity Protection |
| `snapshot` or `disk-snapshot` | VM state or disk snapshots |
| `shared-folder` | Manage VirtioFS mounts |
| a VM name | Operate on that VM (e.g., `cove -vm myvm run`) |

## Quick Workflows

### Zero to running VM (one command)
```bash
cove up -user myuser
# With dev tools:
cove up -user myuser -vzscripts homebrew,golang
```

### Step by step
```bash
cove install                          # Download IPSW, install macOS
sudo cove inject -user me -password secret -skip-setup-assistant
cove run                              # Boot with GUI
```

### Linux VM
```bash
cove install -linux
cove run -linux -gui
```

### Control a running VM
```bash
# Get VM status
cove ctl status

# Take screenshot
cove ctl screenshot /tmp/screen.png

# Run command in guest
cove ctl agent-exec -- ls /

# Type text
cove ctl type "hello world"

# Run vzscript recipes
cove vzscript run homebrew golang
```

### Snapshots
```bash
cove disk-snapshot save before-update
# ... make changes ...
cove disk-snapshot restore before-update
```

### Shared folders
```bash
# Mount at run time
cove run -share ~/projects -share /data:ro

# Add persistent shared folder
cove shared-folder add ~/ml-explore
cove run    # auto-mounts tagged folders
```

### Suspend / Resume
```bash
cove run              # VM runs, quit to suspend
cove run              # Resumes from saved state
cove run -no-resume   # Cold boot instead
```

## Critical Flags

- **`-vm <name>`** — Select which VM to operate on (default: "default"). Place BEFORE the subcommand: `cove -vm myvm run`
- **`-gui` / `-headless`** — GUI is default for `run`; use `-headless` for CI/scripting
- **`-share <path[:ro]>`** — Mount host directory via VirtioFS (repeatable)
- **`-no-resume` / `-cold-boot`** — Discard saved suspend state
- **`-disposable`** — Run from a linked clone that's discarded on exit
- **`-cpu <N>` / `-memory <N>`** — CPU cores and memory in GB

## Things `--help` Won't Tell You

**sudo is required for inject** — launchd requires LaunchDaemon plists to be owned by root:wheel. Without sudo, the provision script never runs and the user isn't created.

**Auto-signing** — cove signs itself with virtualization entitlements on first launch. If you rebuild from source, re-sign: `codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove`

**VM directory** — VMs live in `~/.vz/vms/<name>/`. Each has: `disk.img`, `control.sock`, `control.token`, `suspend.vmstate`.

**Control socket auth** — every VM gets a bearer token at `~/.vz/vms/<name>/control.token`. Use it with ctl commands or direct socket access.

**VZScript recipes** — list built-in recipes with `cove vzscript list`. Run multiples: `cove vzscript run homebrew golang claude-code`. Dependencies resolve automatically.

**Shared folders need boot-time presence** — VirtioFS devices must exist when the VM boots. If you add a folder after suspend/resume, reboot the VM.

**Config fingerprinting** — changing CPU count, memory, or device configuration invalidates saved suspend state. The VM will cold boot instead.

## Error Recovery

| Error | Fix |
|-------|-----|
| "sandbox preferences" | Binary needs signing: `codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove` |
| "Resource temporarily unavailable" on inject | Stop the VM first — can't write to disk while running |
| "could not find Data partition" | Check `cove inject -verbose` output |
| User not created on boot | Run `inject` with `sudo` — LaunchDaemon needs root:wheel ownership |
| Setup Assistant still appears | Use `-skip-setup-assistant` flag with inject |
| Suspend state invalid | Config changed — VM will cold boot. Use `-no-resume` explicitly |
| "Operation not permitted" on mount | VirtioFS folder was added after resume — reboot VM |
| DFU error 3004/4014 during install | Clean VM dir (`rm -rf ~/.vz/vms/default`), retry install |
