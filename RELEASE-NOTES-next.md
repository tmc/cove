# Cove release notes

Cove runs macOS and Linux VMs with Apple Virtualization on Apple Silicon.
Windows 11 ARM64 guests use QEMU/HVF and are in beta. The host requirement
is macOS 14 or later.

## VM images and builds

- Build from a local macOS or Linux VM directory or a macOS registry image
  with `cove build`. Builds can import and export step caches through OCI
  registries. macOS VM images can be pushed and pulled as bootable bundles.
  Keep the local VM directory to reuse a bootable Linux base.
- Use host environment variables for build secrets. The build mounts the
  declared secrets in memory inside the guest for the recipe step.

## Guest workflows

- `cove workspace` opens a prepared macOS or Linux guest for Go tasks, with
  read-only source access by default and a separate writable output directory.
- Windows guests support unattended installation, guest execution and file
  copy, screenshots, OCR, clipboard sharing, and a Cove display window.
  Suspend, snapshots, clone, fork, and the image store are unavailable for
  Windows guests.
- ScreenCaptureKit is available as an opt-in screenshot backend through
  `COVE_CAPTURE_BACKEND=sckit`. The default remains CGWindow capture.

## Host setup

- The optional privileged helper stores the authorized UID in
  `/usr/local/libexec/cove-helper.uid` so that authorization can survive
  a host reboot. See `cove helper status` for installation and connectivity
  diagnostics.
- `cove doctor qemu` checks host prerequisites for the Windows backend.
