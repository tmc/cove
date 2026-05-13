---
title: Shared Folders
---
# Shared Folders

Mount host directories in the guest via VirtioFS.

## Usage

Mount directories at VM launch:

```bash
cove run -v ~/projects                     # mount as read-write
cove run -v ~/projects:myprojects          # custom tag name
cove run -v /data:data:ro                  # read-only
cove run -vol /host/path[:tag][:ro|rw]     # long form
```

Multiple volumes can be specified:

```bash
cove run -v ~/projects -v ~/data:data:ro
```

The deprecated `-share-dir` flag is equivalent to a single `-v` mount.

## Runtime Management

Manage shared folders for the active VM:

```bash
cove shared-folder list                    # list configured folders
cove shared-folder status                  # check mount status
cove shared-folder pending                 # list saved folders not mounted now
cove shared-folder add ~/newdir            # add a folder
cove shared-folder add ~/newdir mytag rw   # add with tag and mode
cove shared-folder remove mytag            # remove by tag or path
cove shared-folder clear                   # remove all folders
cove shared-folder mount                   # mount in guest via agent
```

## Persisted Configuration

`cove shared-folder add` persists the folder in the VM configuration and applies
it on the next boot. VirtioFS devices must be present when the VM starts; cove
does not currently live hot-add a new VirtioFS device to an already-running VM.
The `# mount:` directive in `cove vzscript run` uses the same persisted
configuration but additionally attempts best-effort hot-plug and guest mounting
for that script run; failure there is warning-only and still requires a future
boot for guaranteed availability.

Use `cove shared-folder pending` to see configured folders that are not mounted
in the running guest.

On macOS guests, cove mounts the aggregate VirtioFS share at
`/Volumes/My Shared Files/<tag>`. After a successful runtime mount, cove also
creates `~/<tag>` as a user-session symlink to that system mount path. The
symlink is created through the user agent, so a fresh desktop VM must complete
login before the link can be installed. If the user agent is connected but
macOS still refuses to enumerate the mount, run `cove doctor`; that is the
existing Full Disk Access diagnostic for non-system `/Volumes` mounts.

On Linux guests, VirtioFS mounts are explicit unless the guest agent is already
installed and healthy. Cove prints the manual command when the VM starts:

```bash
sudo mkdir -p /mnt/<tag>
sudo mount -t virtiofs <tag> /mnt/<tag>
```

If a desktop Linux VM has no agent yet, run that command in the guest terminal.
If the agent is present, use:

```bash
cove ctl -vm <vm> agent-mount-volumes
```

Linux guests may reject some VirtioFS cache options depending on the kernel and
desktop image. The simplest fallback is to mount without extra options first,
then add options one at a time.

When falling back to a tarball copy from macOS to Linux, disable AppleDouble
metadata so Linux extraction is quiet:

```bash
COPYFILE_DISABLE=1 tar -czf worktree.tgz -T files.txt
```

## VZScript Host Mounts

VZScript recipes can declare host directories:

```
# mount: ~/ml-explore rw
# mount: /data ro
```

These are registered as shared folders and mounted when the VM boots with the
corresponding VirtioFS device.

## Limitations

> [!WARNING]
> VirtioFS devices must be present at VM boot time. Folders added after suspend/resume require a VM reboot.
- TCC blocks `vz-agent` from accessing VirtioFS mounts as a daemon. The daemon lacks Full Disk Access. Cove routes path-aware `agent-exec` calls for `/Volumes/My Shared Files/...` through the user agent and uses `cove doctor` for FDA failures that remain after routing.
- Clipboard sync is separate from screenshot, OCR, keyboard, and mouse control.
  Check `cove ctl -vm <vm> capabilities` before assuming host-to-guest or
  guest-to-host clipboard support is available for a given VM.
