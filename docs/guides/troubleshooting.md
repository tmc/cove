---
title: Troubleshooting
---
# Troubleshooting

## Installation Issues

### DFU State Error (Code 4014 / 3004)

```
Domain: com.apple.MobileDevice.MobileRestore
Code: 4014 or 3004
Description: Unexpected device state 'DFU' expected 'RestoreOS'
```

**Primary cause:** missing audio device stream configuration. This is handled automatically in current versions but may occur with modified VM configurations.

**Solutions:**
1. Clean VM directory before installation: `rm -rf ~/.vz/vms/default`
2. If it persists, reboot the host machine to reset MobileDevice XPC services

### Sandbox Preferences Error

```
accessing these preferences requires user-preference-read or file-read-data sandbox access
```

**Solution:** sign the binary with virtualization entitlements:

```bash
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

This should happen automatically on first launch. If it doesn't, run the codesign command manually.

## Provisioning Issues

### User Not Created on Boot

**Cause:** LaunchDaemon plist not owned by root:wheel.

**Solution:** Re-run provision with sudo:

```bash
sudo cove provision -user testuser -password secret -skip-setup-assistant
```

Verify with:

```bash
cove doctor
```

### Setup Assistant Still Appears

**Cause:** `.AppleSetupDone` not created or wrong ownership.

**Solution:** Use `-skip-setup-assistant` flag and ensure sudo:

```bash
sudo cove provision -user testuser -skip-setup-assistant
```

### "Resource temporarily unavailable"

**Cause:** VM disk is currently attached (VM running or hdiutil left it attached).

**Solution:**

```bash
cove disk-detach            # force-detach the disk image
```

### Check Provisioning Log Inside VM

If provisioning appears to run but the user isn't created:

```bash
cove ctl agent-exec cat /var/log/vz-provision.log
```

## Runtime Issues

### VM Won't Resume

**Cause:** suspend state doesn't match current configuration (e.g., changed CPU count).

**Solution:** cold boot:

```bash
cove run -no-resume
```

Or delete the suspend state:

```bash
rm ~/.vz/vms/default/suspend.vmstate
```

### Shared Folders Not Accessible After Resume

**Cause:** VirtioFS devices must be present at VM boot time.

**Solution:** reboot the VM:

```bash
cove ctl request-stop
cove run -no-resume
```

### Guest Agent Not Responding

Check agent status:

```bash
cove ctl agent-status
```

If the agent is missing, inject it:

```bash
sudo cove provision-agent
```

Or upgrade an existing agent:

```bash
cove agent-upgrade
```

### TCC Blocks Agent Access to VirtioFS Mounts

The daemon agent lacks Full Disk Access. Files on VirtioFS mounts can be accessed by the logged-in user but not by `agent-exec --daemon`.

**Workaround:** use the user agent (default) or access files through SSH.

## Network Issues

### No Network in Guest

**Cause:** DHCP timeout or network mode misconfiguration.

**Solutions:**
- Check network mode: `cove ctl network-info`
- Try explicit NAT: `cove run -network nat`
- For bridged: ensure the host interface is correct: `cove run -network bridged:en0`

### Proxy Not Applied

**Preflight checks that block `-proxy`:**
- `-sandbox-level strict` rejects proxy
- `-network none` rejects proxy
- macOS `-runtime-profile minimal` rejects proxy

## Display Issues

### Black Screen

**Cause:** VM hasn't finished booting, or Virtio GPU drivers not loaded (Linux).

**Solution:** wait for boot to complete. For Linux VMs, use serial console to check boot progress:

```bash
cove run -linux -serial stdout
```

### Window Position Lost

Window frame is saved per-VM. If the saved position is off-screen (e.g., external display disconnected):

Delete the saved frame and restart. The frame autosave is handled by NSWindow and stored in macOS user defaults.

## Debugging

### Verbose Output

```bash
cove run -verbose
VZ_DEBUG_INSTALL=1 cove install -ipsw restore.ipsw
VZ_DEBUG_INJECT=1 cove provision -user testuser -v
```

### Apple Unified Logs

Stream virtualization-related logs:

```bash
cove run -apple-log
cove run -apple-log -apple-log-predicate "subsystem == 'com.apple.Virtualization'"
```

### OCR Debug

Save screenshots with OCR bounding boxes:

```bash
cove run -debug-ocr
```

### pprof Diagnostics

```bash
cove run -pprof 6060
# Then: go tool pprof http://localhost:6060/debug/pprof/heap
```

### Control Socket Debug

```bash
TOKEN=$(cat ~/.vz/vms/default/control.token)
echo '{"type":"status","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
echo '{"type":"capabilities","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```
