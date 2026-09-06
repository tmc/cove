---
title: GUI & Display
description: Native macOS window with toolbar, menu bar, frame persistence, and multi-display support.
icon: puzzle-piece
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

## Display Modes

A VM's display comes from one of three paths. For a QEMU Windows VM,
`cove gui -vm <name> status` names which one is in effect.

**Native VZ window.** Virtualization.framework VMs. Cove owns the window
directly, including the toolbar, menu, frame persistence, display configuration,
and capture backends described above.

**`qemu-vnc-cove`** — the default for a QEMU Windows VM started with `-vnc`.
Cove renders the guest's localhost RFB stream in its own AppKit window, using
one persistent connection for both display refresh and input. Keyboard and
pointer events are delivered through native window focus, so they reach the
guest only while the Cove window is focused, and no Accessibility grant is
needed. The window uses the same NSWindow frame autosave mechanism as native VM
windows and carries a minimal menu and toolbar with screenshot and close-viewer
actions. `gui close` closes only this viewer and leaves QEMU running.

```bash
cove gui -vm win open
cove gui -vm win status
cove gui -vm win close
```

Set `COVE_QEMU_LEGACY_MONITORS=1` to fall back to the legacy global
event-monitor input path, which requires Accessibility permission and forwards
events while the window is unfocused.

**`qemu-vnc-external`** — a VNC endpoint exists but the Cove viewer is not
running, so the guest is reached through an external VNC viewer or QEMU's own
Cocoa window. Cove cannot close that window for you; close it directly. Set
`COVE_QEMU_GUI_VIEWER=external` to choose this path deliberately.

A QEMU Windows VM started without `-vnc` reports `qemu-cocoa-or-headless` and
has no window cove can open; restart it with `-vnc` to get one.

Use `cove gui -vm <name> diagnose` when the viewer appears stale or the login
state is unclear; it writes a current screenshot under the VM's
`qemu/screenshots` directory and reports whether Windows is already logged in.
Windows credentials shown by `gui status` are guest-login credentials, not VNC
authentication credentials.

See the [Windows guide](../guides/windows.md) for the rest of the QEMU Windows
workflow.

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
