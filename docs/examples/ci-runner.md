---
title: macOS CI Runner
---
# macOS CI Runner

Use cove to run macOS CI test jobs on a Mac mini. Each test run starts from a clean disk snapshot and executes in a headless VM, so failures never leave residue on the host or pollute the next run.

## Prerequisites

- A Mac mini (Apple Silicon) running macOS 14+
- cove installed and signed with virtualization entitlements
- A macOS IPSW restore image (cove downloads the latest automatically if omitted)

## 1. Create the Base VM

Install macOS into a VM named `ci-runner`, provision a user, and inject the guest agent:

```bash
cove -vm ci-runner install -ipsw ~/restore.ipsw -cpu 4 -memory 8 -disk-size 64
cove -vm ci-runner provision -user ci -skip-setup-assistant
```

Boot once to let provisioning complete, then shut down:

```bash
cove -vm ci-runner run -headless &
sleep 120  # wait for first-boot provisioning
cove ctl -vm ci-runner agent-shutdown
```

## 2. Install Build Tools

Start the VM and install the toolchain your tests need. You can use vzscripts for common tools:

```bash
cove -vm ci-runner run -headless &
cove vzscript run -vm ci-runner -v homebrew golang developer-tools
cove ctl -vm ci-runner agent-shutdown
```

Or run arbitrary commands via the guest agent:

```bash
cove ctl -vm ci-runner agent-exec --daemon xcode-select --install
```

## 3. Snapshot the Clean State

With the VM stopped, save an APFS disk snapshot. This is instant and copy-on-write, so it costs almost no extra disk space:

```bash
cove -vm ci-runner disk-snapshot save clean-base
```

Verify:

```bash
cove -vm ci-runner disk-snapshot list
```

## 4. Run a Test Job

Boot the VM headless, mount the test repo via a shared folder, run the tests, and collect results:

```bash
# Boot headless with the repo mounted
cove -vm ci-runner run -headless -v /path/to/repo:repo:ro &

# Wait for the agent to come up
cove ctl -vm ci-runner agent-ping -wait 60s

# Run the test suite inside the guest
cove ctl -vm ci-runner agent-exec bash -c \
  "cd /Volumes/repo && make test 2>&1" > test-output.log

# Copy artifacts out of the guest
cove ctl -vm ci-runner agent-cp -from-guest /tmp/test-results.xml ./results/

# Shut down the VM
cove ctl -vm ci-runner agent-shutdown
```

## 5. Restore Between Runs

After each job, restore the disk to the clean snapshot so the next run starts fresh:

```bash
cove -vm ci-runner disk-snapshot restore clean-base
```

This replaces the disk image with the snapshot copy. Because APFS clonefile is copy-on-write, the restore is instant.

## 6. Full CI Script

Putting it all together as a shell script that your CI scheduler (Jenkins, GitHub Actions self-hosted runner, etc.) calls for each job:

```bash
#!/bin/bash
set -euo pipefail

VM=ci-runner
REPO_PATH="$1"
RESULTS_DIR="$2"

# Restore to clean state
cove -vm "$VM" disk-snapshot restore clean-base

# Boot headless with repo mounted read-only
cove -vm "$VM" run -headless -no-resume -v "$REPO_PATH:repo:ro" &
VM_PID=$!

# Wait for the guest agent
cove ctl -vm "$VM" agent-ping -wait 120s

# Run tests
cove ctl -vm "$VM" agent-exec bash -c \
  "cd /Volumes/repo && make test" > "$RESULTS_DIR/output.log" 2>&1
TEST_EXIT=$?

# Collect artifacts
cove ctl -vm "$VM" agent-cp -from-guest /tmp/junit.xml "$RESULTS_DIR/" 2>/dev/null || true

# Shut down
cove ctl -vm "$VM" agent-shutdown
wait "$VM_PID" 2>/dev/null || true

exit $TEST_EXIT
```

## Tips

- **Headless mode** (`-headless`) uses no GPU resources and works over SSH to the host.
- **Shared folders** mount the test repo read-only so guest-side test failures cannot corrupt the source tree.
- **Disk snapshots** are faster than VM state snapshots for CI because you only need clean disk state, not a running VM checkpoint.
- **Multiple VMs**: Create several base VMs (`ci-runner-1`, `ci-runner-2`, ...) to run jobs in parallel on the same host.
- **`-no-resume`** ensures each boot starts cold rather than resuming a suspended state from a previous run.
- **Agent exec with `--daemon`** runs commands as root, useful for `xcode-select --install` or package installation.

## See also

- [Push & Pull](../getting-started/push-pull.md) -- push the prebuilt runner image to an OCI registry once, then `cove pull` on every CI host instead of reinstalling.
- [Control Socket API](../reference/control-api.md) -- programmatic control if you need finer-grained orchestration than the CLI.
