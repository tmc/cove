---
title: Windows
description: Run Windows 11 ARM64 guests on the direct QEMU/HVF backend, with install media, display modes, input, clipboard, and automation.
icon: compass
---
# Windows

Windows 11 ARM64 runs on cove through a direct QEMU/HVF backend rather than
Virtualization.framework. The backend installs Windows unattended, connects the
guest agent, and supports exec, file copy, screenshots, OCR, clipboard, a
Cove-owned display window, and support bundles.

Windows support is Beta. Suspend and resume, snapshots, clone, fork, and the
image store do not cover Windows VMs. There is no audio device, and no TPM
device: the unattended install bypasses the Windows TPM 2.0 requirement instead.

`-windows-backend` defaults to `qemu`. The `vz` backend is selectable but
experimental, and prints a warning saying Windows needs a linear-framebuffer GOP
that Virtualization.framework does not expose publicly. `cove up -windows`
supports the QEMU backend only.

## Prerequisites

An Apple Silicon Mac, since the backend runs QEMU with the `hvf` accelerator,
and QEMU on `PATH`:

```bash
brew install qemu
```

Check the backend prerequisites:

```bash
cove doctor qemu
cove doctor qemu -json
```

The check covers the host, `qemu-system-aarch64` and its version, `qemu-img`,
the AArch64 EFI pflash code image and vars template, the display-device and
screenshot/text backend overrides, the SPICE vdagent chardev used for clipboard
transport, the console session the Cove display window needs, and the cached
VirtIO driver ISO.

`cove doctor host` runs these same QEMU checks itself when this host already has
a Windows VM, or when the invocation asks for Windows; `cove doctor qemu` is the
focused form.

## Install media

Pass an ISO explicitly:

```bash
cove up -windows -iso ~/Win11_ARM64.iso -user me
```

`-iso` also accepts a URL, which is downloaded into the VM directory.

With no `-iso`, cove looks for a Windows ISO in the VM directory and the cache
directory, and otherwise downloads the latest Windows ESD and converts it to an
ISO. The conversion needs an esd2iso implementation: `esd2iso.sh` or
`w11arm_esd2iso` on `PATH`, the copy inside CrystalFetch, or a path in
`COVE_ESD2ISO`. When no converter is found, cove reports where the ESD was
downloaded so you can convert it yourself and rerun.

## First boot

```bash
cove up -windows -iso ~/Win11_ARM64.iso -user me
```

Install is unattended. cove generates an Autounattend ISO carrying the user
account and agent configuration, attaches the VirtIO driver ISO, and, when
clipboard sharing is enabled, the SPICE guest tools installer. It also sends the
"press any key to boot" keypresses for you after a short delay.

After install, `up` runs the `windows-install` vzscript, plus
`windows-clipboard` when clipboard sharing is enabled. Pass `-vzscripts` to
choose recipes instead, and `-no-shutdown` to leave the VM running when the
scripts finish.

Installation refuses to overwrite an existing disk. Use a new disk for a fresh
installation, or boot the installed VM again with:

```bash
cove run -windows
```

The daemon and user agents are reached through host-forwarded localhost ports on
the NAT network. QEMU Windows VMs accept `-network nat` or `-network none`;
`none` disables the agent forwards along with guest networking.

## Display modes

To get a Cove-owned window, start the VM with a localhost VNC endpoint. `-vnc`
is a global flag, so its position depends on the subcommand: `cove run`
reparses flags after the subcommand, while `cove up` does not and needs `-vnc`
before it.

```bash
cove run -windows -vnc :5901
cove -vnc :5901 up -windows -iso ~/Win11_ARM64.iso -user me
```

The port must be 5900 or higher, and the listener is bound to `127.0.0.1`.

```bash
cove gui -vm windows status     # backend, display mode, input mode, VNC URL
cove gui -vm windows open       # open, or focus an already-open window
cove gui -vm windows close      # close the Cove window, leave QEMU running
cove gui -vm windows diagnose   # screenshot plus detected screen state
```

