# Spec: full vphone feature coverage in cove

Status: implementation proposal, revision 2. Authored 2026-09-06. No iOS runtime
feature is declared implemented by this document.

## Scope and completion rule

Implement the observable features of vphone-cli at
`87f796c62a7cb385cd37afce121f6e222d83e5b5` in cove, using cove's VM lifecycle,
CLI, control API, and GUI. This includes research features, firmware variants,
standalone patch commands, guest services, and desktop tools. First boot is an
intermediate milestone, not the final scope.

Cove's implementation baseline is `c6a6f5f441958583fd97ee8a161f1c8e0f7f01bb`.
The [boot spec](ios-booting-spec-2026-09-06.md) supplies the audited PV=3 device
and binding details. This document extends it and supersedes its deferrals of
image acquisition, GUI parity, and clone behavior for the final deliverable.
A Go port of the patch engine is not required: a pinned external engine is an
implementation choice, with cove responsible for its complete integration.

Every feature below needs a cove entry point, implementation owner, dependency
contract, error behavior, and acceptance evidence. Features conditional on a
firmware profile or host facility must be implemented and tested on an eligible
profile, with truthful unavailability elsewhere. A generic disabled button or
an indefinite future investigation does not constitute parity for a working
upstream feature. For upstream stubs or unhandled commands, parity means an
explicit, tested unsupported result; implementing the missing upstream feature
is a separate extension (F29 foreground and F35 accessibility).

Plan approval means these decisions and tests are sufficient to start and order
implementation. Product completion additionally requires passing those tests;
NotebookLM's approval is not evidence that cove can boot iOS.

## Evidence and source conventions

`VP:path` means a file at the pinned [vphone source revision][vphone]. Paths in
the tables are repository-relative; the source index at the end groups the
entry points. Read active code before README or historical research examples.
CPID is a prediction constant in `VPhoneHardware`, not a descriptor setter.
The counts of patches in README/AGENTS are not a stable behavioral contract.

The source audit includes root/subcommand registries, all active menu files,
`VPhoneHostControl`, `VPhoneControl`, guest command dispatch, bundle/orchestrator
helpers, build scripts, and their tests. Uninitialized resource/vendor submodules
and binary assets are absent from the notebook corpus. Implementation must
resolve the recorded gitlinks and hashes before depending on those artifacts.
The pinned apple module was audited locally in the boot spec; its runtime ABI
and the guest's firmware-dependent behavior remain integration-test obligations.

Existing cove foundations are reuse candidates, not proof of iOS support:

| Foundation | Existing cove files | iOS work required |
| --- | --- | --- |
| CLI and VM planning | `cmd/cove/command_registry.go`, `vmrun_adapter.go`, `internal/vmrun/{config,plan}.go` | Add `GuestIOS`, options and dispatch throughout; reject conflicting OS/start modes. |
| Bundle identity/config | `internal/vmconfig/{config,detect,registry,paths}.go`, `cmd/cove/covevm_bundle.go` | Explicit iOS config before Mac marker detection; new state/artifact validation and Finder routing. |
| Private VM operations | `cmd/cove/runtime_private.go` | Enable iOS Mac-style start options and propagate setter errors; attach the full device graph. |
| Control and operations | `cmd/cove/control_socket.go`, `control_http.go`, `control_mcp.go`, `internal/control/operations` | Typed iOS operations, capability reporting, authorization, progress and cancellation. |
| Display/input/capture | `cmd/cove/gui_control.go`, `control_host_input.go`, `screenshots.go`, `screenshots_private_darwin.go` | Phone coordinate transforms, multitouch, hardware keys, capture and rendering verification. |
| Agent and files | `cmd/cove/agent_control.go`, `agent_control_attach.go` | Separate vphoned transport; do not send cove's existing agent RPC protocol to it. |
| Recording artifacts | `cmd/cove/recording_cli.go`, `internal/runs` | Existing list/export handles run artifacts; continuous H.264 video capture is new work. |
| Image lifecycle | `cmd/cove/clone.go`, `internal/vmidentity`, `internal/ociimage`, `internal/vmtree` | Extend file sets to include all iOS state and firmware; define clone identity semantics explicitly. |

## Implementation boundaries

Use Go for the cove host and retain the upstream iOS guest daemon initially.
Do not create a second Swift VM owner. One cove runtime owns the VZ instance,
serial pipes, guest/control/camera connections, capture queue, and bundle lock.

| Owner (proposed unless listed above) | Responsibility |
| --- | --- |
| `cmd/cove/ios.go`, `ios_commands.go`, `ios_gui.go` | Thin CLI/GUI dispatch and VZ object construction; all controls call the same services. |
| `internal/iosbundle` | Versioned iOS config, adapter workspace, durable stages, clone/import/export and identity/state checks. |
| `internal/iosfirmware` | Toolchain resolution, catalogs, prepare/patch/restore/CFW process execution and artifact manifests. |
| `internal/iosguest` | Typed vphoned client over `io.ReadWriteCloser`, framing, requests, transfers, capabilities and reconnect. No AppKit dependency. |
| `internal/ioscontrol` | Map guest/host operations to cove control responses, host-socket compatibility and ordered action/capture execution. |
| `internal/iosmedia` | Capture/video encoding, frame producers and the separate camera stream; bounded queues and cleanup. |
| `internal/ioshost` | Host location/power/Touch ID subscriptions with explicit enable/disable and main-thread delivery. |
| `scripts/build-ios-tools.sh` and toolchain manifest | Build pinned vphoned and required guest helpers; stage signed, checksummed artifacts outside the cove executable. |
| apple binding generator / vzkit | Add missing typed private wrappers and runtime probes, with errors instead of ad-hoc selectors scattered through cove. |

