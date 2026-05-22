---
title: GUI & Display
---
# GUI & Display

Native macOS window with toolbar, menu bar, frame persistence, and multi-display support.

## Basic Usage

```bash
cove run                     # GUI enabled by default
cove run -headless           # no window
cove run -gui                # explicit GUI mode
```

## Display Configuration

Single display with specific resolution:

```bash
cove run -display 1920x1080
cove run -display 2560x1440@144       # custom PPI
```

Presets:

```bash
cove run -display 4k
cove run -display 1080p
cove run -display 720p
cove run -display retina
```

Multiple displays:

```bash
cove run -display 1920x1080 -display 1024x768
```

## Window Frame Persistence

Window position and display placement are automatically saved per-VM. On next launch, the window restores to its previous position and display.

## Headless to GUI Switching

Switch a running headless VM to GUI mode (or back) without stopping:

```bash
cove ctl gui status                    # check current mode
cove ctl gui open                      # show window for headless VM
cove ctl gui close                     # return to headless mode
```

For Windows QEMU VMs, Cove either uses QEMU's Cocoa window or, when launched
with `-vnc`, opens the local VNC console for headed runs. Use
`cove gui -vm <name>` to reopen a QEMU VNC console. Use
`cove gui -vm <name> diagnose` when the viewer appears stale or the login state
is unclear; it writes a current screenshot under the VM's `qemu/screenshots`
directory and reports whether Windows is already logged in. `gui close` cannot
close an external VNC viewer window; close that viewer directly. The QEMU
Windows VNC console is a localhost viewer endpoint. Windows credentials shown
by `gui status` are guest-login credentials, not VNC authentication credentials.

The Cove-owned QEMU viewer is the default for QEMU VNC VMs:

```bash
cove gui -vm win open
```

This renders the local QEMU RFB stream in a Cove AppKit window and makes
`gui status` report `qemu-vnc-cove` while the viewer process is alive. In that
mode, `gui close` closes only the Cove viewer and leaves QEMU running. The
viewer uses the same persistent RFB connection for display refresh and end-user
keyboard and pointer input. It also uses the same NSWindow frame autosave
mechanism as native VM windows, keyed under a Windows QEMU VM identity. The
viewer installs a minimal macOS menu and toolbar with screenshot and
close-viewer actions.
Set `COVE_QEMU_GUI_VIEWER=external` to open the system VNC viewer instead.

## Automation Backend

Control how screenshots and input events are routed:

```bash
cove run -automation-backend auto          # default: picks best method
cove run -automation-backend framebuffer   # use framebuffer capture
cove run -automation-backend window        # use window capture

cove ctl gui backend framebuffer           # change at runtime
cove ctl gui capture-backend window        # change screenshot backend only
cove ctl gui input-backend direct          # change input backend only
```

## Launch Order

Control the order of GUI window creation and VM start:

```bash
cove run -launch-order window-first    # show window, then start VM (default)
cove run -launch-order start-first     # start VM, then show window
```

## VNC Server

Expose a VNC server for remote access:

```bash
cove run -vnc :5901 -vnc-password <password>
cove run -vnc :5901 -vnc-password <password> -vnc-bonjour "My VM"
```

Check VNC status on a running VM:

```bash
cove ctl vnc status
```

The status output includes the localhost endpoint, whether password
authentication is enabled, and the advertised Bonjour service name when one is
configured. Bonjour advertisement requires `-vnc-password`; unauthenticated VNC
is intentionally not advertised. Use `-vnc-password` whenever you enable VNC.

Host-containment mode rejects VNC entirely:

```bash
cove run -host-containment -vnc :5901 -vnc-password <password>
# error: -sandbox-level host-containment does not allow -vnc or -vnc-bonjour
```

## Debug Stub

Attach the private GDB debug stub for kernel or low-level guest work:

```bash
cove run -gdb :1234
cove ctl debug-stub status
```

The debug-stub status includes an endpoint and an `lldb` connection hint.
`-gdb-listen-all` exposes the listener beyond localhost and should only be used
on trusted networks. Host-containment mode rejects debugger listeners.
