# Copy files

Use `cove cp host-file VM:/guest/path` to upload or
`cove cp VM:/guest/path host-file` to download. Destination paths must be absent
unless `-f` is supplied. Files, directories, and broken symlinks count as existing
paths.

Transfers stage their output beside the destination before publication. Failed
transfers leave the destination unchanged. Cove removes staging data when the
guest agent remains reachable. For directory
copies, `-f` replaces the entire destination tree; it does not merge directories.
A concurrent destination creation prevents publication without `-f`.

Uploads require the guest agent's atomic publication helper. If Cove
reports that the helper is unavailable, run `cove agent-upgrade` for the selected
VM and retry. User-home copies require a logged-in guest user agent; Cove refuses
root fallback when that agent is unavailable.

Windows QEMU file uploads publish through the guest's file move or replace
operation. Directory uploads to Windows QEMU are not supported.

The CLI reports elapsed time and transferred bytes. Press Ctrl-C to cancel the
active copy. Cancellation stops the guest RPC and attempts to remove incomplete
staging output.

Drag files or folders from Finder onto a running VZ guest view to copy them as
the signed-in guest user. Each drop gets a unique `Downloads/Cove-…` directory.
The progress sheet's Cancel button stops the current transfer and remaining
files. Files completed earlier in the drop stay in that directory.