Use concrete clients with small methods and typed requests; introduce an
interface only at an actual transport/test seam. Streaming transfers accept
`io.Reader`/`io.Writer`. GUI models observe operation state and capabilities;
they do not implement separate install, transfer, or restore engines.

All commands below are proposed cove syntax. Reuse existing root verbs when
their semantics match. Put iOS-specific provisioning/research verbs under
`cove ios`; expose runtime features under `cove ctl -vm NAME ios ...` and through
HTTP/MCP using the same operation handlers. Additive protocol fields must not
change existing macOS/Linux/Windows behavior.

## Feature ledger

Each row is an eventual requirement. Phase numbers order delivery; they do not
remove later features from scope. All iOS-specific implementation rows start
**not demonstrated**. “Reuse” identifies an existing cove subsystem that still
needs an iOS adapter and tests.

### Library, setup, firmware and lifecycle

| ID / phase | vphone surface and source | Cove implementation and behavior | Acceptance |
| --- | --- | --- | --- |
| F01 / 1 | `setup`; `VPhoneSetupCLI`, `VPhoneResources`, build/setup scripts | `cove ios setup` resolves pinned host tools, Python environment, resources, guest helpers and optional rebuild. Preserve root/resource/venv overrides with explicit precedence. Report paths, versions, hashes and missing prerequisites. | Fresh install, cached offline reuse, corrupt cache, missing SDK and forced environment recreation; no writes inside a signed app bundle. |
| F02 / 1 | `vm list/info`; `VPhoneVMCLI`, `VPhoneBundleReport` | Extend `cove ls/status` JSON and GUI details with iOS/cloudOS versions, variant, CPU, memory, disk, networking, identity and readiness. Warn and skip invalid entries when listing; direct open returns an error. | Empty library, mixed OS types, damaged manifest, stable JSON and unavailable identity. |
| F03 / 1 | `vm new/config`; `VPhoneVMCLI`, manifest and launch layout | `cove ios new NAME` creates only a blank bundle; `cove ios config NAME` edits CPU/memory, display, network and ROM overrides while stopped. Support manifest-relative and absolute input paths; normalize staged references to bundle-relative paths. | Defaults 8 CPU/8192 MiB/64 GiB are profile defaults subject to host bounds; invalid limits/paths and mutation while running fail. |
| F04 / 1 | VM and firmware terminal pickers; `VPhoneVMSelection`, `VPhoneVMPicker`, `VPhoneFirmwareSelection` | Interactive selection when names/images omitted on a TTY; noninteractive mode requires an unambiguous target or explicit input and never blocks for selection. | Cancel, no candidates, one/many candidates, invalid input, JSON mode and piped invocation. |
| F05 / 2 | `fw catalog`, `fw prepare --list/--iphone-version/--iphone-build`; firmware catalog/picker | `cove ios firmware catalog/prepare`: list tested pair profiles and downloadable inputs separately, accept local paths or URLs, resolve exact versions/builds and record sources. Never equate downloadable with compatible. | Version/build conflict, unavailable build, offline cache, explicit pair, duplicate candidates, JSON and picker parity. |
| F06 / 2 | `fw_prepare.sh`, `fw_manifest.py` | Delegate download/extract/merge and hybrid BuildManifest/Restore generation to the pinned toolchain in staging. Cove verifies profile, tree completeness and digests before publication. | Interrupted/resumed download, wrong pair, truncated archive, manifest/component mismatch, repeat preparation and matching ROM outputs. |
| F07 / 2 | `fw patch`, `patch-firmware`; `FirmwarePipeline`, `VPhoneCLI` | `cove ios firmware patch`: all five variants; quiet/verbosity, patch-record output, force-EXC_GUARD, Frida, no-binpack/no-vphoned where supported. Pass typed options to the matching upstream entry point rather than silently dropping unsupported high-level flags. | Test valid/invalid variant-option combinations and fixture outputs/patch records for each supported input profile. |
| F08 / 2 | `patch-component`; `PatchComponentCLI` | `cove ios firmware patch-component` delegates txm, kernel-base and diagnostic kernel-jb with input/output, records, quiet, kernel-jb target-OS and Frida options from the pinned parser. Separate diagnostics from production patch-all. | No in-place input mutation; deterministic fixture output and exact records; missing input, invalid target OS and unknown component fail. |
| F09 / 2 | `restore --get-shsh/--offline/--ecid/--udid`; restore CLI/bridge | `cove ios restore` supports separate ticket acquisition, online erase restore, explicit offline tickets and AEA staging. Bind all operations to observed identity and the selected restore tree. | Wrong ECID/UDID, multiple connected devices/trees, nonce mismatch, DFU vs recovery, disconnect/reconnect, decryption/key failure and cancellation. |
| F10 / 2 | `cfw install`; `cfw_install_host.sh`, variant scripts | `cove ios cfw install` runs the selected privileged recipe with typed flags for spoof-build, force-DSC-maxslide, retained artifacts and elevation method. Stop and release VZ disk handles before mounting; detach every mount before returning. | Each non-less variant; injected mount/patch/sign/rename/unmount failure leaves explicit recoverable state, logs and no competing disk writer. |
| F11 / 2 | `vm create`; `VPhoneCreateOrchestrator`, boot patterns | `cove install -ios` composes blank creation, pair resolution, prepare, patch, DFU restore, variant-specific CFW, first-boot commands, finalization and readiness. Support interactive/unattended and keep-artifacts. | One command from explicit raw inputs to normal boot on every variant, plus resume after each stage boundary and repeat boot. |
| F12 / 1–2 | `vm launch/stop`, low-level `boot`; launch CLI, managed process, app delegate | `cove run` detects iOS; support headed/headless, DFU, explicit variant, config path, guest-binary override, no-vphoned and AP debug port. `cove stop` has bounded graceful stop then forced termination. | Double launch rejected; PID reuse/stale lock handled; signals terminate owned children and close sockets; GUI close behavior explicit; options survive relaunch. |
| F13 / 3 | `vm rename/delete`; bundle ops | Reuse `cove rename/rm`, with live-VM rejection and atomic index/alias updates. Deletion confirmation can be bypassed by the existing explicit force mode. | Target collision, path traversal, missing VM, running VM and failure halfway through rename. |
| F14 / 3 | `vm clone`; `clone/resetIdentity` | Extend `cove clone` to CoW the full iOS bundle, falling back to streamed copy. Fresh machine identity/MAC by default; reset NVRAM and tickets as specified below. | Parent unchanged; new ECID/MAC; no stale PID/socket/SHSH; parent and child independently restore/boot on the qualified profile. |
| F15 / 3 | `vm export/import`; transfer CLI and bundle ops | `cove export/import -format vphone` implements default zstd and maximum xz, archive autodetection, optional restore assets, inferred name and existing-directory output behavior. Translate manifests in a staging directory. | Round-trip stopped state and sparse disks; byte digests after decompression; import renamed archive; malformed/traversal archives and destination collision rejected. |
| F16 / 1 | `VPhoneVerbosity`, resources/library roots | Keep cove logging style while mapping vphone tool detail, serial and internal traces separately. Support resource/venv/cache overrides without exporting credentials to logs. | Default vs verbose vs trace output; noninteractive progress; subprocess diagnostics retained with secrets redacted. |

