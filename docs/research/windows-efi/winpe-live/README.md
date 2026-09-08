# Live VZ guest frames over NAT

The host received six 1920x1200 Windows Setup PNGs while the VZ VM ran.
Transfers arrived at approximately 13, 15, 17, 19, 21 and 23 seconds of the
receiver's run. Guest IP 192.168.64.55 came from NAT DHCP. All six capture/upload
operations returned success. Native PMU, GOP shim and NetKVM remain unchanged.

The host replied to frame0 with shift-f10. SendInput reported all four events
accepted, but the later screenshots still show Setup's missing-install.wim
dialog, not a command prompt. The intended input effect is not verified.
A changed focus border/hash is insufficient to claim successful keyboard control.

This proves live guest-to-host screen transport and a command reply consumed by
the guest, not an interactive viewer, responsive general input, or installation.
Images are external: /tmp/cove-winpe-live-20260907/retry/frame-{0..5}.png.
Their hashes, byte counts, receive times and command response are in events.json.

The initial single-threaded receiver got no complete frames and guest requests
timed out. The second receiver used ThreadingHTTPServer plus a successful local
HTTP self-check and passed. This does not isolate the original timeout's cause.
No host firewall setting changed; read-only state showed Python allowed incoming
connections. Do not attribute the failed run to a firewall block.

run-retry.py creates a fresh retry directory, an ephemeral TCP listener and a
random per-run URL token, then launches apple-retry.py. The URL is copied only to
the scratch FAT disk. The host accepts PNG POSTs up to2MB and emits just one
fixed input response. Both scripts preserve fixed scratch paths; do not rerun
on existing evidence. Listener and VM stop at the end of the bounded experiment.
No screenshot, guest executable, disk or URL token is staged.

The next discriminator should verify focus and a visible input action before
building a viewer around this channel. SendInput acceptance alone does not
prove the target application acted on a shortcut.

References: [SendInput](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendinput),
[INPUT](https://learn.microsoft.com/en-us/windows/win32/api/winuser/ns-winuser-input).

## Foreground shortcut check

A fresh Alt+Space run again delivered six frames and returned success from
SendInput. GetForegroundWindow/GetWindowText reported Windows Setup each time,
but no system menu appeared and image hashes stayed unchanged. Its source and
text receipts are in input-control/. Raw images remain at
/tmp/cove-winpe-input-20260907/frame-{0..5}.png. The foreground title alone does
not prove intended message delivery. Stop shortcut variants here; an own-window
input control is needed to separate injection correctness from Setup behavior.
