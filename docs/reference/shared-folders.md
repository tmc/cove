---
title: Shared Folders
description: cove shared-folder add persists a host directory in the VM configuration.
icon: book
---
# Shared Folders

`cove shared-folder add` saves a host directory and applies the updated share
while the VM is running, if its shared-folder VirtioFS device is present.
Changing a folder's access mode also uses this live-apply path.

A VM without that device must restart to pick it up. Cove cannot attach a
new VirtioFS device to an already-running VM.

## Commands

```bash
cove -vm my-vm shared-folder add ~/src src rw
cove -vm my-vm shared-folder list
cove -vm my-vm shared-folder status
cove -vm my-vm shared-folder pending
cove -vm my-vm shared-folder mode src ro
cove -vm my-vm shared-folder mode src rw
cove -vm my-vm shared-folder remove src
```

`pending [vm]` lists configured folders that are not currently visible in the
running guest mount. If the VM is not running, has no control socket, or was not
booted with the shared-folder VirtioFS device, all configured folders are
reported as pending.

`shared-folder mode <tag-or-path> ro|rw` saves the access mode and reloads the
running share. With a stopped VM, the mode applies on its next boot. If live
apply fails, the command returns an error; the saved choice remains in effect
for the next boot. Read/write access still respects host file permissions.

The Shared Folders menu exposes the same choices under each folder. A
checkmark identifies its saved mode. `shared-folders-apply` is the control
command that reloads the folders on an existing VirtioFS device.