### VM graph, guest protocol and desktop/automation features

| ID / phase | vphone surface and source | Cove implementation and behavior | Acceptance |
| --- | --- | --- | --- |
| F17 / 1 | VM/hardware model/manifest files | Implement the complete PV=3 graph in the boot spec: Mac platform/ROM/graphics, virtio disk/network/audio/entropy, optional vsock, PL011, keyboard/touch, SEP, synthetic battery, accelerators and AP/SEP debug stubs. Preserve the profile's device presence/order. | Constructor/ABI and selector probes; nil/error handling; config validation; correct DFU ECID and normal boot with the audited graph. |
| F18 / 1 | Entitlements, app packaging and preflight | Fix stale public signing paths; explicit research signing across CLI, autosign, app bundle and helpers. Probe policy, effective entitlements and host/device support independently. | CLI and Finder launch signed correctly after relaunch; unsupported/nested host produces actionable error; release profile excludes private keys. |
| F19 / 3 | `VPhoneControl`, `vphoned.m/protocol` | New vphoned client, version/capability handshake, ping/version/IP/iOS metadata, bounded requests, reconnect and cancellation. Separate it from existing cove guest-agent RPC. | Fragmented reads/writes, concurrent calls, response ID matching, missing capability, timeout, malformed payload, disconnect and stale callback tests. |
| F20 / 3 | `loadGuestBinary/pushUpdate`, daemon build/launchd | Build and stage pinned signed daemon; hash handshake and version-qualified update except on less. Install/update matching guest helpers and resources via recipe. | Same hash skips update; approved new hash restarts/reconnects; bad hash/truncated binary rejected; less never receives an update. |
| F21 / 3 | `VPhoneKeyHelper`, virtual machine view | Native USB keyboard + private touch events; guest HID/touch fallback per profile. Implement home, power, volume, Spotlight, press/down/up, mouse drag/swipe/right-click Home and clipboard typing. | Press/release balance, modifier handling, drag/cancel, right click, scale/coordinate edges and guest-touch vs native-touch profiles. |
| F22 / 4 | Touch ID monitor/menu | Opt-in physical sensor presence forwarding as Home single/double-press, with capability/entitlement probes, active-window gating, reconnect and preference persistence. This is not guest biometric authentication. | Sensor absent, permission denied, non-key window, repeated presence callbacks, double tap and disable/disconnect all handled without stuck gestures. |
| F23 / 3 | Window controller/view/menu | Phone viewport, scale/resize, keyboard shortcuts, title/status, toolbar/menu controls and File/App/Keychain windows. Share runtime services; disable controls with a reason when a capability is absent. | Retina and resized windows, coordinate round-trip, multiple VM windows, minimize/reopen and headless/headed transitions without guest restart. |
| F24 / 3 | Screen recorder + Record menu | Screenshot to clipboard/file, explicit full-resolution capture and compact images; H.264 MOV start/stop capture with monotonic timestamps and bounded frame production. Register outputs with cove run artifacts. | Correct orientation/resolution, valid playable MOV, no overlapping capture requests, empty recording and disk-full cleanup; separate visible/offscreen capture tests. |
| F25 / 3 | `VPhoneHostControl` | Optional `vphone.sock` JSON-line compatibility endpoint translating screenshot/tap/swipe/key/type into cove services; cove API/HTTP/MCP also exposes them. Preserve response shape and optional action screenshot behavior. | Upstream-compatible request fixtures and a socket client; sequential action/screenshot ordering, finite coordinates/durations, unsupported method, disconnect and timeouts. |
| F26 / 3 | `VPhoneFileBrowser*`, `VPhoneRemoteFile`, guest files | GUI list/search/sort/breadcrumb/history/refresh, recursive uploads/downloads, mkdir/delete/rename, drag/drop and progress; map `cove cp` and new `ctl ios file` commands to the same client. | Metadata/perms/symlinks, zero-length and large files, names with spaces/Unicode, partial transfer cleanup, collision, cancellation and reconnect. |
| F27 / 4 | `VPhoneQuickLookController`, file browser | Download selected remote preview into managed temporary storage; Quick Look panel, navigation and host drag-out promises. Evict previews when closed or expired. | Preview changed/deleted file, cancel during fetch, unsupported format, repeated previews and cleanup after process exit. |
| F28 / 3 | `VPhoneInstallPackage`, `installIPAWithBuiltInInstaller`, `vphoned_install.m` | IPA/TIPA picker and drop install plus `ctl ios app install`; stream archive/certificate to guest staging and invoke the guest built-in signing/registration path. Support version-specific registration helpers. | Both suffixes/case variants; embedded frameworks/extensions; missing certificate/helper, signing/registration failure; verify app launch and remove all staging artifacts. |
| F29 / 3 | App browser/control/guest apps | App list/filter all/user/system/running, search/sort, launch with optional URL, terminate, capability-gated foreground query, app metadata and Open URL dialog/API. | Known app lifecycle, unknown bundle, system app denied operation, explicit unsupported foreground response without disconnect, malformed URL and disconnect refresh behavior. |
| F30 / 4 | Keychain browser/control/guest keychain | Guest-only class-filtered inspection/search/detail views with diagnostics and test-item creation; `ctl ios keychain list/add`. Preserve binary fields without lossy conversion and handle locked/unavailable classes. | Fixture items in each supported class, duplicate add behavior, inaccessible items reported, no host-keychain access and no secret values in routine logs. |
| F31 / 3 | Connect menu, clipboard methods and guest clipboard | Text and image clipboard get/set, supported type metadata, change counter and host paste action. Distinguish setting clipboard from synthesizing paste/type keys. | Unicode, empty text, image binary transfer, unavailable pasteboard, large input, types/change counter and stale UI state. |
| F32 / 4 | Location provider/menu/guest location | Explicit host-location forwarding and stop, authorization status, persisted enable state and manual coordinate API with altitude, accuracy, course, speed and timestamp. | Denied location, stale sample, invalid coordinates, guest reconnect and disable/VM stop ending simulation. |
| F33 / 4 | Battery menu, synthetic source, guest notify | Manual charge/connectivity and host battery sync; separately propagate host low-power mode and expose manual low-power command. | 0/100%, invalid value, desktop with no battery, charging/unplugged transitions, disable cleanup and capability-gated low-power notification. |
| F34 / 4 | Settings get/set, Connect menu | Typed domain/key read/write and domain listing through the guest, including boolean/integer/float/string/data/null and returned plist/date values. | Type-preserving round trip, missing key, invalid type/data, failed preference synchronization and reconnect. |
| F35 / 4 | `accessibility_tree`, accessibility guest helper | Expose `ctl ios accessibility tree -depth N` and API/MCP dispatch; the pinned guest returns its not-implemented error. Report unsupported; a real tree is a separate guest extension, with no OCR substitute. | Recorded stub response maps to structured unsupported error and nonzero CLI exit, without timeout/disconnect or invented tree; a future extension needs depth/size/cycle tests and real UI evidence. |
| F36 / 3 | Devmode status/enable and menu | Status inspection and explicit enable operation with reboot-required result; share status with UI readiness. | Enabled/disabled/unsupported profiles; enable failure and post-reboot status verification; never auto-enable merely to answer status. |
| F37 / 4 | Camera menu/server/frame producers, guest vcam | Off/test pattern/video-file source, start/stop, looped video frames, separate vsock 1338 stream and required guest camera hook/resource. API and menu expose source and connection state. | Guest Camera sees pattern/video; EOF loops, source switch, invalid video, backpressure, receiver restart and stop clearing stale output. |
| F38 / 2–4 | Binpack/CFW resources and daemon recipes | Preserve variant-specific SSH/VNC, rpcserver, Sileo/TrollStore/Procursus finalization, tweak loader and optional Frida installation as pinned recipe outputs. Publish actual endpoint/credential configuration and completion state. | Each selected service responds; first-boot finalization exits successfully before ready; no-binpack/no-vphoned omit those components; opt-in Frida server version matches recorded artifact. |
| F39 / 1–4 | AP/SEP debugger, serial, boot logs, build hash | Reuse cove GDB/serial controls; expose both AP and SEP endpoints, build/guest versions, boot analysis, patch records and support logs. | Port collision/range, debug-disabled profile, debugger attach to AP/SEP, serial flow without deadlock, panic detection and redacted support export. |

