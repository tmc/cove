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
cove run -vnc :5901
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
is intentionally not advertised.

Host-containment mode rejects VNC entirely:

```bash
cove run -host-containment -vnc :5901
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
