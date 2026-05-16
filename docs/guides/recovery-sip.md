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
cove sip disable-auto -user admin -password <password>
```

This creates:
- A recovery tools disk at `~/.vz/vms/<name>/recovery-disk.img`
- A boot automation script at `~/.vz/vms/<name>/sip-disable.vzscript`

The script answers Recovery confirmation prompts when they appear.

### Step 2: Run Automation

```bash
cove run -recovery -no-resume -gui -unattended \
  -usb ~/.vz/vms/default/recovery-disk.img \
  -boot-commands ~/.vz/vms/default/sip-disable.vzscript
```

The automation script:
1. Selects Recovery Options, continues through the Recovery screen, and opens Utilities > Terminal
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
cove sip enable-auto -user admin -password <password>

cove run -recovery -no-resume -gui -unattended \
  -usb ~/.vz/vms/default/recovery-disk.img \
  -boot-commands ~/.vz/vms/default/sip-enable.vzscript
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
[text-visible:Password] type-keycodes '<admin-password>'
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
| Terminal doesn't open | Recovery menu not ready | Re-run the automation with `-gui` and keep the window focused |
| Agent unavailable | Expected in Recovery | Agent doesn't run in Recovery mode |