`gui status` reports one of three display modes:

- `qemu-vnc-cove` — the Cove-owned AppKit window over the guest's localhost VNC.
- `qemu-vnc-external` — a VNC endpoint exists but the Cove viewer is not
  running. `gui close` cannot close an external viewer window.
- `qemu-cocoa-or-headless` — no VNC endpoint; restart the VM with `-vnc` to get
  a window cove can manage.

See [GUI & Display](../features/gui-display.md) for how these relate to the
native Virtualization.framework window.

Launch the viewer from a logged-in graphical session. From an SSH or otherwise
non-Aqua context the AppKit viewer cannot attach to the window server; start it
through the console session (`launchctl asuser <uid> cove gui -vm <name> open`)
if you need to open it from headless tooling.

## Display size

The guest display geometry is set with `-windows-display-size WxH`. The value is
persisted in the VM's `qemu/metadata.json`, so it applies to later runs of the
same VM without repeating the flag. Resolution is taken from, in order, the
flag, the persisted value, `COVE_QEMU_DISPLAY_SIZE`, and the built-in default of
1280x800. Sizes below 640x480 fall back to the default.

## Keyboard and pointer

The Cove window delivers keyboard and pointer events through ordinary AppKit
focus, reported as `input: responder` by `gui status`: events reach the guest
only while the window is focused, and no Accessibility grant is required.

Scripted input works without a window. `cove ctl` parses its own flags before
the command word, so `-vm` comes first and the command's arguments follow it:

```bash
cove ctl -vm windows key space
cove ctl -vm windows key return
cove ctl -vm windows text "notepad"
cove ctl -vm windows mouse 100 100 move
cove ctl -vm windows mouse 100 100 click
cove ctl -vm windows click-text "Recycle Bin"
```

`key` takes a key name such as `space`, `return`, `tab`, `escape`, `delete`, an
arrow name, a single letter or digit, or `f1` through `f12`; it also accepts the
macOS keycodes `36`, `48`, `49`, `51`, `53`, and `123` through `126`. `mouse`
takes `x y action`, where the action is `move`, `down`, `up`, or `click`, and
coordinates between 0 and 1 are treated as fractions of the framebuffer.

`text` input goes over the VNC/RFB connection when the VM has a VNC endpoint and
falls back to QEMU monitor `sendkey` otherwise. `mouse` requires a VNC endpoint.
The default pointer device is `usb-tablet`, so pointer coordinates are absolute
and match the visible desktop.

## Clipboard

Clipboard sharing uses the QEMU SPICE vdagent chardev plus the SPICE guest tools
installed during provisioning. It is on by default; the global `-clipboard=false`
disables it, and `cove doctor qemu` reports whether the host QEMU has the
`qemu-vdagent` chardev.

```bash
cove ctl -vm windows clipboard-push "text"
cove ctl -vm windows clipboard-pull
cove ctl -vm windows clipboard-sync-to-guest
cove ctl -vm windows clipboard-sync-from-guest
```

The agent clipboard helpers must run in the logged-in user's desktop session;
the service session has a separate clipboard. Text is transferred as Unicode,
including empty text. Direct helper invocation can clear the clipboard with
`vz-agent.exe -clipboard-set-base64=`.

## Shared directory

QEMU Windows VMs do not use VirtioFS. `-windows-shared-dir PATH` exports a host
directory through QEMU's user-mode SMB server on the NAT network, reachable in
Windows as `\\10.0.2.4\qemu`:

```bash
cove run -windows -windows-shared-dir ~/share
```