## Bundle, profile and toolchain contract

`config.json` retains cove hardware fields and adds `ios` with `schemaVersion=1`,
`profile`, `variant`, `firmwareDigest`, screen geometry/scale, boot arguments,
network/MAC policy and guest-service preferences. The profile fixes PV, board,
ISA, CPID, expected device graph, firmware pair and supported switches. Persist
explicit values; reject unknown schema/profile/variant instead of guessing macOS.

Keep `hw.model`, `machine.id`, `aux.img`, `sep.img`, `disk.img`, AP/SEP ROMs,
`firmware.json`, `restore-state.json`, and per-attempt logs together. Runtime
sockets, locks, PID files, camera buffers and preview caches are ephemeral.
An adapter workspace maps cove files to upstream `config.plist`, `Disk.img`,
`nvram.bin`, `SEPStorage`, ROM names and exactly one `iPhone*_Restore` tree.
It references the same authoritative identity and disk; never create a second
VM owner or allow a tool to silently create a replacement machine identifier.
The JSON/plist translation has round-trip tests including network/display fields.

`firmware.json` is versioned and records exact iPhone/cloudOS product/build IDs,
source hashes/URLs, hybrid identity, ROM/payload digests, upstream/tool/resource
commits, Python lock digest, patch options/records, selected guest artifacts,
expected services and recipe ID. A source catalog is not a signing guarantee.
Resolve TSS/nonce requirements at restore time; offline mode requires compatible
tickets, keys, resources and every payload locally.

