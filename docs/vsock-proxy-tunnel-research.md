# Vsock Proxy Tunnel: Network Egress for macOS VMs

Research findings for nanoclaw VM network integration.
Investigated 2026-02-16 against the appledocs/vmkit/vz-macos codebase.

## Executive Summary

The "vsock proxy tunnel" approach — no NAT device, filtered egress via a second vsock port — is **fully supported** by the existing codebase. All required primitives (guest AF_VSOCK sockets, multi-port listeners, disk injection, bidirectional relay) already exist and are proven in production use by vz-agent and vz-container.

---

## 1. Vsock Guest-Side Support

**macOS guests have native AF_VSOCK support. No kext or driver needed.**

The kernel exposes `AF_VSOCK = 40` for userspace socket creation. The existing `vz-agent` binary already uses this to listen on port 1024 inside macOS guests.

### Implementation (existing code)

`examples/vz-macos/cmd/vz-agent/vsock_darwin.go:14-72`:

```go
fd, err := syscall.Socket(AF_VSOCK, syscall.SOCK_STREAM, 0)

// macOS sockaddr_vm: 12 bytes (BSD-style with svm_len prefix)
sa := [12]byte{}
sa[0] = 12        // svm_len — UNIQUE TO macOS/BSD
sa[1] = AF_VSOCK  // svm_family (40)
// bytes 2-3: svm_reserved (zero)
// bytes 4-7: svm_port (little-endian)
// bytes 8-11: svm_cid (VMADDR_CID_ANY = 0xFFFFFFFF)
syscall.RawSyscall(syscall.SYS_BIND, uintptr(fd),
    uintptr(unsafe.Pointer(&sa[0])), 12)
```

### Platform differences

| Platform | sockaddr_vm size | Layout |
|----------|-----------------|--------|
| macOS (BSD) | 12 bytes | `svm_len(1) + svm_family(1) + reserved(2) + port(4) + cid(4)` |
| Linux | 16 bytes | `svm_family(2) + reserved(2) + port(4) + cid(4) + zero(4)` |

There is no `/dev/vsock` character device on macOS. Use `syscall.Socket(AF_VSOCK, SOCK_STREAM, 0)` directly.

To **connect** from guest to host (CID 2), the guest-side bridge binary would fill `svm_cid = 2` and `svm_port = 2223` then call `connect()`.

### Relevant files

- `examples/vz-macos/cmd/vz-agent/vsock_darwin.go` — macOS guest listen implementation
- `examples/vz-macos/cmd/vz-agent/vsock_linux.go` — Linux variant (for comparison)
- `examples/vz-macos/cmd/vz-agent/main.go:25-46` — GRPC server on vsock port 1024

---

## 2. Socat Vsock on macOS

**macOS does not ship socat.** It is not in `/usr/bin/`.

Homebrew socat 1.8.1+ supports `VSOCK-CONNECT` and `VSOCK-LISTEN`:

```
socat VSOCK-CONNECT:<cid>:<port>  -
socat VSOCK-LISTEN:<port>         -
```

However, relying on socat inside the guest VM means either:
- Pre-installing it (requires network or manual setup)
- Injecting a Homebrew install (fragile)

### Recommendation: static Go binary

A purpose-built Go binary (~100 lines) is the better path. The codebase already has the exact relay pattern in `vz-container/cmd/vminitd/vsock_proxy.go`:

```go
func relay(a, b net.Conn) {
    var wg sync.WaitGroup
    wg.Add(2)
    go func() { defer wg.Done(); io.Copy(a, b) }()
    go func() { defer wg.Done(); io.Copy(b, a) }()
    wg.Wait()
}
```

Cross-compile for `GOOS=darwin GOARCH=arm64`, inject into the guest disk image alongside a LaunchDaemon plist. Zero external dependencies.

---

## 3. VMKit Network Primitives

`vmkit/network.go:10-126` defines four modes:

| Mode | Status | API |
|------|--------|-----|
| `nat` (default) | Working | `vz.NewVZNATNetworkDeviceAttachment()` |
| `bridged:<iface>` | Working | `vz.NewBridgedNetworkDeviceAttachmentWithInterface()` |
| `vmnet` | **Stub** — returns "not yet implemented" | — |
| `none` | Working | Skips `SetNetworkDevices()` entirely |

### Per-VM selectivity

Network mode is fully per-VM via the `-network` flag:

```bash
./vz-macos run -network nat        # Full internet access
./vz-macos run -network none       # Air-gapped, vsock-only
./vz-macos run -network bridged:en0 # On host LAN
```

When `none` is selected, `macos.go:223-234` skips adding any `VZNetworkDeviceConfiguration`. The guest has **no network interface** — the only path out is vsock.

### Multiple network devices

