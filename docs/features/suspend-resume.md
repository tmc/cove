---
title: Suspend & Resume
---
# Suspend & Resume

VMs suspend to disk on quit and resume where they left off on next launch.

## How It Works

When you close the VM window or send SIGINT/SIGTERM, cove saves the full VM state (CPU registers, memory, device state) to `~/.vz/vms/<name>/suspend.vmstate`. On the next `cove run`, the VM resumes from this saved state instantly -- no boot sequence, no login.

## Usage

Suspend happens automatically on quit. No flags needed:

```bash
cove run          # boots or resumes
# ... use the VM ...
# close window or Ctrl+C -> suspends to disk

cove run          # resumes instantly from saved state
```

## Cold Boot

To discard saved state and boot fresh:

```bash
cove run -no-resume
```

Or equivalently:

```bash
cove run -cold-boot
```

## Save Options

Advanced save options (experimental):

```bash
cove run -save-compress    # compress suspend state
cove run -save-encrypt     # encrypt suspend state
```

## Suspend State Location

```
~/.vz/vms/<name>/suspend.vmstate
```

> [!NOTE]
> Suspend state is saved to `~/.vz/vms/<name>/suspend.vmstate`. Delete this file to force a cold boot without using `-no-resume`.

## Limitations

> [!WARNING]
> Changing VM configuration (CPU count, memory, devices) invalidates saved suspend state. The VM will cold boot instead of resuming.

- VirtioFS shared folders must be present at VM boot time. Folders added after resume require a VM reboot.
