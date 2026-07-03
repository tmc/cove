# Design 044 Verification Runbook (QEMU Windows display parity)

Date: 2026-07-02

The design 044 Verification checklist is a *live* check that must run against a
running Windows QEMU guest with a display. It cannot run in headless CI (no
AppKit window; focus-gated input needs an interactive session). This runbook is
the exact command sequence; record PASS/FAIL per item.

**Status (2026-07-02):** run live against `windows-qemu`; 8/11 pass, checks #3,
#4, #7, #8, #9, #10 plus #1/#2 confirmed. Check #3 (repeated `open` focuses
instead of duplicating) was fixed in commit `d649f398`. Checks #5 (frame
persistence round-trip) and #6 (focus-gated input into the guest) still need
interactive confirmation. See the results table in
`docs/designs/044-qemu-display-window.md`.

**Launching the viewer from a headless/SSH context:** the AppKit viewer must
bind to the logged-in WindowServer session or its RFB connection EOFs before the
window renders. From a normal GUI terminal `cove gui open` already inherits that
session; from SSH/tooling, launch it into the console session:

```sh
launchctl asuser "$(stat -f '%u' /dev/console)" cove qemu-display -vm "$VM"
```

## Prerequisites

- A built, signed `cove` on `PATH` (see the build recipe below).
- A Windows QEMU VM. This host already has one: `windows-qemu`
  (`~/.vz/vms/windows-qemu`, 30G `windows.qcow2`). Substitute your VM name for
  `$VM` below.

```sh
VM=windows-qemu
```

### Build recipe (while apple/x is unreleased)

`cmd/cove` depends on `github.com/tmc/apple/x/codesign` and
`.../x/guest/portfwd`, which are not in any released apple tag (through
v0.6.14); they live only on the local apple checkout's `x-shared-extraction`
branch. Build via a go.work overlay — do NOT edit go.mod/go.sum:

```sh
# from the cove worktree
tmpwork=$(mktemp -d)
cat > "$tmpwork/go.work" <<EOF
go 1.25.5
use $(pwd)
use /Users/tmc/go/src/github.com/tmc/apple
EOF
GOWORK="$tmpwork/go.work" go build -o /tmp/cove ./cmd/cove
codesign -s - -f --entitlements cmd/cove/vz.entitlements /tmp/cove
export PATH="/tmp:$PATH"   # or install /tmp/cove where you keep it
```

Once a released apple version ships those packages: `go get github.com/tmc/apple@<ver>`,
drop the overlay, and `go build ./cmd/cove` directly.

## Checklist

Start the VM headed so the Cove-owned window can open:

```sh
cove up -windows -windows-backend qemu -vnc :1 -vm "$VM"   # or `cove run -vm "$VM"` if already provisioned
```

1. **Cove-owned window opens.**
   ```sh
   cove gui -vm "$VM" open
   ```
   PASS: a Cove-titled window shows the Windows desktop.

2. **`gui status` reports the Cove-owned mode + native input.**
   ```sh
   cove gui -vm "$VM" status
   ```
   PASS: output includes `gui: qemu-vnc-cove` and
   `input:   responder (native focus; input only while the Cove window is focused)`.
   JSON form (`--json` if available, or read `vm/qemu-status.json` from a bundle)
   shows `"gui":"qemu-vnc-cove"` and `"displayInputMode":"responder"`.

3. **`open` on an already-open window focuses it (no second window).**
   ```sh
   cove gui -vm "$VM" open
   ```
   PASS: the existing window comes to front; no duplicate viewer process
   (`ls ~/.vz/vms/$VM/qemu/viewer.pid` unchanged).

4. **`close` closes the window but leaves QEMU running.**
   ```sh
   cove gui -vm "$VM" close
   pgrep -fl "qemu.*$VM"     # QEMU still present
   cove ctl -vm "$VM" ready  # guest still reachable
   ```
   PASS: window gone, QEMU process alive, guest agent still answers.

5. **Frame position/size persist across close/open and VM restart.**
   Reposition/resize the window, then:
   ```sh
   cove gui -vm "$VM" close && cove gui -vm "$VM" open   # same frame
   cove stop -vm "$VM" && cove run -vm "$VM" && cove gui -vm "$VM" open   # still same frame
   ```
   PASS: window reopens at the saved frame (autosave key
   `window-display-com.tmc.cove.window.$VM.$VM`).

6. **Focus-gated keyboard/pointer land in the guest with NO Accessibility grant.**
   Confirm `cove` is NOT listed (or is unchecked) in System Settings → Privacy &
   Security → Accessibility. With the Cove window focused, open Notepad in the
   guest and type; click a UI element.
   ```sh
   cove ctl -vm "$VM" screenshot -o /tmp/win-after-type.png   # verify typed text
   ```
   PASS: keystrokes and clicks reach the guest while the window is focused, with
   no Accessibility permission for `cove`. Click away (unfocus) and confirm
   input no longer forwards (native focus semantics).

7. **`ctl screenshot` matches the visible desktop.**
   ```sh
   cove ctl -vm "$VM" screenshot -o /tmp/win-desktop.png
   ```
   PASS: the image matches what the Cove window shows.

8. **Support bundle omits screenshots by default yet records the display mode.**
   ```sh
   cove support-bundle -vm "$VM" -o /tmp/win-bundle.tgz
   tar tzf /tmp/win-bundle.tgz | grep -E 'screenshot|qemu-status'
   tar xzf /tmp/win-bundle.tgz -O vm/qemu-status.json | python3 -m json.tool | grep -E 'gui|displayInputMode'
   ```
   PASS: no screenshot entries; `vm/qemu-status.json` present with `gui` +
   `displayInputMode`.

9. **`-include-screenshot` includes a valid, unredacted-marked image.**
   ```sh
   cove support-bundle -vm "$VM" -include-screenshot -o /tmp/win-bundle-shot.tgz
   tar tzf /tmp/win-bundle-shot.tgz | grep -i screenshot
   ```
   PASS: a valid screen image is present and marked unredacted.

10. **RFB-fail path names the VNC fallback.**
    Simulate viewer/RFB failure (e.g. stop QEMU's VNC export or point the viewer
    at a dead port), then:
    ```sh
    cove gui -vm "$VM" open
    ```
    PASS: the error names the fallback VNC URL (and, if the viewer started then
    exited, the last `qemu/viewer.log` line).

## Recording results

When all ten pass on a live guest, update
`docs/designs/044-qemu-display-window.md` Status to `Verified` with the date and
VM name, and the audit's Stop Condition can be marked satisfied.