`networking.go:57-78` provides `SetupMultipleNetworkDevices()` for attaching more than one NIC, though this is not needed for the vsock proxy design.

### Relevant files

- `vmkit/network.go:10-126` — Core modes and attachment creation
- `examples/vz-macos/networking.go:1-157` — Wrapper layer, help text, multi-device support
- `examples/vz-macos/macos.go:223-234` — macOS VM network setup (conditional on mode)
- `examples/vz-macos/linux.go:96-107` — Linux VM network setup (same pattern)

---

## 4. Virtio-Vsock Multi-Port

**One `VZVirtioSocketDeviceConfiguration` per VM, unlimited concurrent ports.**

The generated bindings (`vz_virtio_socket_device.gen.go:168`) document: *"You can register the same listener object on multiple ports."*

### Already in use

| Port | Purpose | Code |
|------|---------|------|
| 1024 | GRPC agent communication | `vz-agent/main.go` |
| 0x10000001+ | Stdio streams (3 per process) | `vz-container/stdio.go:24-36` |

### Adding port 2223

Host side — one call:

```go
device := vm.SocketDevices()[0]
proxyListener := NewVsockListener(2223)
device.SetSocketListenerForPort(proxyListener.Listener(), 2223)
```

Guest side — connect to CID 2, port 2223:

```go
fd, _ := syscall.Socket(AF_VSOCK, SOCK_STREAM, 0)
sa := buildSockaddrVM(2, 2223) // CID 2 = host, port 2223
syscall.RawSyscall(SYS_CONNECT, uintptr(fd), uintptr(unsafe.Pointer(&sa[0])), 12)
```

Ports 2222 (MCP) and 2223 (proxy) coexist on the same device with zero configuration changes.

### Relevant files

- `generated/virtualization/vz_virtio_socket_device.gen.go:168-202` — Port listener API
- `examples/vz-container/vsock_listener.go:15-109` — Go listener with ObjC delegate
- `examples/vz-container/stdio.go:24-36` — Dynamic port allocation pattern
- `vmkit/vsock.go:53-101` — VsockManager (host-side connect)

---

## 5. Guest Provisioning

The existing disk injection system handles everything needed.

### Current flow (`provision.go:779-1008`)

1. `hdiutil attach disk.img -nobrowse -nomount`
2. Find APFS "Data" partition via `diskutil list`
3. `diskutil mount <partition>` — handles non-deterministic mount points (`/Volumes/Data 1`, etc.)
4. `diskutil enableOwnership <partition>` — APFS has ownership disabled by default on images
5. Write files to mapped guest paths:
   - Guest `/var/db/` → Host `/Volumes/Data/private/var/db/`
   - Guest `/Library/` → Host `/Volumes/Data/Library/`
   - Guest `/usr/local/bin/` → Host `/Volumes/Data/usr/local/bin/`
6. `sudo chown root:wheel` on LaunchDaemon plists (launchd silently ignores wrong ownership)
7. `hdiutil detach`

### What to inject for proxy bridge

**File 1**: `/usr/local/bin/vz-proxy-bridge` (static Go binary)

```
Host path: /Volumes/Data/usr/local/bin/vz-proxy-bridge
Permissions: 0755
Owner: root:wheel
```

**File 2**: `/Library/LaunchDaemons/com.nanoclaw.proxy-bridge.plist`

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.nanoclaw.proxy-bridge</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/vz-proxy-bridge</string>
        <string>-listen</string>
        <string>127.0.0.1:8080</string>
        <string>-vsock-cid</string>
        <string>2</string>
        <string>-vsock-port</string>
        <string>2223</string>
    </array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardOutPath</key>
    <string>/var/log/vz-proxy-bridge.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/vz-proxy-bridge.log</string>
</dict>
</plist>
```

**File 3** (optional): `/etc/profile.d/proxy.sh` or inject into `/etc/zprofile`

```bash
export http_proxy=http://127.0.0.1:8080
export https_proxy=http://127.0.0.1:8080
export HTTP_PROXY=http://127.0.0.1:8080
export HTTPS_PROXY=http://127.0.0.1:8080
```

### Boot execution order

```
Kernel → launchd → LaunchDaemons
                       ├── com.vz.provision.plist (user creation)
                       └── com.nanoclaw.proxy-bridge.plist (proxy bridge)
                   → WindowServer → loginwindow → Desktop
