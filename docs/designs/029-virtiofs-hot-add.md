# Design 029: VirtioFS Hot-Add for Shared Folders

Status: Shipped. Live-apply through the pre-existing shared-folders
VirtioFS device works end-to-end on macOS and Linux guests; true device
hot-add is still gated on Apple shipping a public attach API.
Author: Travis Cline
Date: 2026-05-04

## Status

| Slice | State | SHA |
|-------|-------|-----|
| Runtime preflight (`shared-folders-runtime-status`) | shipped | `16853bb` |
| Host-side live apply (`shared-folders-apply`) | shipped | `fdda2ab` |
| Persist-only fallback message | shipped | `f6553e5`, `5d62b0c` |
| `shared-folder pending` CLI | shipped | `39d0916` |
| Guest remount after apply (macOS + Linux) | shipped | `c5a3c67`, `11b54c5` |
| Per-tag Linux mount paths + bind entries | shipped | `27e52f2`, `0a13e8b`, `4caaffb`, `3f26e4d` |
| Guest home symlinks for tags | shipped | `56c531d`, `1500f54`, `b63712f` |
| Linux VirtioFS default `cache=none` | shipped | `e202836` |
| Read-only tag + sanitization | shipped | `c535502` |
| True VirtioFS device hot-add | not pursued | depends on Apple public API |

Resolved open questions:

- *Auto-mount after apply.* Yes — control command updates the host share
  and the CLI/agent path remounts on the guest (`c5a3c67`, `11b54c5`).
- *`shared-folder pending` exists.* Shipped at `39d0916`.

## Problem

`cove shared-folder add` now tells the truth: it persists the folder and applies
on next boot unless the running VM was already booted with the shared-folders
VirtioFS device. That is the right UX for the current implementation, but it is
not the feature users expect from the words "add shared folder" in a long-lived
VM.

There are two different operations that are easy to conflate:

1. **Changing the contents of an existing VirtioFS device.** Cove can already do
   this for the dedicated shared-folders device by replacing the runtime
   `VZVirtioFileSystemDevice.share`.
2. **Adding a new VirtioFS device to a running VM.** This is true device
   hot-add. It would be needed when the VM did not boot with a directory-sharing
   device, such as a minimal runtime profile or an older saved state whose
   directory-sharing device count does not match current cove.

Apple's public `VZVirtualMachine` surface exposes runtime collections such as
`directorySharingDevices`, `networkDevices`, `socketDevices`, and
`usbControllers`, but it does not expose `addDirectorySharingDevice`,
`attachDirectorySharingDevice`, or an equivalent `attachStorageDevice` method for
VirtioFS. The public VirtioFS documentation says a
`VZVirtioFileSystemDeviceConfiguration` is created in the
`VZVirtualMachineConfiguration`, and the resulting device appears in
`VZVirtualMachine.directorySharingDevices`. That points to boot-time
configuration, not runtime device insertion.[^apple-vm][^apple-virtiofs]

Code-Hex/vz reflects the same split. Its package surface has
`VirtualMachineConfiguration.SetDirectorySharingDevicesVirtualMachineConfiguration`
and `VirtioFileSystemDeviceConfiguration.SetDirectoryShare`, while runtime
hot-plug methods are present for USB through `USBController.Attach` and
`USBController.Detach`. There is no corresponding runtime `VirtualMachine`
method for adding a directory-sharing device.[^codehex-vz]

## Proposal

Do not chase private APIs for VirtioFS device hot-add. Treat true
device-level hot-add as unavailable until Apple ships a public API.

Instead, make cove's supported live behavior explicit:

1. Full-profile macOS VMs boot with one dedicated shared-folders VirtioFS device
   tagged by `SharedFoldersVirtioFSTag`, even when `shared_folders.json` is
   empty.
2. `cove shared-folder add` persists the new entry, asks the control socket for
   `shared-folders-runtime-status`, and if the dedicated device exists, calls
   `shared-folders-apply`.
3. `shared-folders-apply` reloads `shared_folders.json`, rebuilds the
   `VZMultipleDirectoryShare`, and assigns it to the existing
   `VZVirtioFileSystemDevice.share`.
4. After host-side apply succeeds, cove asks the guest agent to remount the
   aggregate shared-folders mount:

   - macOS guest: `mount_virtiofs <tag> <mountPoint>`
   - Linux guest: `mount -t virtiofs -o cache=none,uid=<uid>,gid=<gid> <tag> <mountPoint>`

5. If the running VM has no shared-folders VirtioFS device, return the existing
   persist-only message and list the exact recovery path: reboot the VM with the
   full runtime profile, or cold-boot with `cove run -no-resume` when a saved
   state has the old device shape.

This is "live folder apply through a pre-existing VirtioFS device," not true
VirtioFS device hot-add. The CLI and docs should use that language.

## Runtime Changes

The smallest runtime change is in the existing control path, not in VM
construction.

