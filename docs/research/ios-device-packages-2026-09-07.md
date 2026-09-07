# iOS device package boundaries

The implementation separates Virtualization.framework helpers from device
protocols. Python is not involved in these packages or in ordinary VM execution.

| Package in tmc/apple | Implemented responsibility |
| --- | --- |
| `x/vzkit/exp/research` | Private hardware descriptor/model construction, ABI-checked ECID access and DFU/iBoot start options. Cove retains VM ownership, dispatch and identity persistence. |
| `x/usbmux` | Plist-protocol device listing and device-port connections through usbmuxd. Each operation owns a daemon connection; a successful dial transfers stream ownership to the caller. |
| `x/irecovery` | macOS libusb loading, Apple DFU/recovery discovery, exact ECID selection, interface claiming and bounded control/bulk transfers. No automatic kernel-driver detachment or write retries. |
| `x/iosrestore` | Bounded restored-service plist framing and TSS requests using a caller-supplied personalization dictionary. No VZ dependency. |

Cove's `ios devices [-libusb PATH]` lists normal/restored daemon attachments and,
when requested, raw USB recovery endpoints separately. An empty result is not
proof that a VM can enter DFU. The public signed executable successfully queried
both the host daemon and `/opt/homebrew/lib/libusb-1.0.dylib` on this host; both
lists were empty. No restore request or USB write was sent to a device.

The libusb adapter uses the C ABI via purego. It does not use MobileDevice.framework
or assume VZMacOSInstaller can restore iOS. The TSS client defaults to HTTPS;
compatibility with Apple's live service has not been demonstrated. The caller
must supply matching build identity, ECID and nonces. A returned dictionary is
not proof that its tickets match a particular VM or firmware.

## Development and validation

Apple changes are isolated in `../apple-wt-ios`, branch `ios-device-packages`.
The packages and plist correction are published on the Apple
`ios-device-packages` branch at `f656c7e780de`. Cove now pins
`v0.6.19-0.20260907144313-f656c7e780de`; its temporary `go.work` has been removed.
The configured GitHub remote is reachable using a command-local URL rewrite
override; no global Git configuration was changed.

Tests cover daemon framing, port byte order, connection ownership, cancellation,
malformed response lengths, restore framing, TSS errors, recovery serial parsing,
USB timeout bounds, closed handles and invalid VZ inputs. Cove's existing identity
and preflight tests exercise the extracted VZ helpers through their callers.

## Remaining work

These are working primitives, not a completed replacement for pymobiledevice3.
Still required: DFU manifestation/reset and recovery transitions, pairing and
lockdown where used, complete restore orchestration and personalization, ASR
image transfer, matching-device reconnect handling, firmware fixtures, and a
qualified end-to-end restore. Discovery, control and bulk primitives alone do
not implement that state machine. RemoteXPC and DTX are outside this initial
restore path; broader parity requirements remain in the F01–F39 ledger.

The current vphone firmware/CFW recipe also still invokes Python. Replacing the
restore transport alone does not remove Python from that delegated recipe.

## Plist integer correction

The new signing transport exposed an existing `x/plist` bug: marshaling unsigned
values above MaxInt64 converted them to negative int64 values. The shared package
now preserves uint64 values in XML and uses a zero-extended 16-byte binary-plist
integer when needed. Decoding accepts those values and rejects overflow when
assigning to narrower integer destinations. Tests include independent binary
fixtures for 1<<63 and MaxUint64, XML/binary round trips, and overflow rejection.
Race tests for plist and its new protocol consumers pass.

## Notebook review

The implementation review qualified the package architecture as approved while
retaining the overall INCOMPLETE verdict. Its three alleged defects were checked
against current code: usbmux Dial waits for cancellation cleanup and checks
ctx.Err before returning; recovery iteration closes each non-retained handle
before advancing; Configure already removes newly staged ROMs on returned errors.
Those claims do not justify removing the existing ownership/rollback logic.
Crash interruption, full restore sequencing and live qualification remain open.

## Published dependency validation

After publishing the Apple branch and replacing the local workspace with the
module pin, `go build ./...` and `go test ./...` pass. `make build` also builds and
signs the public executable. Its device-discovery command again successfully
queries both transports and finds no endpoints. These checks establish ordinary
buildability and discovery transport access, not research VM or restore success.

## Native image upload

Apple commit `f656c7e780de` adds `irecovery.Conn.Upload`: recovery bulk/ZLP and
DFU logical blocks, footer and download-idle polling. It checks complete transfer
counts and device status, respects 24-bit poll delays and supports cancellation
while waiting for connection ownership. It does not execute or finalize the
image; manifestation notification, reset and matching-device reconnect remain
required. Cove pins the implementation, but does not yet expose a firmware upload
or complete restore command.

Packet fixtures follow pinned libirecovery commit `29592eb6` and document its CRC
difference from pymobiledevice3 revision `a16ffc51`. Tests cover the footer bytes,
block boundaries, split footers, recovery ZLP, short transfers, status failures,
initial-state CLRSTATUS/ABORT and explicit retry, cancellation and large recovery
images bypassing the DFU-only block-count limit. Race tests pass. No live image
upload, manifestation, restore or boot has been demonstrated.

The self-prompting anchor confirmed this slice serves F09 and remains on mission.
Its recovery-size-limit finding was accepted and fixed. Initial non-idle DFU
handling now matches the reference's clear/abort followed by a returned error;
it never silently retries an upload.

Cove's build, full test suite and signed public build pass with this published
upload revision pinned. Validation sent no image bytes to a live USB endpoint.

## DFU finalization and reconnect

Apple commit `3e8ad6225` adds `Conn.FinalizeDFU` after a successful upload and
`WaitOpen` for exact ECID/mode reconnection. Raw control or bulk operations
invalidate the upload recorded for finalization. Finalization polls manifestation
states and retires the old connection after reset. A canceled caller stops
waiting, but native resources remain owned until the blocking reset and cleanup
return. `Close` waits for completion before reconnecting.

Reset accepts libusb success or NOT_FOUND (requiring rediscovery), propagating
other errors. Reconnect retries absence and rejects duplicate identities before
claiming an interface. Package race tests pass; no live DFU, restore or boot is
qualified. These primitives still require integration into Cove's restore flow.
