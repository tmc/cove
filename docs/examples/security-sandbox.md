---
title: Security Research Sandbox
---
# Security Research Sandbox

Use cove to run untrusted code in isolated, disposable macOS VMs. Snapshots provide instant rollback, disposable mode auto-deletes the VM on shutdown, and network controls limit what the guest can reach.

## 1. Create a Base VM for Analysis

Install a base macOS VM with the tools you need for analysis:

```bash
cove -vm sandbox install -cpu 2 -memory 4 -disk-size 32
sudo cove -vm sandbox provision -user analyst -password analyst -skip-setup-assistant
```

Boot, install analysis tools, then shut down:

```bash
cove -vm sandbox run -headless &
cove ctl -vm sandbox agent-ping -wait 120s
cove ctl -vm sandbox agent-exec --daemon bash -c \
  "su -l analyst -c 'brew install radare2 yara strings'"
cove ctl -vm sandbox agent-shutdown
```

Save a clean snapshot:

```bash
cove -vm sandbox disk-snapshot save clean
```

## 2. Disposable VMs

The `-disposable` flag creates a linked clone (APFS copy-on-write) of the VM that is deleted on shutdown. Any changes the malware makes are discarded automatically:

```bash
cove -vm sandbox run -disposable -gui
```

This boots a temporary copy of the `sandbox` VM. When you close the window or shut down the guest, the clone is removed. The base VM is untouched.

To clean up any orphaned disposable clones:

```bash
cove gc
cove gc -dry-run              # preview what would be deleted
cove gc -older-than 24h       # only delete clones older than 24 hours
```

## 3. Snapshot Before Executing Samples

For analysis that requires inspecting guest state after execution, use disk snapshots instead of disposable mode:

```bash
# Restore to clean state
cove -vm sandbox disk-snapshot restore clean

# Boot the VM
cove -vm sandbox run -gui -no-resume &

# Wait for agent
cove ctl -vm sandbox agent-ping -wait 60s

# Copy the sample into the guest
cove ctl -vm sandbox agent-cp ./suspicious-binary /tmp/sample

# Run it
cove ctl -vm sandbox agent-exec /tmp/sample &

# ... observe behavior, take screenshots ...
cove ctl -vm sandbox screenshot -o post-execution.png
```

When done, shut down and restore:

```bash
cove ctl -vm sandbox agent-shutdown
cove -vm sandbox disk-snapshot restore clean
```

## 4. Disable SIP for Deeper Access

Some analysis requires disabling System Integrity Protection to attach debuggers, load unsigned kexts, or inspect protected directories:

```bash
# Generate SIP disable automation
cove -vm sandbox sip disable-auto -user analyst -password analyst

# Boot into recovery with the automation script
cove -vm sandbox run -recovery -no-resume -gui -unattended \
  -usb ~/.vz/vms/sandbox/recovery-disk.img \
  -boot-commands ~/.vz/vms/sandbox/sip-disable.vzscript
```

After recovery reboots the VM, verify:

```bash
cove -vm sandbox run -gui &
cove ctl -vm sandbox agent-ping -wait 60s
cove sip status
```

Save a new snapshot with SIP disabled:

```bash
cove ctl -vm sandbox agent-shutdown
cove -vm sandbox disk-snapshot save clean-sip-off
```

## 5. Network Isolation

Control what network access the guest has:

```bash
# No network at all -- fully air-gapped
cove -vm sandbox run -network none -gui

# NAT with default settings (guest can reach the internet)
cove -vm sandbox run -network nat -gui

# Packet capture for traffic analysis (filehandle network mode)
cove -vm sandbox run -network filehandle -pcap /tmp/capture.pcap -gui
```

For NAT mode, the guest gets a private IP on a virtual network. It can reach the internet but the host controls the gateway. Combine with host-side firewall rules for fine-grained control.

## 6. Automated Analysis Script

A script that runs a sample in a disposable VM, captures a screenshot and network traffic, and tears everything down:

```bash
#!/bin/bash
set -euo pipefail

VM=sandbox
SAMPLE="$1"
OUTPUT_DIR="$2"
mkdir -p "$OUTPUT_DIR"

# Restore clean state
cove -vm "$VM" disk-snapshot restore clean

# Boot headless with packet capture
cove -vm "$VM" run -headless -no-resume -network filehandle \
  -pcap "$OUTPUT_DIR/traffic.pcap" &
VM_PID=$!

# Wait for agent
cove ctl -vm "$VM" agent-ping -wait 120s

# Copy sample in
cove ctl -vm "$VM" agent-cp "$SAMPLE" /tmp/sample

# Execute with timeout
timeout 60 cove ctl -vm "$VM" agent-exec /tmp/sample > "$OUTPUT_DIR/stdout.log" 2>&1 || true

# Collect evidence
cove ctl -vm "$VM" screenshot -o "$OUTPUT_DIR/screen.png"
cove ctl -vm "$VM" agent-exec ps aux > "$OUTPUT_DIR/processes.log" 2>&1 || true
cove ctl -vm "$VM" agent-exec --daemon \
  bash -c "ls -laR /tmp /var/tmp" > "$OUTPUT_DIR/filesystem.log" 2>&1 || true

# Shut down
cove ctl -vm "$VM" agent-shutdown
wait "$VM_PID" 2>/dev/null || true

echo "Analysis complete. Results in $OUTPUT_DIR/"
```

## Tips

- **Disposable mode** is best for quick checks where you do not need to inspect the guest afterward. Use disk snapshots when you need to examine the post-execution state.
- **`-no-resume`** ensures each analysis session cold-boots from a known state rather than resuming a prior session.
- **Agent exec with `--daemon`** runs commands as root, useful for reading protected files or installing kernel extensions (with SIP disabled).
- **PCAP capture** requires `-network filehandle` mode. The pcap file is written on the host and is available immediately after shutdown.
- **Templates** can distribute a pre-configured sandbox to a team: `cove template save sandbox-base`, then `cove template create sandbox-base my-sandbox` on each analyst's machine.
