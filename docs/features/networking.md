# Networking

Cove exposes a small network policy surface for VM runs. The default is NAT,
which is the right choice for most local development and agent-sandbox runs.

## Modes

Use `--net` or `-network` with `cove run` and `cove up`.

| Mode | Behavior | Use when |
| --- | --- | --- |
| `nat` | Guest gets DHCP on a private virtual network and reaches outbound networks through the host. | Default local development and CI jobs that need internet access. |
| `bridged:<iface>` | Guest attaches to a host interface, for example `bridged:en0`, and appears on that network. | The guest must be reachable from the LAN or a specific test network. |
| `host-only` | Guest uses a private host/guest network. | CI jobs need host services without LAN exposure. |
| `none` | Cove attaches no virtual network device. | Air-gapped tests or malware/sandbox analysis. |

Advanced modes also exist:

| Mode | Behavior |
| --- | --- |
| `vmnet` | Uses the vmnet shared networking path when available on the host OS. |
| `filehandle` | Uses `VZFileHandleNetworkDeviceAttachment` for raw frame capture. Pair with `-pcap <path>`. |

## Named Policies

Named policies are accepted anywhere a network mode is accepted. They describe
the intended egress posture and are recorded in the run audit log.

| Policy | Effective mode | Intended egress |
| --- | --- | --- |
| `offline` | `none` | No network device. |
| `packages` | `nat` | Package registries only: Debian, Ubuntu, PyPI, npm, GitHub Container Registry, Docker Hub, and Fedora registry hosts. |
| `host-services` | `nat` | Package registries plus RFC1918 host/LAN ranges: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`. |
| `lan` | `nat` | RFC1918 LAN ranges only; no public internet by policy. |
| `open` | `nat` | Full egress. Equivalent to `nat`. |

Examples:

```bash
cove run --net offline
cove run --net packages
cove run --net host-services
cove run --net lan
cove run --net open
```

For policies other than `open`, Cove writes `network.log` under the run
artifact directory, for example `~/.vz/runs/<run-id>/network.log`.
Print it with:

```bash
cove network audit <run-id>
```

Current enforcement limits:

- `offline` is enforced by attaching no virtual network device.
- `packages`, `host-services`, and `lan` currently use the shipped
  Virtualization.framework NAT path. That path does not expose host-side
  per-connection allow/deny hooks, so `network.log` records the selected policy,
  allowlist, and limitation rather than a complete connection decision stream.
- `filehandle` mode exposes raw frames and can write a PCAP, but it is an
  advanced attachment mode rather than the default NAT path.

Examples:

```bash
cove run --net nat
cove run --net packages
cove run --net host-only
cove run --net bridged:en0
cove run --net none
cove run --net filehandle -pcap /tmp/cove.pcap
cove up --net nat -user me
```

List likely host interfaces for bridged mode:

```bash
cove network list
```

`bridged` by itself is not accepted. Use `bridged:<iface>` so CI jobs do not
silently bind to a different host interface.

## Sandbox Interaction

`-sandbox-level minimal` defaults to `--net none` unless you explicitly set a
network mode. That keeps disposable research runs offline by default while still
allowing a deliberate mode:

```bash
cove run -sandbox-level minimal --net nat
```

`-sandbox-level strict` forces `--net none` and rejects explicit networked modes.
It also disables vsock, so startup port forwards are rejected.

`-host-containment` is the fail-closed research mode. It is equivalent to
`-sandbox-level host-containment` and rejects host-escape features instead of
silently enabling them: shared folders, clipboard, agent auto-upgrade, startup
port forwards, VNC, debug stubs, host HTTP listeners, proxying, and explicit
networked modes.

Inspect the effective policy for an invocation with:

```bash
cove -host-containment security status
cove -host-containment security status -json
```

## Port Forwarding

Cove can forward host TCP listeners to guest vsock ports. This does not expose
guest TCP/IP ports; the guest service must be listening on the target vsock port.

Start forwards with the VM:

```bash
cove run --pf 8080:80
cove run --port-forward 8080:80 --port-forward 8443:443
```

The left side is the host TCP port on localhost. The right side is the guest
vsock port.

Manage forwards on a running VM:

```bash
cove ctl port-forward start 8080:80
cove ctl port-forward list
cove ctl port-forward stop 8080
```

Startup forwards bind to `localhost` only. They work with `--net none` because
they use vsock rather than guest IP networking.

## CI Defaults

For CI jobs that need package downloads, use NAT:

```bash
cove run -fork-from macos-ci:base -ephemeral --net nat
```

For jobs that should not reach the network but still need guest-agent control,
use minimal sandboxing or explicit `none`:

```bash
cove run -fork-from macos-ci:base -ephemeral -sandbox-level minimal
cove run -fork-from macos-ci:base -ephemeral --net none
```

For jobs that need host-only services:

```bash
cove run -fork-from macos-ci:base -ephemeral --net host-only
```