```

The proxy bridge starts before any user session, so package managers work immediately on login.

### Relevant files

- `examples/vz-macos/provision.go:381-601` — APFS volume discovery/mounting
- `examples/vz-macos/provision.go:779-1008` — Main injection orchestrator
- `examples/vz-macos/provision.go:1432-1584` — LaunchDaemon + script templates (pattern to follow)

---

## 6. Performance

**No benchmarks exist in this codebase.** Published data from comparable systems:

### Raw vsock throughput

| Platform | Throughput | Source |
|----------|-----------|--------|
| Linux KVM (vhost-vsock) | 12-40 Gbps | KVM Forum 2019 |
| Apple Silicon (estimated) | ~10-25 Gbps | Extrapolated |
| With userspace proxy (gvisor-tap-vsock) | 1.6-2.3 Gbps | Lima/Podman |

### Latency

| Metric | Value | Source |
|--------|-------|--------|
| vsock packet round-trip | ~30-100 microseconds | Linux Plumbers 2023 |
| Per-connection proxy overhead | ~0.1ms | Single vsock RTT |
| TLS handshake to remote server | 50-200ms | Typical |

### Impact on package downloads

**Negligible.** The internet link is always the bottleneck.

- `pip install numpy`: ~50MB download over a 100Mbps link takes ~4 seconds. The vsock hop adds 0.1ms.
- `npm install react`: Multiple small fetches. Each connection adds 0.1ms vs 50-200ms TLS overhead.
- Docker Desktop already routes all container traffic through vsock (LinuxKit VM ↔ host). No user-visible impact.
- Lima VZ driver reported slowness was caused by DNS proxy overhead in gvisor-tap-vsock, not vsock throughput.

---

## Proposed Architecture

```
┌─────────────── macOS Guest VM ───────────────┐
│                                               │
│  pip/npm/cargo ──→ http://127.0.0.1:8080     │
│                         │                     │
│                   vz-proxy-bridge             │
│                   (LaunchDaemon)              │
│                         │                     │
│                   AF_VSOCK connect            │
│                   CID=2, port=2223            │
│                                               │
│  [NO NETWORK INTERFACE — -network none]       │
└─────────────────────┬─────────────────────────┘
                      │ virtio-vsock
                      │ (same VZVirtioSocketDevice
                      │  as MCP on port 2222)
┌─────────────────────┴─────────────────────────┐
│                    Host                        │
│                                                │
│  VsockProxyBridge                              │
│  (listens on vsock port 2223)                  │
│         │                                      │
│         ▼                                      │
│  Egress Policy Engine                          │
│  (allowlist: pypi.org, npmjs.org, crates.io)   │
│         │                                      │
│         ▼                                      │
│  Internet (filtered)                           │
└────────────────────────────────────────────────┘
```

## New Code Required

| Component | Estimated Size | Base Pattern |
|-----------|---------------|--------------|
| `vz-proxy-bridge` (guest binary) | ~100 LOC | `vz-agent/vsock_darwin.go` + `vminitd/vsock_proxy.go` |
| Host `VsockProxyBridge` | ~150 LOC | `vz-container/vsock_listener.go` |
| Provisioning integration | ~50 LOC | `provision.go:1432-1584` |
| Proxy env injection | ~20 LOC | Append to existing provision script |

### Existing code reusable as-is

| Component | Location | What it provides |
|-----------|----------|-----------------|
| Guest AF_VSOCK sockets | `vz-agent/vsock_darwin.go` | Socket creation, bind, listen, connect |
| Host vsock listener | `vz-container/vsock_listener.go` | ObjC delegate, connection channel |
| Bidirectional relay | `vminitd/vsock_proxy.go:153-163` | `io.Copy` both directions |
| Disk injection | `provision.go:779-1008` | APFS mount, file write, ownership fix |
| LaunchDaemon template | `provision.go:1432-1459` | Plist generation |
| Network mode "none" | `vmkit/network.go:94-95` | Air-gapped VM |
| Multi-port vsock | `vz_virtio_socket_device.gen.go:168` | SetSocketListenerForPort |
| VsockManager connect | `vmkit/vsock.go:53-101` | Host→guest async connect |

---

## Open Questions

1. **HTTPS CONNECT tunneling**: The proxy bridge as described handles HTTP. For HTTPS, the host-side proxy needs to support the `CONNECT` method to tunnel TLS. Standard HTTP proxy behavior — no TLS termination needed.

2. **DNS resolution**: With `-network none`, the guest has no DNS. Options:
   - Proxy bridge handles DNS (CONNECT to hostnames, proxy resolves)
   - Inject `/etc/hosts` entries for known registries
   - Run a DNS-over-vsock forwarder on a third port

3. **Authentication**: Should the proxy bridge require auth? Probably not — it only listens on localhost inside the VM.

4. **SOCKS5 vs HTTP proxy**: HTTP proxy is simpler and universally supported by package managers. SOCKS5 would be more general but adds complexity. Recommend starting with HTTP proxy.

5. **Guest binary updates**: How to update `vz-proxy-bridge` inside running VMs? Options:
   - vz-agent `WriteFile` + restart LaunchDaemon
   - Re-inject on next VM stop/start cycle
