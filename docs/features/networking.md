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

Examples:

```bash
cove run --net nat
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
