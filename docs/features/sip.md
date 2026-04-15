---
title: SIP Management
---
# SIP Management

Manage System Integrity Protection (SIP) for VMs with automated recovery boot.

> [!WARNING]
> Disabling SIP reduces guest security. Only disable when needed for development or testing.

## Commands

```bash
cove sip status              # query SIP status from running VM via agent
cove sip enable              # show instructions to enable SIP
cove sip disable             # show instructions to disable SIP
cove sip create-disk         # create (or recreate) the recovery tools disk
```

## Automated SIP Disable

Generate boot automation scripts and execute them in recovery mode:

```bash
# Generate the automation script
cove sip disable-auto -user admin -password secret -confirm

# Boot into recovery with the automation
cove run -recovery -no-resume -gui -unattended \
  -usb ~/.vz/vms/default/recovery-disk.img \
  -boot-commands ~/.vz/vms/default/sip-disable-commands.txt
```

The `-confirm` flag handles the "Are you sure" prompt that some Recovery builds show.

## Automated SIP Enable

```bash
cove sip enable-auto -user admin -password secret

cove run -recovery -no-resume -gui -unattended \
  -usb ~/.vz/vms/default/recovery-disk.img \
  -boot-commands ~/.vz/vms/default/sip-enable-commands.txt
```

## How It Works

1. `cove sip create-disk` creates a recovery tools disk image containing `csrutil-enable.sh` and `csrutil-disable.sh` scripts
2. `cove sip disable-auto` generates a vzscript boot automation file that navigates Recovery Terminal, types the csrutil command, and handles password prompts via OCR
3. The VM boots into recovery mode with the tools disk attached as USB storage
4. The boot automation script drives the recovery UI using OCR-based text detection

## Verify After Reboot

```bash
cove sip status
```

## Requirements

- The VM user must be created through the bootstrap-recovery provisioning flow for full recovery authorization
- The guest must have completed provisioning (check for `/var/db/.vz-provisioned` inside the VM)