`control_socket.go` should keep `shared-folders-runtime-status` and
`shared-folders-apply` as control commands. The status response should continue
to report whether a live VM is connected, whether the shared-folders VirtioFS
device exists, the VM state, and an actionable message. That gives the CLI a
cheap preflight before promising live behavior.

`shared_folders_runtime.go` should remain the host-side applier. It already
dispatches onto the VM queue, requires the VM to be running or paused, searches
`vm.DirectorySharingDevices()` for the dedicated VirtioFS tag, and calls
`device.SetShare(...)`. The design-level cleanup is to make the result explicit:

```text
applied: host share updated on existing device
not-live: running VM has no shared-folders VirtioFS device
not-running: config persisted only
```

The guest remount should stay in the CLI/agent path, because the host-side
Virtualization framework operation only changes what the device exposes. The
guest still needs its mount table refreshed so the user sees the new tag under
the configured shared-folder mount point. This matches the existing cove mount
helpers: Linux uses `mount -t virtiofs`, while macOS uses `mount_virtiofs`.

Do not attempt to append to `VZVirtualMachineConfiguration` after the VM has
started. The configuration object is the construction input; changing it after
`VZVirtualMachine` creation is not a runtime device insertion API.

## UX

Keep persist-only as the default message unless a live preflight proves the VM
can apply the folder now.

Recommended behavior:

```text
cove shared-folder add ~/src src rw
shared folder saved; applying to running VM ...
applied to running VM; mounted at /Volumes/cove-shared/src
```

Fallback:

```text
cove shared-folder add ~/src src rw
shared folder saved; will mount on next boot of dev
running VM was not booted with the shared-folders VirtioFS device; live apply is not possible
```

Do not add `-live` or `-hot` as the primary path. The command should be live
when cove can prove live apply is possible, and honest when it cannot. A future
`-no-live` flag could be useful for scripts that only want to update
`shared_folders.json`, but `-live` would make the common case harder while still
not solving true device hot-add.

For minimal-profile VMs, keep the current limitation. The minimal profile's
purpose is a smaller device surface; adding a permanent shared-folders device to
that profile would change its contract and saved-state fingerprint.

## Risks

- **Misleading terminology.** Calling this "hot-add" implies runtime VirtioFS
  device insertion. The shipped feature should say "live apply" unless Apple
  adds a public directory-sharing attach API.
- **Saved-state compatibility.** Cove already fingerprints the directory-sharing
  device count. A VM restored from a state without the dedicated shared-folders
  device cannot be made live by changing host config; it needs a cold boot with
  the new device shape.
- **Guest mount staleness.** Updating `VZVirtioFileSystemDevice.share` is not
  enough if the guest already mounted the aggregate tag. Cove must remount or
  verify the mount before reporting success.
- **TCC and daemon-agent limits.** Host files are accessed as the effective host
  user through Virtualization, and cove's daemon agent can still hit TCC limits
  when traversing mounts. Live apply does not change that security model.
- **Linux cache coherency.** Linux guests should keep the existing
  `cache=none` default for shared folders so host-side changes are visible
  immediately.

## Open Questions

- Should minimal profile gain an explicit opt-in such as
  `-runtime-profile minimal -shared-folders-device`, or should users switch to
  the full profile when they want live shared folders?
- Should `shared-folder pending` distinguish "pending because no runtime device"
  from "pending because guest remount failed"?
- Should cove expose `shared-folders-runtime-status` in user-facing JSON for
  scripts that need to decide whether to reboot?

## References

- Existing cove docs: `docs/features/shared-folders.md` says
  `shared-folder add` persists config and that VirtioFS devices must be present
  when the VM starts.
- Existing cove troubleshooting: `docs/guides/troubleshooting.md` documents the
  post-resume mount failure and the `cove run -no-resume` recovery path.
- Existing cove runtime: `macos.go` creates a dedicated shared-folders
  `VZVirtioFileSystemDeviceConfiguration` in the full runtime profile, and
  `shared_folders_runtime.go` applies changes by replacing the existing
  `VZVirtioFileSystemDevice.share`.

[^apple-vm]: Apple Developer Documentation, `VZVirtualMachine`, lists runtime
    device properties including `directorySharingDevices` and `usbControllers`,
    but no directory-sharing attach method:
    https://developer.apple.com/documentation/virtualization/vzvirtualmachine
[^apple-virtiofs]: Apple Developer Documentation,
    `VZVirtioFileSystemDevice`, says the device is created by instantiating a
    `VZVirtioFileSystemDeviceConfiguration` in a `VZVirtualMachineConfiguration`
    and then appears in `VZVirtualMachine.directorySharingDevices`:
    https://developer.apple.com/documentation/virtualization/vzvirtiofilesystemdevice
[^codehex-vz]: Code-Hex/vz v3 package documentation lists
    `SetDirectorySharingDevicesVirtualMachineConfiguration` and
    `VirtioFileSystemDeviceConfiguration.SetDirectoryShare`; runtime attach
    methods are present for USB controllers, not directory-sharing devices:
    https://pkg.go.dev/github.com/Code-Hex/vz/v3