Use a pinned executable plus explicit argv/env/cwd, not shell interpolation.
`internal/iosfirmware` defines operations `Catalog`, `Prepare`, `Patch`,
`PatchComponent`, `GetTicket`, `Restore`, `Customize`, `FirstBoot`, and `Inspect`.
Each operation accepts a context, typed input and output/log writers, returns
artifact metadata and a structured stage error, and has a default/configurable
deadline. Process groups are owned and reaped on cancellation. Bound log storage;
retain the tail and full artifact path instead of exhausting memory.

The adapter initially invokes vphone's existing CLI/scripts for firmware and
CFW but not its VM-launch commands. For orchestration, replace the upstream
launcher dependency with cove-controlled stage calls and serial input. Pinned
patch execution must preserve each component's transformations and record them;
merely starting `fw patch` is not acceptance. A conformance fixture records
component input/output digests and PatchRecord JSON for each variant, including
DSC code-directory updates, cryptex/APFS snapshot changes, daemon plists and
version-specific registration/camera helpers produced by the upstream recipe.
Do not implement binary patch algorithms in this spec or infer success from a
constant number of patches.

Tool setup resolves all `.gitmodules` gitlinks, the Python dependency lock,
Xcode/iOS SDK, native libraries, patch executables, guest daemon, registration and
camera helpers, signing material and resource bundles into a checksummed local
store. Capture actual versions rather than an unconstrained `>=` requirement.
Allow external user-managed tools only with an explicit manifest and successful
contract probe. Custom paths do not bypass verification. Do not distribute
firmware archives or credentials inside the public cove executable.

### Variant and create-state behavior

All five variants are supported; do not conflate their create flows:

| Variant | Required flow |
| --- | --- |
| less | Upstream's less prepare/patch/boot profile, with no-binpack/no-vphoned switches; no regular host-CFW stage or daemon self-update. Dedicated root/boot requirements must be tested. |
| regular | Standard prepare/patch/restore/CFW/first-boot recipe and its selected services. |
| dev | Standard flow plus the development/TXM and rpcserver recipe. |
| jb | Standard flow plus jailbreak resources and guest finalization; optional Frida. |
| exp | JB superset plus selected experimental/DSC/build-spoof changes; optional force-maxslide and Frida. |

Validate options against the pinned entry point. In particular, standalone
`cfw install` accepts four non-less variants whereas create/patch accepts five.
No-binpack and no-vphoned are less-only patch controls; Frida kernel options are
jb/exp-only. Record force-EXC_GUARD independently of profile-mandated patches.
Keep original build identities separate from an explicitly spoofed display build.

Persist successful transitions atomically:

```text
blank -> prepared -> patched -> dfu-observed -> restored
      -> customized-or-profile-skip -> first-boot-configured
      -> guest-finalized-or-profile-skip -> boot-verified
```

Persist attempt ID and input/output hashes per stage. A stage is reusable only
when those inputs match and its live prerequisites have been rechecked. The
`dfu-observed` checkpoint never substitutes for current ECID/mode discovery.
A failure preserves the disk and the last completed stage. Restart requires an
explicit erase decision if it would destroy previously restored state.

For profiles with binpack installed, first boot runs the pinned `VPhoneBootPatterns.firstBootCommands` via the serial
channel after a positive prompt, then waits for the expected shutdown and starts
again. Bound prompt/shutdown waits and detect panic or early exit. Cove does not
copy upstream's timed-continue fallback as proof of success. Less skips host CFW. A no-binpack profile skips binpack-dependent serial
bootstrap commands and records that profile skip; use its boot/UI evidence
instead. A less profile retaining binpack must separately qualify its serial
prompt and bootstrap commands. JB/exp guest
finalization must report a durable completion marker and versioned log before
those services are declared ready. Keep source archives by default; purge built
restore/CFW intermediates only after their last successful consumer unless
keep-artifacts is enabled. Retention never deletes authoritative VM state.

