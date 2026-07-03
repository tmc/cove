# Design 044: QEMU Display Window

Status: Implemented; live verification pending (see Verification Status).
Date: 2026-05-21
Updated: 2026-07-02

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

## Verification Status

All slices are implemented and land in `cmd/cove` (`qemu_display.go`,
`ctl_qemu.go`, `windows_qemu.go`) with the `internal/rfb` transport. The final
input slice shipped in commit `10bc176a` (native focus via in-view
`NSTrackingArea`; global `NSEvent` monitors demoted to the opt-in
`COVE_QEMU_LEGACY_MONITORS` escape hatch; VNC fallback on viewer startup
failure; `displayInputMode` recorded in status and support bundles).

Build and automated gates pass via the local apple overlay (a `go.work`
overlaying `github.com/tmc/apple` at `x-shared-extraction`, which carries the
yet-unreleased `x/codesign` + `x/guest/portfwd`; no released apple tag through
v0.6.14 has them, and `go.mod`/`go.sum` are not edited to work around it):

- `go build ./cmd/cove/` — pass
- `go vet ./cmd/cove/` — pass
- `go test ./internal/rfb/ -count=1` — pass
- `go test ./cmd/cove/ -count=1` — pass, except
  `TestGoListModuleDirRejectsNonModuleDir`, which fails *only* because the
  overlay `go.work` puts the cove module in scope so `go list -m` no longer
  errors on an unrelated temp dir. Proven a recipe artifact (with `GOWORK=off`
  the same `go list -m` exits 1 as the test expects); it passes in normal CI and
  once a released apple version removes the overlay.

Fixing the gate along the way surfaced two real, pre-existing breakages on the
Windows branch (not from the input slice): the E2E/integration signing helpers
and doc/error strings still pointed at `internal/autosign/vz.entitlements`, which
commit `c4a54078` moved to `cmd/cove/vz.entitlements`; and the `support-bundle`
alias help printed `Usage: cove support-bundle` while the branch's test expected
the canonical `Usage: cove support bundle`. Both are fixed.

The Verification checklist above is a *live* check against a running Windows
guest with a display. It has not yet been run end to end; it is the remaining
gate and is the user's to execute (headless CI cannot open the AppKit window or
confirm focus-gated input without an interactive session). A ready-to-run
command-by-command runbook is at
`docs/research/qemu-windows-044-verification-runbook.md`. Each item maps to a
concrete command:

- window opens / focuses / closes-without-stopping-QEMU:
  `cove gui -vm <name> open|status|close`;
- `gui status` reports `gui: qemu-vnc-cove` and `input: responder`;
- frame persistence: reposition, `close`, `open`, then `cove stop`/`up` and
  reopen;
- focus-gated keyboard/pointer land in the guest with **no** Accessibility grant
  for `cove` (verify in System Settings → Privacy & Security → Accessibility);
- `cove ctl -vm <name> screenshot` matches the visible desktop;
- `cove support-bundle -vm <name>` omits screenshots yet records `gui` +
  `displayInputMode` in `vm/qemu-status.json`;
- `cove support-bundle -vm <name> -include-screenshot` marks pixels unredacted;
- RFB-fail path: stop QEMU's VNC and confirm `gui open` names the fallback URL.

## Display Modes (user-facing)

Cove reports one of three display modes for a VM, visible in
`cove gui -vm <name> status`:

- **native VZ window** — Virtualization.framework VMs. Cove owns the window
  fully.
- **`qemu-vnc-cove`** — QEMU Windows with a Cove-owned AppKit window over the
  guest's localhost VNC. Input uses native focus (`input: responder`): keyboard
  and pointer reach the guest only while the Cove window is focused, and no
  Accessibility permission is needed. `gui open`/`close`/`status` manage this
  window; `close` leaves QEMU running. Set `COVE_QEMU_LEGACY_MONITORS=1` to fall
  back to the legacy global-event-monitor input path (needs Accessibility,
  forwards events while unfocused) only if native focus delivery misbehaves.
- **`qemu-vnc-external`** / QEMU Cocoa — diagnostic fallback when the Cove-owned
  viewer is unavailable or has exited. Cove opens the external VNC URL and
  cannot close that window for you.

## Stop Condition

Until those checks pass, QEMU Windows usability is improved but not at native
VZ display parity. The current backend should continue to describe display as
QEMU Cocoa or local VNC, not as a native Cove window.
