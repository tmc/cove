# Design 044: QEMU Display Window

Status: Draft.
Date: 2026-05-21

## Problem

The Windows QEMU backend now boots and controls a Windows guest through direct
QEMU/HVF, host-forwarded agents, screenshots, OCR, keyboard input, clipboard,
support bundles, and `cove gui -vm <name>` reopening for VNC-backed runs.

The display path is still not equivalent to Cove's native Virtualization.framework
window. A headed QEMU VM uses either QEMU's Cocoa window or a local VNC viewer.
Those windows are not owned by Cove, so Cove cannot provide the same close/open
semantics, frame persistence, toolbar integration, capture backend selection, or
single-process lifecycle as native VZ VMs.

This design defines the next display slice for QEMU Windows. It is not a claim
that QEMU display parity already exists.

## Current Contract

The current backend contract is:

- `cove up -windows -windows-backend qemu -vnc :NNNN` records a local VNC
  endpoint in the VM metadata;
- headed VNC runs open the local VNC URL automatically after the QEMU monitor is
  ready;
- `cove gui -vm <name>` reopens that VNC URL;
- `cove gui -vm <name> close` reports that Cove cannot close an external VNC
  viewer window;
- `cove ctl -vm <name> screenshot`, `ocr`, `key`, `text`, and `click-text`
  operate through QEMU monitor and screenshot primitives, not through a Cove
  AppKit view;
- support bundles omit screenshots by default and require
  `-include-screenshot` for guest pixels.

That contract is usable, but it is intentionally weaker than native VZ display
ownership.

## Goal

Give QEMU Windows a Cove-owned display window that is good enough for normal
interactive setup and post-install use:

- `cove gui -vm <name> open` creates or focuses a Cove-owned window;
- `cove gui -vm <name> close` closes that window without stopping QEMU;
- frame position and size persist per VM;
- screenshots and OCR can use the same visible pixels the user sees;
- keyboard and pointer input are routed through the same window;
- the implementation works with the existing QEMU process and metadata layout;
- failure messages point to the VNC fallback.

## Non-Goals

This slice should not patch QEMU or replace QEMU's display backend. It should
also not attempt remote, Bonjour-advertised, or authenticated VNC sharing. The
first pass is a localhost-only viewer for Cove-owned interaction with one
running VM.

Do not remove QEMU Cocoa or external VNC fallback behavior. They are useful
diagnostic paths and remain the escape hatches when the Cove-owned viewer fails.

## Preferred Shape

Use QEMU's existing VNC server as the transport and render it inside a Cove
AppKit window:

1. Keep `-vnc 127.0.0.1:display` as the QEMU display export.
2. Add a small internal RFB client package for localhost connections.
3. Start with RFB 3.8, no authentication, raw encoding, framebuffer update
   requests, key events, pointer events, and clipboard only if it fits cleanly.
4. Render decoded pixels in an AppKit view backed by a stable image buffer.
5. Route keyboard and pointer events from the AppKit view to RFB input events.
6. Reuse existing Cove window persistence, menu, and lifecycle code where it is
   product-level behavior rather than VZ-specific view construction.

The initial client may reject unsupported encodings with a clear error. Adding
Tight or ZRLE can be a later compatibility slice if raw updates are too slow.

The first substrate is `internal/rfb`: a small RFB 3.8 no-auth client with raw
framebuffer updates and key/pointer events. QEMU Windows screenshots can already
prefer that RFB framebuffer before falling back to QEMU monitor screendump, and
`ctl text`, `ctl mouse`, and `click-text` can use RFB-backed input when the VM
has a VNC endpoint. QEMU Windows uses `usb-tablet` by default so RFB absolute
pointer events match the visible desktop. Support bundles record the computed
QEMU display mode plus resolved screenshot/text backends in
`vm/qemu-status.json`.
`COVE_QEMU_SCREENSHOT_BACKEND=rfb|monitor` and
`COVE_QEMU_TEXT_BACKEND=rfb|monitor` are available for forced diagnostics. The
RFB package remains transport-only; window state and Cove CLI behavior stay
outside the package.

## Implementation Slices

### 1. Metadata and CLI Contract

Record whether a QEMU VM has a Cove-owned display viewer available. Keep the
existing `vncURL` and `gui` status fields, but distinguish:

- `gui: qemu-vnc-external` for current external viewer behavior;
- `gui: qemu-vnc-cove` when the Cove-owned window is active;
- `gui: qemu-cocoa-or-headless` when no VNC endpoint exists.

`cove gui -vm <name> status` should expose the selected path in both text and
JSON output.

### 2. RFB Client

Add an internal package with a narrow API:

```go
type Client struct { ... }

func Dial(ctx context.Context, addr string) (*Client, error)
func (c *Client) ReadUpdate(ctx context.Context) (image.Image, error)
func (c *Client) Key(key uint32, down bool) error
func (c *Client) Pointer(x, y int, buttons uint8) error
func (c *Client) Close() error
```

Keep this API transport-focused. Window state, CLI behavior, and VM metadata
belong outside the package.

### 3. AppKit Window

Add a QEMU display window path beside the native VZ GUI path:

- one window per VM;
- focus existing window on repeated `gui open`;
- close only the Cove-owned window;
- display a concise error panel or CLI error when the VNC connection fails;
- keep frame persistence per VM name.

### 4. Automation Integration

After the window path exists, update screenshot and OCR routing so they can
prefer the live Cove-owned framebuffer when present and fall back to QEMU
monitor screendump otherwise. This must not regress headless automation.

### 5. Documentation and Support

Document three display modes clearly:

- native VZ window for VZ-backed VMs;
- Cove-owned QEMU VNC window for QEMU Windows when available;
- external VNC/QEMU Cocoa fallback for diagnostics.

Support bundles should record which path was active. Screenshots must remain
opt-in because guest pixels are not redacted.

## Verification

The feature is not complete until these pass:

- `cove up -windows -windows-backend qemu -vnc :NNNN` opens a Cove-owned window;
- `cove gui -vm <name> status` reports the Cove-owned QEMU display mode;
- `cove gui -vm <name> open` focuses an already-open window;
- `cove gui -vm <name> close` closes the Cove-owned window and leaves QEMU
  running;
- frame position and size survive close/open and VM restart;
- keyboard input logs into Windows and runs a Start menu command;
- pointer input can click a Windows dialog button;
- `cove ctl -vm <name> screenshot` captures the same desktop shown in the
  window;
- `cove support-bundle -vm <name>` omits screenshots by default and records the
  display mode;
- `cove support-bundle -vm <name> -include-screenshot` includes a valid screen
  image and marks it unredacted;
- if the RFB client cannot connect, `gui open` explains the fallback VNC URL.

## Stop Condition

Until those checks pass, QEMU Windows usability is improved but not at native
VZ display parity. The current backend should continue to describe display as
QEMU Cocoa or local VNC, not as a native Cove window.