Before mounting, persist an attempt-scoped mount journal with device identifiers
and ownership. Normal return and cancellation detach owned mounts and verify
detachment. On restart after a kill/crash, reconcile the journal against current
host mounts; refuse boot or another writer until cleanup is confirmed. Never
detach a mount merely because its path resembles a previous attempt. Test a
killed helper between attach and detach, then recovery before the next stage.
A Go defer or shell EXIT trap alone cannot guarantee cleanup after process death.

Use cove's bundle lock across the full create/restore operation. Elevated helpers
receive only the required paths and operation arguments; passwords use an
interactive credential channel/OS authorization, never command-line flags or
persistent config. Offer a native authorization dialog equivalent to root-popup.
Automated installs use an already provisioned authorization mechanism or fail
with an explicit requirement. This replaces upstream's password argument while
preserving unattended operation on a prepared host.

### Clone and transfer semantics

Normal restart preserves identity, NVRAM, SEP and disk. Corrupt or missing
identity in a restored VM fails; do not regenerate it implicitly. Backup/export
preserves the complete consistency set and imports it as the same device, with a
check preventing simultaneous use of duplicate identity in cove's registry.

Fresh-identity clone matches upstream's observable intent: CoW/copy all state,
generate a new machine ID and MAC, reset auxiliary storage, remove predicted
identity and tickets, regenerate adapter manifest and invalidate readiness.
Retain the copied disk/SEP only for a profile qualified by the clone test. The
implementation experiment compares that exact upstream reset against a cloned
restore when retained SEP state is incompatible; the profile records the working
strategy and reprovisioning requirements. No clone is published as ready until
new identity, guest state and independent parent/child boot have been verified.
This is a required qualification gate, not an indefinite deferral of F14.

Import extracts into a fresh staging directory, rejects absolute/traversal paths,
escaping symlinks, special device nodes and missing required artifacts, validates
space and hashes, translates the plist, then atomically publishes. Export uses
stopped consistent state, excludes runtime/transient files and restore staging
by default, retains sparse-file efficiency where supported and removes partial
archives on failure. Support upstream-compatible `.tzst`, `.txz` and legacy tar
formats accepted by its importer; offer explicit include-restore assets.
Cove's native image format must retain the same consistency set rather than
exporting only `disk.img`. Save/resume is a cove extension, not claimed upstream
parity; gate it independently while keeping stop/restart fully implemented.

## Guest and host protocol contracts

### vphoned compatibility

Port 1337 is the existing vphoned control channel. Frames are a 32-bit
**big-endian** byte count followed by UTF-8 JSON. Requests use `v=1`, `t`, and
unique string `id`; hello supplies the host binary hash and returns guest name,
version, IP, iOS version, capabilities and optional update requirement. Certain
messages carry a subsequent raw binary body, not base64 JSON. Preserve exact
upstream field names and fixture bytes in the adapter.

One writer serializes each header-plus-body transaction; one reader routes
responses by ID and drains exactly the declared raw body. Store sizes as integers
without float64 truncation; reject overflow before allocation. Proposed defaults:
4 MiB JSON maximum (matching the pinned guest), 1 GiB configurable per-file limit with streamed bodies,
8-second handshake, 10-second ordinary request, 30-second slow operation and
180-second transfer idle deadline. Total transfer deadline is configurable and
separate from idle timeout. A framing error closes the connection and fails all
pending requests. A semantic error with a body either drains it or closes; never
parse leftover binary bytes as the next JSON message.

Reconnect after 3 seconds with bounded backoff and attempt generation IDs;
ignore late callbacks from old sockets. Cancelled operations stop host work,
report uncertain guest side effects where the upstream protocol cannot cancel,
and reconcile before retry. Never replay install/delete/keychain/setting/update
mutations automatically after an ambiguous disconnect.

Expose all guest operations, not only those used by menus:

| Wire operation group | Typed cove client / runtime surface |
| --- | --- |
| hello, update, ping, version | Connect/status, approved daemon update, health and version APIs. |
| hid, touch | Press/down/up HID and touch phase/coordinates; explicit fallback routing. |
| devmode status/enable | Developer-mode status and enable with reboot-required result. |
| location, location_stop | Start/update/stop simulation with complete sample fields. |
| file_list/get/put/mkdir/delete/rename | Streaming files, metadata and mutations. |
| keychain_list/add | Filtered guest inspection, diagnostic messages and explicit item creation. |
| clipboard_get/set | Text/image payload, types and change counter. |
| ipa_install | Guest-path install, certificate and registration result. |
| app_list/launch/terminate/foreground | App metadata and lifecycle, including errors for unsupported methods. |
| open_url | Guest URL dispatch. |
| settings_get/set | Typed preferences and domain enumeration. |
| accessibility_tree | Pinned stub maps to unsupported; depth-bounded structured output requires a separate guest extension. |
| low_power_mode | Guest notification/state update distinct from synthetic battery level. |

The pinned host requests `app_foreground`, but `VP:scripts/vphoned/vphoned_apps.m`
implements only list/launch/terminate and returns an unknown-command error for
foreground. Preserve an explicit unsupported result against that daemon; a
working foreground implementation is a separately versioned guest extension,
not a prerequisite for matching the pinned upstream behavior. Test the unknown
command response and ensure the GUI does not display invented foreground data.

