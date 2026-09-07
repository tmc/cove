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
`ios-device-packages` branch at `3d33fff2e278`. Cove now pins
`v0.6.19-0.20260907142414-3d33fff2e278`; its temporary `go.work` has been removed.
The configured GitHub remote is reachable using a command-local URL rewrite
override; no global Git configuration was changed.

Tests cover daemon framing, port byte order, connection ownership, cancellation,
malformed response lengths, restore framing, TSS errors, recovery serial parsing,
USB timeout bounds, closed handles and invalid VZ inputs. Cove's existing identity
and preflight tests exercise the extracted VZ helpers through their callers.

## Remaining work

These are working primitives, not a completed replacement for pymobiledevice3.
Still required: raw DFU transfer sequencing and recovery transitions, pairing and
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
