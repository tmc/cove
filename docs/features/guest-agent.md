---
title: Guest Agent
---
# Guest Agent

A vsock gRPC agent injected into the guest at install time. Execute commands, transfer files, manage proxy settings, and share the clipboard -- all without SSH.

## How It Works

The guest agent (`vz-agent`) is a Go binary cross-compiled for the guest OS and injected as a LaunchDaemon during provisioning. It communicates with the host over virtio-socket (vsock), which requires no network configuration.

Two agent instances run inside the guest:
- **Daemon agent** (root, port 1024): system-level operations
- **User agent** (login session): user-level operations with TCC/FDA access

## Run Commands

Use top-level `cove exec` for the Docker-shaped path:

```bash
cove exec ubuntu uname -a
cove exec -it ubuntu bash
cove exec -e CI=1 -w /work ubuntu go test ./...
```

`cove shell <vm>` is a shortcut for an interactive login shell in a running VM.

## Agent Commands via ctl

`cove ctl` exposes the lower-level control-socket commands. Prefer these for
automation that already works directly with the control socket.

```bash
cove ctl agent-ping                       # check connectivity
cove ctl agent-info                       # guest OS, hostname, arch
cove ctl exec ls /tmp                     # run command (auto-routed by path)
cove ctl agent-exec --daemon whoami       # run as root
cove ctl agent-exec-stream make build     # stream output live
cove ctl agent-read /etc/hosts            # read guest file
cove ctl agent-write /tmp/foo "hello"     # write to guest file
cove ctl agent-cp ~/file.txt /tmp/        # copy host to guest
cove ctl agent-cp -from-guest /tmp/f ./   # copy guest to host
cove ctl agent-shutdown                   # graceful shutdown
cove ctl agent-reboot                     # reboot guest
cove ctl agent-sshd on                    # enable SSH
cove ctl agent-status                     # daemon + user health
```

## Clipboard Sharing

Host-guest clipboard sharing via the SPICE agent protocol:

```bash
cove run -clipboard          # enabled by default
cove run -clipboard=false    # disable
```

Requires `spice-vdagent` in the guest. macOS 15+ guests have native support.

## Proxy Configuration

Configure guest HTTP/HTTPS proxy settings after boot:

```bash
cove run -proxy http://192.168.64.1:8080
```

- Linux: writes `/etc/environment.d/99-cove-proxy.conf`
- macOS: configures via `networksetup` through the user agent
- Proxy state is cleaned up on graceful shutdown

## Agent Injection

The agent is injected automatically during `cove provision` or `cove up`. To inject only the agent (no user provisioning):

```bash
cove provision-agent
```

To upgrade the agent in an existing VM:

```bash
cove agent-upgrade
```

Or automatically on each boot:

```bash
cove run -auto-upgrade-agent
```

## Limitations

- The user agent requires a logged-in GUI session to access TCC-protected resources
- VirtioFS mounts accessed via the daemon agent may be blocked by TCC (Full Disk Access required)