Likewise, `VP:scripts/vphoned/vphoned_accessibility.m` always returns an `err`
with `accessibility_tree not yet implemented — requires XPC research`. Preserve
that diagnostic with a structured unsupported error and nonzero CLI exit; do
not advertise a working accessibility-tree capability. Both unsupported paths
must leave the connection usable for subsequent supported requests.

Capabilities are not inferred from VZ device presence. Map the hello's actual
`caps` to cove capabilities and record method-specific probes for handlers not
advertised in the upstream caps list (notably accessibility/low-power paths).
Expose availability as supported, unavailable-with-reason, or not-yet-verified.
A profile with no-vphoned retains host-native display/keyboard/serial/debugging
where tested; it must not advertise daemon-only tools.

Build the guest implementation at the pinned commit, including its Objective-C
private API dependencies and entitlements; record its hash in the toolchain
manifest. Changes needed for robustness or new capability reporting go in an
explicitly pinned patch set with host/guest interoperability tests. Daemon
self-update sends only a verified build, waits for acknowledgement and reconnect,
then checks the new hash. A failed update keeps a recovery path through the
stopped-disk install recipe; it never disables access and calls that success.

### Cove API and vphone socket compatibility

The canonical cove API authenticates the selected VM and dispatches typed iOS
operations through the existing control server. HTTP/MCP use the same capability
checks, operation IDs, progress, cancellation and error codes. Do not expose raw
guest filesystem/keychain or camera control through an unauthenticated listener.
Keychain values, signing material and clipboard contents stay out of routine logs.

The opt-in compatibility socket is owner-only `<bundle>/vphone.sock`, with JSON
requests separated by newlines. Preserve the five upstream command names:
`screenshot`, `tap`, `swipe`, `key`, `type`; required arguments, `ok/error/path/image`
response fields, screenshot request options and compact-image encoding must be
covered by transcript fixtures. Actions default to `screen=true` and
`delay=500` milliseconds; swipe uses `ms=300` by default. Compact images are
base64 grayscale JPEG at one-third dimensions. In the socket handler,
`screenshot` writes full resolution only when `path` is supplied and always
requests the compact image. The Desktop default belongs to the GUI save flow;
the socket comment claiming that default does not match its active handler. Preserve `type` as clipboard-set semantics;
a distinct cove paste operation may subsequently emit keyboard paste events.
Do not claim that upstream's type command already types arbitrary text via HID.

Coordinates are guest pixels measured from a declared origin, converted through
one tested transform to view points/normalized touch coordinates. Use the
manifest's current display dimensions; never hard-code the 3× compact screenshot
scale as the input coordinate scale. Serialize action completion, requested
settle delay and capture. Report action success and capture failure separately
where cove's API permits, and preserve legacy error shape in compatibility mode.
Reject non-finite/out-of-bounds coordinates and excessive durations. Headless
operations use a verified offscreen capture/input route or return an explicit
unavailable result; they must not fabricate an active view or successful image.

### Media and host integration

Recording uses the public/native media APIs through Go bindings: capture one
frame at a time, encode H.264 MOV, use monotonic presentation times and finalize
on stop. Fall back among verified cove capture backends without silently changing
coordinate space. Screenshots support clipboard and file output; publish metadata
for resolution, origin and scale. Test output decoding, color/orientation, resize,
minimize, no-view and disk-full cases. Host audio input/output are VM graph
features; do not assert microphone audio is included in a silent screen recording.

Virtual camera has a separate port **1338** and **little-endian** framing:
32-bit payload length, 32-bit JSON header length, UTF-8 header `w/h/bpr/fmt/ts`,
then exactly `bpr*h` BGRA bytes. Start with upstream's 1280×720 at 30 fps,
`fmt=0x42475241`, separate producer/send queues and a latest-frame bounded buffer.
A slow receiver drops stale frames rather than growing an unbounded queue.
Validate stride/length multiplication and reconnect generation. The guest vcam
receiver publishes shared memory consumed by the camera hook; pin/build/stage
both halves and qualify the camera patch recipe. Test pattern and looping
video-file sources are required; a live host webcam is not an upstream feature
and is not substituted for them.

Host location, battery/low-power and Touch ID subscriptions are explicit
per-VM opt-ins with visible state. Stop callbacks and guest simulation on disable,
VM shutdown or lost ownership; reapply only after a compatible guest reconnects.
Touch ID requires the private BiometricKit entitlement and forwards presence to
Home gestures in the focused VM window; it never claims enrollment or successful
guest biometric authentication. Test duplicate callbacks and synthetic release
on focus loss. Host policy/permissions are diagnosed, not silently modified.

File, app and keychain GUI windows use the same typed client as CLI commands.
Remote Quick Look fetches into a permission-restricted cache; preview lifecycle
and drag-out promises use cancellation and cleanup. Recursive traversal detects
symlink cycles and does not follow links outside the user-selected guest subtree.
IPA/TIPA install performs guest-side archive extraction/signing/registration with
pinned helpers, not the obsolete host signer architecture described in some
research notes. Handle framework/extension signing order, profile-specific
LaunchServices registration and cleanup. The binary signing certificate is an
explicit toolchain artifact absent from the notebook's text corpus.

## Delivery and verification

