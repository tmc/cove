---
title: Recovery & SIP
---
# Recovery & SIP

How to boot into recovery mode and manage System Integrity Protection.

## Recovery Boot

Boot a VM into macOS Recovery:

```bash
cove run -recovery -no-resume -gui
```

Recovery mode gives access to Terminal, Disk Utility, and the csrutil command for SIP management.

## SIP Disable (Automated)

### Step 1: Generate Automation

```bash
cove sip disable-auto -user admin -password secret -confirm
```

This creates:
- A recovery tools disk at `~/.vz/vms/<name>/recovery-disk.img`
- A boot automation script at `~/.vz/vms/<name>/sip-disable-commands.txt`

The `-confirm` flag handles the "Are you sure" confirmation prompt that some Recovery builds show.

### Step 2: Run Automation

```bash
cove run -recovery -no-resume -gui -unattended \
  -usb ~/.vz/vms/default/recovery-disk.img \
  -boot-commands ~/.vz/vms/default/sip-disable-commands.txt
```

The automation script:
1. Navigates to Utilities > Terminal in Recovery
2. Types the `csrutil disable` command
3. Handles password prompts via OCR text detection
4. Reboots the VM

### Step 3: Verify

After the VM reboots normally:

```bash
cove sip status
```

## SIP Enable (Automated)

```bash
cove sip enable-auto -user admin -password secret

cove run -recovery -no-resume -gui -unattended \
  -usb ~/.vz/vms/default/recovery-disk.img \
  -boot-commands ~/.vz/vms/default/sip-enable-commands.txt
```

## SIP Manual Instructions

If automation doesn't work, use the manual flow:

```bash
cove sip disable    # prints manual instructions
cove sip enable     # prints manual instructions
```

Manual steps:
1. Boot into recovery: `cove run -recovery -gui -usb <recovery-disk>`
2. Open Terminal from Utilities menu
3. Navigate to the recovery disk: `cd /Volumes/VZRECOVERY`
4. Run the script: `sh csrutil-disable.sh`
5. Reboot: `reboot`

## Recovery Tools Disk

Create or recreate the recovery tools disk:

```bash
cove sip create-disk
```

This creates an IMG file containing:
- `csrutil-enable.sh`
- `csrutil-disable.sh`

The disk is attached as USB storage when booting into recovery.

## Prompt Handling

The automation scripts handle password prompts using OCR conditions:

```
[text-visible:Authorized+user] type 'admin'
[text-visible:Password] type-keycodes 'secret'
[text-visible:Password] wait-prompt-clear 'Password'
```

This avoids hardcoding prompt order while keeping the syntax shell-like.

## Prerequisites

- The VM user must be created through the bootstrap-recovery provisioning flow for full recovery authorization
- The guest provisioning must have completed (check `/var/db/.vz-provisioned` inside VM)
- The guest should have run `diskutil apfs updatePreboot /`

## Troubleshooting

| Issue | Cause | Solution |
|-------|-------|---------|
| "Authentication failure" | User lacks recovery authorization | Reprovision with `-bootstrap-recovery` |
| "User interaction required" | Account state problem | Verify provisioning completed in guest |
| Terminal doesn't open | Menu click timing | Re-run the automation |
| Agent unavailable | Expected in Recovery | Agent doesn't run in Recovery mode |
