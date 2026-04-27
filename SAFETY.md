# cove Safety Posture

cove is a local VM tool. It wraps Apple's Virtualization.framework, stores VM state on your Mac, and exposes local control surfaces for automation. This document describes the trust boundary cove intends to keep.

## What cove does on your machine

- Stores VMs under `~/.vz/vms/<name>/` by default.
- Downloads Apple restore images only when you ask it to install macOS.
- Pulls and pushes VM images only against registries you name.
- Runs a guest agent inside the VM over vsock.
- Uses local Unix sockets and bearer tokens for host-side control.
- Uses root only for operations that macOS requires to be root-owned, such as installing LaunchDaemon files into a mounted guest disk.

cove does not include telemetry or a cloud relay.

## Guardrails

### 1. Host-bounded control plane

The in-guest agent talks to the host over vsock. That channel is scoped to the host and guest VM relationship; it is not an externally routable IP listener.

### 2. Strict local control socket

Each VM control socket is a Unix domain socket with owner-only permissions. Each VM also has a `control.token` file written with `0600` permissions. The HTTP gateway uses a gateway token, and strict per-VM auth is available with `cove serve -per-vm-auth`.

### 3. Explicit privilege escalation

cove runs as the invoking user by default. Disk injection may need root because macOS launchd ignores LaunchDaemon plists that are not owned by `root:wheel`. The privileged helper is opt-in and can be inspected with `cove helper status`; it is not required for normal VM runtime control.

### 4. No telemetry

cove has no usage telemetry, crash-report upload, analytics endpoint, or auto-update beacon. Expected outbound traffic is limited to Apple restore-image downloads and registries or network endpoints the user configures.

### 5. Same-PR safety review for new trust boundaries

Any change that adds a privileged operation, network egress path, host-guest file crossing, token format, helper daemon behavior, or telemetry-adjacent reporting path must update this document in the same change.

## Known Limitations

### TCC and VirtioFS

macOS TCC can block background agents from traversing protected user paths and VirtioFS mounts, even as root. v0.1.1 documents the current state: routing through the user agent is scaffolding, but VirtioFS `readdir` still requires a manual Full Disk Access grant for the in-guest agent binary. See `docs/research/tcc-via-user-agent.md`.

### Apple virtualization license limits

cove is MIT-licensed, but macOS guests are governed by Apple's macOS Software License Agreement. The standard SLA language permits two additional virtualized macOS instances per Apple-branded host for the listed purposes. cove does not bypass that limit. See `INSTALL.md`.

### Apple platform behavior can change

cove depends on public Virtualization.framework APIs and on macOS provisioning behavior such as Setup Assistant skipping, auto-login files, and LaunchDaemon startup. Apple OS releases can change those behaviors.

### Soft reset is not a hard isolation boundary

Deleting and recreating users inside a warm macOS guest is not equivalent to a fresh VM. TCC, System Keychain state, Apple Account limits, GlobalPreferences, FileVault SecureToken propagation, and orphaned LaunchDaemons can outlive a user account. Privacy-critical evals should use VM fork or restore, not UID recycling.

### Shared folders expose host paths you choose

VirtioFS shares make selected host paths visible to the guest. Treat a shared folder as a deliberate trust-boundary crossing, especially when running untrusted code in the guest.

### The helper daemon is privileged

If installed, `cove-helper` runs as root and accepts typed manifests over `/var/run/cove-helper.sock`. It checks the peer UID against the installing user and logs to `/var/log/cove-helper.log`. Keep it current with `sudo cove helper install` after upgrading cove.

## How to Audit cove

1. Inspect VM-local tokens and sockets:

   ```bash
   ls -l ~/.vz/vms/*/control.sock ~/.vz/vms/*/control.token
   ```

2. Inspect the HTTP gateway token if you use `cove serve`:

   ```bash
   ls -l ~/.vz/gateway.token
   ```

3. Check the helper state before relying on it:

   ```bash
   cove helper status
   ```

4. Review helper logs if the helper is installed:

   ```bash
   sudo tail -100 /var/log/cove-helper.log
   ```

5. Verify network behavior with your own tools. A fresh install should contact Apple's restore-image endpoints; OCI operations should contact only the registry you name.

6. Review the relevant code paths:

   - `control_socket.go` for local control and token handling.
   - `serve.go`, `serve_gateway.go`, and `gateway_token.go` for HTTP gateway auth.
   - `agent_control.go` and `agent_routing.go` for daemon vs user-agent routing.
   - `helper.go` and `elevated_exec.go` for host privilege escalation.
   - `provision_*.go` and `agent_inject.go` for guest disk injection.

## Reporting Security Issues

Use GitHub security advisories for private reports. Include the cove version or commit, host macOS version, guest macOS version, command line, and whether the privileged helper was installed.
