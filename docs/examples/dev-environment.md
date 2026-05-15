---
title: Reproducible Dev Environment
---
# Reproducible Dev Environment

Define a complete macOS development environment as code using vzscripts and shared folders. Suspend at the end of the day, resume instantly the next morning.

## 1. One-Command Setup

The `up` command installs macOS, provisions a user, and runs vzscripts in sequence:

```bash
cove up -user dev -vzscripts homebrew,golang,claude-code -cpu 4 -memory 8
```

This does the following in order:

1. Downloads and installs macOS (or uses a cached IPSW)
2. Provisions the `dev` user with auto-login and guest agent
3. Boots the VM
4. Runs the `homebrew`, `golang`, and `claude-code` vzscripts with dependency resolution

When the scripts finish, you have a fully configured VM at the desktop.

## 2. Write a Custom VZScript

Built-in recipes cover common tools. For project-specific setup, write a custom `.vzscript` file. Scripts are txtar archives: the comment section contains commands, and embedded files are extracted to a working directory.

Create `workstation.vzscript`:

```
# requires: homebrew, golang
# runs-on: daemon

# Wait for the guest agent
guest-wait 3m

# Install project dependencies
guest-shell install-tools.sh

-- install-tools.sh --
#!/bin/bash
set -euo pipefail

# Homebrew packages
su -l dev -c 'brew install protobuf grpcurl jq ripgrep fd bat'

# Go tools
su -l dev -c 'go install google.golang.org/protobuf/cmd/protoc-gen-go@latest'
su -l dev -c 'go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest'

# Configure git
su -l dev -c 'git config --global user.name "Dev User"'
su -l dev -c 'git config --global user.email "dev@example.com"'
```

Run it against a running VM:

```bash
cove vzscript run ./workstation.vzscript
```

Or include it in the initial setup:

```bash
cove up -user dev -vzscripts homebrew,golang,./workstation.vzscript
```

## 3. Shared Folders for Project Code

Mount your host project directories into the guest so you can edit on the host and build in the VM:

```bash
cove run -v ~/projects:projects -v ~/go/src:gosrc:ro
```

Inside the guest, these appear as VirtioFS mounts under `/Volumes/projects` and `/Volumes/gosrc`.

For persistent shared folder configuration that survives restarts:

```bash
cove shared-folder add ~/projects projects rw
cove shared-folder add ~/go/src gosrc ro
cove shared-folder list
```

These are stored in `~/.vz/vms/<name>/shared_folders.json` and mounted automatically on each boot when `-auto-mount-volumes` is enabled (the default).

## 4. Daily Suspend and Resume

At the end of the day, close the VM window or press Ctrl+C. Cove saves the full VM state (CPU, memory, devices) to disk:

```bash
cove run    # resumes from yesterday's saved state -- no boot, no login
```

Resume is instant. You pick up exactly where you left off, with all applications and terminal sessions intact.

To force a fresh boot (e.g., after a host reboot):

```bash
cove run -no-resume
```

## 5. Snapshot Before Risky Changes

Before a major OS update or experimental package install, save a disk snapshot:

```bash
cove ctl agent-shutdown             # stop the VM cleanly
cove disk-snapshot save pre-update
```

If the update breaks something:

```bash
cove disk-snapshot restore pre-update
cove run
```

The restore is instant thanks to APFS copy-on-write.

## 6. Template for Team Distribution

Once your environment is dialed in, save it as a template so teammates can spin up identical VMs:

```bash
cove ctl agent-shutdown
cove template save dev-env-2024q4
```

A colleague creates their own VM from the template:

```bash
cove template list
cove template create dev-env-2024q4 my-dev
cove -vm my-dev run
```

## Tips

- **VirtioFS limitations**: shared folders must be present at boot time. If you add a folder to a resumed VM, reboot the guest for it to appear.
- **CPU and memory**: match the VM resources to your workload. 4 CPUs and 8 GB RAM is a reasonable starting point for Go/Rust builds.
- **Verbose vzscript output**: use `cove vzscript run -v` to see command output as scripts execute.
- **Dependency resolution**: vzscripts with `# requires:` headers are resolved automatically. Each recipe runs at most once, even when required by multiple scripts.
