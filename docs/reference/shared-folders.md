# Shared Folders

`cove shared-folder add` persists a host directory in the VM configuration.
It applies on the next VM boot, when cove can create the VirtioFS device before
Virtualization.framework starts the guest.

Live hot-add is not currently supported. If a VM was not booted with the shared
folder VirtioFS device, `shared-folder add` saves the config and reports that
the folder will mount on next boot. Reboot the VM to make the folder visible in
the guest.

## Commands

```bash
cove -vm my-vm shared-folder add ~/src src rw
cove -vm my-vm shared-folder list
cove -vm my-vm shared-folder status
cove -vm my-vm shared-folder pending
cove -vm my-vm shared-folder remove src
```

`pending [vm]` lists configured folders that are not currently visible in the
running guest mount. If the VM is not running, has no control socket, or was not
booted with the shared-folder VirtioFS device, all configured folders are
reported as pending.

The old `shared-folders-apply` control command is an internal reload hook for a
VM that already has the VirtioFS device. It does not attach a new VirtioFS
device to an already-running VM. True live hot-add is tracked as a future design.
