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

## Native ASR and restored identity

`x/iosrestore.SendImage` implements ASR validation and image transfer without
Python. It reads bounded, newline-terminated XML plists without restored's length
prefix, answers checked `OOBData` ranges through `io.ReaderAt`, and sends 128 KiB
payload chunks with optional SHA-1 suffixes. Repeated `Initiate` messages can
renegotiate checksums. Short writes are completed; a write error ends the stream
without retrying a possibly consumed chunk. Context cancellation interrupts
network I/O, and caller ownership of the connection is preserved.

Protocol references are the pinned
[pymobiledevice3 ASR client](https://github.com/doronz88/pymobiledevice3/blob/a16ffc51dcfe2c36fc659fbb2e7b7dedb31d77d5/pymobiledevice3/restore/asr.py)
and [idevicerestore ASR implementation](https://github.com/libimobiledevice/idevicerestore/blob/60192e97f87d1bbab5c493684e0a245b0966363f/src/asr.c).
The latter handles repeated checksum negotiation. Neither payload completion nor
connection EOF proves that the device completed a restore.

`x/iosrestore.QueryInfo` queries `com.apple.mobile.restored` and its hardware
`UniqueChipID`. It preserves all 64 ECID bits, including signed plist producers.
Cove's `ios restore probe -ecid N [-udid SERIAL]` checks USB candidates against
that hardware value, rejects duplicate matches and incomplete probes, and closes
all probe connections. Serial filtering never substitutes for an ECID match.

Tests exercise fragmented/coalesced control messages, random OOB ranges, checksum
renegotiation, a known SHA-1 vector, short I/O, malformed inputs, cancellation,
high-bit ECIDs and ambiguous discovery. Live ASR transfer, personalization,
StartRestore dispatch and device completion/status handling remain unqualified
or unimplemented. This does not complete the F09 restore requirement.

Published dependency: Apple `e80f461d48303e88911e95feff15ba820b8a5507`, pinned
as `v0.6.19-0.20260907170253-e80f461d4830`. The signed Cove CLI ran
`ios restore probe -ecid 1 -timeout 10s` and returned the expected no-match error.
This exercises the host discovery path; it does not qualify a connected restored
device or an ASR transfer.

## IMG4 container assembly

Apple `x/img4.Personalize` combines complete IM4P and IM4M DER objects, optionally
retags the payload FourCC, and encodes IM4R restore properties. It preserves
payload encoding, optional IM4P fields such as PAYP, and the supplied ticket
bytes. Restore properties support integer, boolean and octet values; octets are
already in wire order and are never reversed by this API. In particular, Cove's
future TSS adapter must apply the pinned BNCN convention explicitly.

The format was checked against
[PyIMG4's encoder](https://github.com/m1stadev/PyIMG4/blob/80491329a41ff90f1df1a2bcaa122d190db94bf3/pyimg4/parser.py)
and the pinned [pymobiledevice3 component adapter](https://github.com/doronz88/pymobiledevice3/blob/a16ffc51dcfe2c36fc659fbb2e7b7dedb31d77d5/pymobiledevice3/restore/img4.py).
Synthetic DER vectors cover the IMG4 envelope, high-number private tags, nonce
slots, large unsigned values, payload-field preservation and length boundaries.
OpenSSL independently parses the `anid=2` IM4R vector.

This API checks outer container structure, not ticket authenticity or hardware
eligibility. Build/ECID/nonce binding, component-name mappings, TSS `-TBM` handling,
nonce-slot policy, complete restore dispatch and real-device qualification still
belong to the pending Cove restore controller. Container assembly alone does not
complete image personalization acceptance or F09.

Published IMG4 implementation: Apple
`63f5d90dbc4661a5258a7d58b66bd6b297cbe71f`, pinned by Cove as
`v0.6.19-0.20260907171849-63f5d90dbc46`. The 20-second fuzz run completed
2,694,163 executions with no failure, alongside passing golden and race tests.

## Ticket assertions and component policy

Apple `x/img4.ParseManifest` reads MANB/MANP and image properties from signed-shape
IM4M containers. It rejects duplicate names, mismatched private tags, unsupported
scalar types and integers outside `uint64`. Byte values are copied. Parsing does
not verify a signature or certificate chain; synthetic tickets with empty
signature/certificate fields are deliberately used in tests.

Cove's `internal/ios/restore` owns the pinned component policy:

- `Personalize` uses the pymobiledevice3 component tags, including `OS\0\0`;
  unknown names retain their IM4P type.
- `RequiresNonceSlot` applies to SEP, SepStage1 and LLB. Observed AP parameters
  override manifest Info, including an observed zero. Defaults are SEP slot 2
  and AP slot 0. Duplicate nonce-slot properties in component TBM are errors.
- BNCN must be eight bytes and is reversed into IM4R wire order on a copy.
- `MatchTicket` compares asserted ECID, board/chip IDs, current AP/SEP nonces and
  explicitly supplied signing-build image digests. High-bit ECIDs remain intact.
  A ticket SEP nonce without an observed counterpart is rejected. A missing SEP
  nonce is allowed only when both the requirements and ticket omit it.

These rules follow the pinned
[pymobiledevice3 adapter](https://github.com/doronz88/pymobiledevice3/blob/a16ffc51dcfe2c36fc659fbb2e7b7dedb31d77d5/pymobiledevice3/restore/img4.py)
and [PyIMG4 property parser](https://github.com/m1stadev/PyIMG4/blob/80491329a41ff90f1df1a2bcaa122d190db94bf3/pyimg4/parser.py).
`MatchTicket` compares BNCH directly with the observed AP nonce; it does not hash
that nonce again. The caller must derive requirements independently from current
device observations and the selected signing manifest. Supplied digest equality
is not a check of patched payload bytes or proof of build provenance.

Tests cover the wire output, slot precedence, duplicate/malformed properties,
input immutability and mismatched hardware/nonces/digests. Parser race tests and a
20-second fuzz run (2,772,220 executions) pass. No real signed ticket is qualified.
The adapter is not yet connected to an end-to-end restore controller. TSS request
construction, authenticated ticket provenance, security-policy eligibility,
selected-build extraction, patched-payload policy and nonce-change reacquisition
remain required. F09 remains incomplete.

The parser is published in Apple commit
`868ca1d5aa1c745aa56406b8a1bdca2801647bf1`, pinned by Cove as
`v0.6.19-0.20260907174357-868ca1d5aa1c`.
Cove's full `go test ./...` and `go build ./...` gates pass against this published
pin. Standard and research CLI binaries build and are re-signed with their
respective entitlements. These host checks do not qualify guest restore or boot.

## Native AP signing request path

`restore.APSigningRequest` derives an AP IMG4 request from one selected
BuildIdentity and current device observations. It requires matching board/chip
IDs, a nonzero ECID, AP nonce, security domain, UniqueBuildID and explicitly named
next-stage components with nonempty signing digests. The request contains all
eligible AP entries; ticket checks cover the named next-stage components.
`Info.Img4PayloadType` supplies a ticket tag when present, with the pinned component
mapping as fallback. Inputs, request data and matching requirements do not alias.

The builder copies the pinned AP manifest fields, renames ApSepNonce to SepNonce,
applies the RequiresUIDMode/SikaFuse workaround when requested, and handles
NeRDEpoch/PermitNeRDPivot. It strips component Info, skips the pinned non-AP,
Cryptex1 and FTAB entries, and supplies empty digests for trusted entries that
lack one. Empty digests cannot establish next-stage ticket matching. Trusted
components without RestoreRequestRules are included with EPRO/ESEC from the
request policy, following libtatsu; untrusted components without rules are
skipped. This fixes an omission in the pinned Python request builder.

RestoreRequestRules compare typed boolean values, including false. This follows
[libtatsu's value comparison](https://github.com/libimobiledevice/libtatsu/blob/e7d6ad13ef928aa609d0ccdfc586f7d6e8e049bf/src/tss.c#L448)
rather than the pinned Python implementation's false-value short-circuit. Unknown
conditions and malformed rules are errors. Boolean actions are applied in rule
order; integer 255 is ignored. These checks deliberately surface unsupported
manifest policy instead of silently dropping it.

`restore.SignAP` connects request construction to the native HTTPS TSS transport
and rejects missing tickets or mismatched identity, nonces, security policy and
requested signing digests. Ticket requirements now support SDOM, CPRO and CSEC
assertions; the signing path always requests these checks. This checks returned
assertions and relies on trusted HTTP/TLS configuration for source provenance;
it does not verify the ticket's cryptographic signature or patched payloads.
Redirects must remain on HTTPS gs.apple.com, on the default port or 443. The
supplied client's settings remain unchanged; stricter caller redirect callbacks
are honored.

Fixture and HTTP integration tests cover request derivation, false-valued rules,
input isolation, filtering, UID policy, high-bit ECIDs, invalid builds, stale
nonces, wrong security policy, server rejection and missing tickets. No real TSS
request or signed ticket has been qualified. Complete restore dispatch, automatic
device observation/reacquisition, offline ticket persistence and separate
recovery-root, local-policy, baseband and coprocessor flows remain required.

Final validation for this path: focused race tests, `go test ./...`,
`go build ./...`, and re-signed standard/research CLI builds pass after the
trusted-component correction. Notebook review approved this checkpoint only.