The directory is persisted in the VM's `qemu/metadata.json`. The path is taken
from, in order, the flag, the persisted value, and `COVE_QEMU_SMB_DIR`. It must
be an existing directory and must not contain a comma, which QEMU's `-netdev
user` option cannot parse. Sharing needs a QEMU build with SMB support and is
unavailable with `-network none`. For one-off transfers, `cove cp` is simpler.

## Screenshots and OCR

```bash
cove ctl -vm windows -o /tmp/win.png screenshot
cove ctl -vm windows ocr
```

Screenshots prefer the RFB framebuffer when a VNC endpoint exists and fall back
to the QEMU monitor's `screendump`. `cove gui -vm windows status` shows which
backend was resolved.

Support bundles omit guest pixels by default:

```bash
cove support bundle -vm windows
cove support bundle -vm windows -include-screenshot
```

The bundle records QEMU metadata and the computed display mode and backends in
`vm/qemu-status.json`, with the guest password redacted. `-include-screenshot`
adds an unredacted screen image.

## Running commands and copying files

```bash
cove exec -vm windows whoami            # logged-in user session
cove exec -vm windows --daemon whoami   # service agent
cove -vm windows shell -- whoami        # one-shot command
cove cp ./app.log windows:/C:/Users/me/Desktop/app.log
cove cp windows:/C:/Users/me/out.txt ./out.txt
```

Commands execute directly, without an implicit shell. For shell syntax, pass
`cmd.exe /d /c` or a PowerShell command explicitly:

```bash
cove exec -vm windows cmd.exe /d /c "echo hello & whoami"
cove exec -vm windows powershell.exe -NoProfile -Command 'Get-NetIPConfiguration'
```

Interactive `cove shell` and PTY execution are not supported for QEMU Windows
VMs yet; use one-shot commands or the display window. Guest paths are written
as `vm:/C:/path`. The daemon cannot switch users; use the user agent to run in
the logged-in session. The agent signal RPC supports signal 9 to terminate the
tracked process only; Unix signals 2 and 15 and process-tree termination are
not supported. Agent exec requests inherit the agent environment and apply any
requested environment overrides in both service and user modes. Guest time
synchronization through the agent remains unsupported.

## Shutdown

```bash
cove ctl -vm windows request-stop   # ACPI power button; the guest may ignore it
cove ctl -vm windows stop           # quit QEMU
```

`cove ctl -vm windows agent-shutdown` asks the guest agent to shut Windows down,
and `cove ctl -vm windows agent-shutdown force` forces it.

## Troubleshooting

- Run `cove doctor qemu` first: it names the missing tool, firmware image, or
  invalid override.
- No window, and `gui status` shows `qemu-cocoa-or-headless`: the VM was started
  without `-vnc`. Restart it with `-vnc :5901`.
- `gui open` prints a VNC fallback URL: the Cove viewer exited during startup.
  The last line of `<vm>/qemu/viewer.log` says why; use the VNC URL meanwhile.
- Input does not reach the guest: confirm the Cove window is focused and check
  the `input:` line in `gui status`. `COVE_QEMU_LEGACY_MONITORS=1` restores the
  older global-event path, which needs Accessibility permission and forwards
  events while unfocused.
- Agent commands time out while Windows is running: confirm NAT networking,
  that the daemon and user agent are running, and that the guest firewall allows
  their TCP listeners (ports 1024 and 1025 by default). A user agent requires a
  logged-in user. `-network none` disables both host forwards.
- The Windows agent listens on TCP, using `-tcp-listen` or
  `VZ_AGENT_TCP_LISTEN` to override its address. A guest loopback-only bind cannot
  receive QEMU NAT forwards. Repeat custom `COVE_QEMU_AGENT_GUEST_PORT` and
  `COVE_QEMU_USER_AGENT_GUEST_PORT` overrides on each run; saved metadata does
  not restore them. Keep the guest listeners configured to match.
  The protocol is plaintext and unauthenticated;
  restrict access to the host's loopback forwards and a trusted guest network.
- Agent `-relay`, `-udp-relay`, and reverse relay options require vsock and are
  unsupported on Windows. Use QEMU networking or SMB for guest connectivity.
- QEMU monitor unavailable: the VM is not running, or has exited. Serial output
  is written to `<vm>/qemu/serial.log` unless `-serial` says otherwise.
- Collect everything at once with `cove support bundle -vm <name>`.

## Guest integration tests

On Windows with Go installed, run the agent tests from a checkout:

```powershell
go test ./cmd/vz-agent
```

These cover TCP RPC transport with HTTP/1.1 and HTTP/2, command output and exit
status, disk capacity, process termination, and shutdown command arguments.
Shutdown tests do not shut down the machine. Clipboard roundtrip tests are
opt-in because they replace all clipboard formats; run them in a disposable
logged-in desktop:

```powershell
$env:COVE_TEST_WINDOWS_CLIPBOARD = '1'
go test ./cmd/vz-agent -run Clipboard
```

Cross-compiling these tests on macOS checks compilation only.

## Diagnostic environment variables

The supported configuration surface is the command-line flags above. The
`COVE_QEMU_*` variables below exist for debugging and experiments; they are not
stable configuration, and `cove doctor qemu` validates several of them.

| Variable | Purpose |
|----------|---------|
| `COVE_QEMU_SYSTEM_AARCH64` | Path to `qemu-system-aarch64` instead of the one on `PATH` |
| `COVE_QEMU_IMG` | Path to `qemu-img` instead of the one on `PATH` |
| `COVE_QEMU_EFI_CODE` | Path to the AArch64 EFI pflash code image |
| `COVE_QEMU_EFI_VARS_TEMPLATE` | Path to the writable EFI pflash vars template |
| `COVE_QEMU_DISPLAY_DEVICE` | Display device: `ramfb`, `virtio-gpu-pci`, `ramfb+virtio-gpu-pci`, `bochs-display`, or `none` |
| `COVE_QEMU_DISPLAY_SIZE` | Guest display size as `WxH`; below `-windows-display-size` and the persisted value in precedence |
| `COVE_QEMU_INPUT_DEVICE` | Pointer device: `usb-tablet` (default, absolute) or `usb-mouse` |
| `COVE_QEMU_NODEFAULTS` | Set to `0` to keep QEMU's default devices instead of passing `-nodefaults` |
| `COVE_QEMU_AUTO_BOOT_KEY` | Set to `0` to stop cove sending the "press any key to boot" keypresses |
| `COVE_QEMU_BOOT_KEY_DELAY` | Delay before the first boot keypress |
| `COVE_QEMU_BOOT_KEY_COUNT` | Number of boot keypresses |
| `COVE_QEMU_BOOT_KEY_INTERVAL` | Interval between boot keypresses |
| `COVE_QEMU_SMB_DIR` | Host directory shared over SMB; below `-windows-shared-dir` and the persisted value in precedence |
| `COVE_QEMU_AGENT_FORWARD` | Set to `0` to disable both guest-agent host port forwards |
| `COVE_QEMU_AGENT_GUEST_PORT` | Daemon agent port inside the guest |
| `COVE_QEMU_AGENT_HOST_PORT` | Host port for the daemon agent |
| `COVE_QEMU_USER_AGENT_FORWARD` | Set to `0` to disable only the user-agent forward |
| `COVE_QEMU_USER_AGENT_GUEST_PORT` | User agent port inside the guest |
| `COVE_QEMU_USER_AGENT_HOST_PORT` | Host port for the user agent |
| `COVE_QEMU_SCREENSHOT_BACKEND` | Force the screenshot path: `auto`, `rfb`, or `monitor` |
| `COVE_QEMU_TEXT_BACKEND` | Force the text-input path: `auto`, `rfb`, or `monitor` |
| `COVE_QEMU_GUI_VIEWER` | Set to `external` to make `gui open` use the system VNC viewer instead of the Cove window |
| `COVE_QEMU_LEGACY_MONITORS` | Set to `1` to use the legacy global NSEvent input path, which needs Accessibility permission |
| `COVE_QEMU_DISPLAY_DEBUG` | Set to any value for verbose Cove display-viewer logging |