| Phase / owner | Work | Exit evidence |
| --- | --- | --- |
| 1 — runtime + bundle | F01–04, F12/F16 foundations, F17–18, F39 probes | Signed cove runtime creates/opens iOS bundle, validates actual bindings/device graph and enumerates the expected DFU identity. Registry/CLI/Finder regression tests pass. |
| 2 — firmware + provisioning | F05–11, F38 recipes and F39 boot diagnostics | Every variant has prepare/patch/restore and appropriate customization/first-boot qualification. Ticket/identity, stopped-mount and interruption tests pass. |
| 3 — guest + core UI/control | F13–16, F19–21, F23–26, F28–29, F31, F36 | Repeatable create-to-app-launch; file/app/clipboard/developer-mode controls; stable clone/transfer; recorder and compatibility client tests. |
| 4 — extended UI/host/media | F22, F27, F30, F32–35, F37, remaining F38–39 | Every remaining guest/host feature works on an eligible profile; disabled cases explain why. Full feature ledger has acceptance artifacts and no indefinite deferrals. |

Use table-driven tests and `rsc.io/script` for CLI flows with fake tools. Use
recorded wire transcripts for every daemon command and socket compatibility
request, including truncated headers/bodies and out-of-order responses. Use
small redistributable/synthetic fixtures for parsers; do not commit firmware,
private keys, disk images, or build binaries. Exported Go APIs need runnable
examples; benchmarks cover large streaming transfers, video and input latency.

Integration qualification records: cove/upstream/tool/guest hashes; host model,
OS build and entitlements; firmware pair and variant/options; manifest and
patch output hashes; ECID; stage logs; capability probes; UI screenshots; decoded
video/camera evidence; service/registration/finalization results; restart/clone
outcomes. Exercise native-touch and guest-touch firmware profiles, all variants,
NAT/bridged/none, missing host sensors, debug enabled/disabled and no-vphoned/
no-binpack. Use the upstream tested matrix as candidate inputs, not a claim that
cove has passed it. A missing resource requires an explicit setup failure and
qualification task; it is not proof of a missing upstream feature.

Priority is separate from phase: P0 covers wrong identity, state loss, malformed
protocol handling and first-boot blockers; P1 covers repeatability and basic
usability; P2 covers remaining advanced/research parity. All priorities are still
required for full coverage. Do not use a numerical completion score without
passing artifacts for each ledger row.

Before landing implementation, run `go build ./...` and `go test ./...`, sign
emitted VM binaries, and run the opt-in firmware/host matrix. The doc-only audit
previously observed a native `SIGTRAP` in `TestPrivateAPI_NameGetSet`; that is a
baseline test failure, not an iOS acceptance result. Keep planning and actual
qualification results in separate records.

## Source index and review protocol

The following active upstream files establish the inventory (all under [VP][vphone]):

- `sources/vphone-cli/VPhone{CLI,VMCLI,VMCreateCLI,VMLaunchCLI,VMTransferCLI,FWCLI,SetupCLI,RestoreCLI}.swift`: command registry and options.
- `sources/vphone-cli/VPhone{CreateOrchestrator,VirtualMachine,HardwareModel,AppDelegate,WindowController,VirtualMachineView,HostControl,Control}.swift`: lifecycle/device/control graph.
- `sources/vphone-cli/VPhoneMenu*.swift`, `VPhone*Browser{Model,View}.swift`, `VPhoneQuickLookController.swift`, `VPhoneInstallPackage.swift`, `VPhoneKeyHelper.swift`: active desktop surfaces.
- `sources/vphone-cli/VPhone{ScreenRecorder,CameraServer,FrameProducer,LocationProvider,TouchIDMonitor}.swift`: media and host integration.
- `sources/VPhoneCore/VPhone{BundleOps,VirtualMachineManifest,Resources,Library,ManagedProcess,BootPatterns,FirmwareCatalog,FirmwarePicker,VMPicker,RestoreInfo,RestoreOps}.swift`: persistence/toolchain/orchestration contracts.
- `sources/FirmwarePatcher/`, `scripts/fw_prepare.sh`, `scripts/fw_manifest.py`, `scripts/pymobiledevice3_bridge.py`, `scripts/cfw_install*.sh`, `scripts/patchers/`, `scripts/build.sh`, `scripts/setup_tools.sh`: delegated implementation and build graph.
- `scripts/vphoned/`, `scripts/vpregister/`, camera/tweak resources and their recorded submodule manifests: guest implementations and artifacts.
- `tests/VPhoneCoreTests/`, `tests/FirmwarePatcherTests/`, script tests: reference fixtures to adapt, not presumed cove coverage.

Notebook review must examine **this full plan**, not reject it merely because
implementation is future work. Ask for missing features, ambiguous ownership,
unspecified behavior, incorrect source claims, broken dependencies and inadequate
acceptance tests. For each finding, record its source and disposition. Verify
suggested changes against code; do not accept invented symbols, unsupported
100% duplication claims or CPID setter claims from an earlier notebook answer.

Approval requires a final review of the exact latest uploaded revision stating
no unresolved planning blockers or feature omissions, plus a local ledger/source
coverage check. Preserve review prompts/responses and dispositions in the review
record. Runtime qualification stays pending until implementation happens.

[vphone]: https://github.com/Lakr233/vphone-cli/tree/87f796c62a7cb385cd37afce121f6e222d83e5b5
