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
cove shared-folder add ~/newdir            # add a folder
cove shared-folder add ~/newdir mytag rw   # add with tag and mode
cove shared-folder remove mytag            # remove by tag or path
cove shared-folder clear                   # remove all folders
cove shared-folder mount                   # mount in guest via agent
```

## Hot-Add to Running VM

Apply shared folder changes to a running VM:

```bash
cove ctl shared-folders-apply
```

This reloads `shared_folders.json` into the running VM. The guest agent auto-mounts tagged volumes if `-auto-mount-volumes` is enabled (default: true).

## VZScript Host Mounts

VZScript recipes can declare host directories:

```
# mount: ~/ml-explore rw
# mount: /data ro
```

These are registered as shared folders and mounted automatically when the script runs.

## Limitations

> [!WARNING]
> VirtioFS devices must be present at VM boot time. Folders added after suspend/resume require a VM reboot.
- TCC blocks `vz-agent` from accessing VirtioFS mounts as a daemon. The agent lacks Full Disk Access. Users can `ls` from the GUI session, but `agent-exec` as daemon cannot traverse the mount.
