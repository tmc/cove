# Shared Folders Runbook

## Goal

Add a host folder from CLI, hot-apply to a running VM UI/session, and mount it in guest.

## Commands

List configured entries:

```sh
vz-macos -vm macos-3 vm shared-folder list
```

Status (config + control socket + agent + mount state):

```sh
vz-macos -vm macos-3 vm shared-folder status
```

Add and attempt hot-apply + guest mount:

```sh
vz-macos -vm macos-3 vm shared-folder add /Volumes/tmc
```

With explicit tag and mode:

```sh
vz-macos -vm macos-3 vm shared-folder add /Volumes/tmc tmc rw
```

Mount in guest (default `/Volumes/My Shared Files`):

```sh
vz-macos -vm macos-3 vm shared-folder mount
```

Remove and clear:

```sh
vz-macos -vm macos-3 vm shared-folder remove tmc
vz-macos -vm macos-3 vm shared-folder clear
```

## Guest Path

Folders are mounted via `_shared-folders` and appear under:

```text
/Volumes/My Shared Files/<tag>
```

Example:

```text
/Volumes/My Shared Files/tmc
```

## Troubleshooting

- `shared-folders-apply` unknown:
  - Running VM process is old; shares are saved and will apply on next boot.
- `guest agent unavailable`:
  - Agent isn’t running; mount step can’t execute yet.
- Folder not visible:
  - Confirm it exists in `shared_folders.json`, then run `vm shared-folder status` and `vm shared-folder mount`.
