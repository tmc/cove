# Spec: vphone-style iOS guest booting in cove

Reviewed 2026-09-06 (America/Los_Angeles). Proposal; iOS boot support is not
implemented or runtime-validated by this review.

## Recommendation

Add an experimental iOS guest type that uses `VZMacOSBootLoader` and a PV=3
research hardware model. Start with a prepared firmware bundle and prove DFU
connectivity before building an installer. Keep firmware preparation, patching,
and post-restore customization in an external, version-pinned toolchain.

The architecture is plausible from source inspection. It is not yet a demonstrated
cove boot path. Three independent gates remain: the host must permit the research
VM, the Go bindings must work at runtime, and a specific firmware/toolchain
combination must restore and boot successfully.

Apple documents `VZMacOSInstaller` for macOS restore images. vphone instead uses
private Virtualization.framework devices and a pymobiledevice3 bridge for DFU
restore. This is an experimental use of the framework, not a public iOS guest
API. [Apple installer documentation][apple-installer], [vphone restore CLI][vp-restore].

## Evidence and scope

This review inspected these exact revisions:

| Source | Revision | Scope |
| --- | --- | --- |
| cove | `25e1d70000090500696cb98706cabe3d1cd02b56` | Working tree was clean at review start. |
| vphone-cli | `87f796c62a7cb385cd37afce121f6e222d83e5b5` | Clean local checkout; upstream commit links below. |
| `github.com/tmc/apple` | `v0.6.19-0.20260907023617-4c51c1664813` | Resolved module cache from `go list -m -json`, with no workspace override. |

The dependency timestamp is UTC; its September 7 date is September 6 locally.
Binding claims below refer to the pinned module, not the adjacent apple checkout.
Local links name files and symbols rather than unstable line numbers. The public
GitHub URL for the pinned apple commit returned 404 during link verification;
its binding audit is local evidence. Locate the reviewed source through the
`Dir` field of `go list -m -json github.com/tmc/apple`. Availability of that exact
module on a fresh machine remains to be checked.

| Finding | Evidence status |
| --- | --- |
| PV=3, board ID `0x90`, ISA `2` construct vphone's `vresearch101` model | Present in upstream source; host support still requires a probe. |
| Descriptor constructor, ECID accessor, SEP constructors, and private device setters exist | Confirmed in the pinned Go dependency; presence does not establish ABI correctness or runtime availability. |
| cove has DFU start options | Confirmed, but currently restricted to `GuestMacOS`; setter errors are ignored. |
| This host can start a PV=3 cove VM | **Unverified.** No reproducible PV=3 probe or DFU enumeration result is attached to this spec. |
| Restored iOS boots and is usable in cove's viewer | **Unverified.** Requires restore, post-restore work, and separate display/input tests. |

The original draft claimed that an amfidont/LLDB experiment had already proved
reachability on this host. Treat that as an unrecorded prior observation, not an
acceptance result. The [Windows research note](macos27-windows-virtualization-2026-09-05.md)
provides context about restricted-device checks, but a different device graph's
result does not prove iOS boot.

## Upstream boot and restore model

Use the active [VPhoneVirtualMachine.swift][vp-vm] implementation as the reference.
`research/VPhoneVirtualMachineRefactored.swift` is supplementary research material,
not the executable's current device builder.

The [hardware model helper][vp-hardware] sets platform version 3, board ID `0x90`,
and ISA `2`. Its CPID constant is `0xFE01`. vphone persists the machine identifier
in `config.plist`, reads its ECID, and predicts a UDID formatted as eight uppercase
hex CPID digits, a hyphen, and sixteen uppercase hex ECID digits. This prediction
must be checked against the device observed by the restore tool.

The upstream pipeline has distinct stages:

1. Create a bundle and obtain a compatible iPhone/cloudOS image pair.
2. Prepare a merged restore tree and apply the selected firmware patches.
3. Launch the VM with the custom AVPBooter ROM and force DFU.
4. Discover the target, obtain a TSS/SHSH ticket, and restore through the Python
   bridge. The bridge uses IRecv for DFU/recovery and usbmux/lockdown where available.
5. Stop the VM and perform the selected CFW installation against the host-mounted
   disk. This is a separate privileged operation in upstream.
6. Detach host mounts and launch normally; verify guest readiness.

The documented workflow includes CFW installation. Omitting it from cove's
installer requires evidence that the selected firmware variant boots without it.
Neither a fixed patch count nor a percentage estimate of upstream's patching code
is a useful compatibility contract. Record the exact variant, commits, and input
builds instead. [Upstream workflow][vp-readme], [restore bridge][vp-bridge],
[CFW host installer][vp-cfw].

### Device graph and binding audit

All private filenames below are relative to the pinned dependency's
`private/virtualization/` directory. The public package is
`github.com/tmc/apple/virtualization`. This table records source correspondence;
the minimum device set for successful iOS boot remains an experiment.

| Concern | Active vphone behavior | Go binding / implementation implication |
| --- | --- | --- |
| Hardware model | Descriptor with PV=3, board `0x90`, ISA `2` | `vz_mac_hardware_model_descriptor.gen.go`: `NewVZMacHardwareModelDescriptor`, `SetPlatformVersion(uint32)`, `SetBoardID(uint32)`, `SetISA(int64)`. `GetVZMacHardwareModelClass().HardwareModelWithDescriptor` returns `(objectivec.IObject, error)`; check and wrap the object ID as the public model. |
| Platform / identity | Mac platform, saved machine identifier, auxiliary storage | Public Mac classes; `vz_mac_machine_identifier.gen.go` exports `CanECID()` and `ECID() (uint64, error)`. Call these rather than the inaccessible `_ECID` method. |
| NVRAM | Sets `serial=3 debug=0x104c04` | `vz_mac_auxiliary_storage.gen.go`: `SetDataValueForNVRAMVariableNamedError` takes Objective-C objects for data/name and returns `(bool, error)`; check both results. |
| AP boot ROM | Custom AVPBooter URL | `vz_mac_os_boot_loader.gen.go`: `SetROMURL` returns an error. |
| SEP | Storage URL, optional SEP ROM, debug stub | `vzsep_coprocessor_configuration.gen.go`: `NewVZSEPCoprocessorConfigurationWithStorageURL`, `SetRomBinaryURL`; `SetDebugStub` is inherited from `VZCoprocessorConfiguration`. Attach with `SetCoprocessors`. |
| Display | Mac graphics, default 1290×2796 at 460 ppi | Public Mac graphics classes. Dimensions are upstream defaults, not a universal firmware requirement. |
| Disk / network | Virtio block disk; configured NAT, bridged, or no network | Public classes. Start with NAT or no network; bridged networking is a separate entitlement/configuration concern. |
| Serial | PL011 UART with input/output pipes | `vzpl011_serial_port_configuration.gen.go` and public file-handle attachment. Keep pipe handles alive for the run. |
| Keyboard / touch | USB keyboard and private USB touch screen | Public keyboard; `vzusb_touch_screen_configuration.gen.go` plus `SetMultiTouchDevices`. |
| Entropy | Virtio entropy device | Public `VZVirtioEntropyDeviceConfiguration`; omitted from the original draft. |
| Accelerators | Attempts VideoToolbox, Neural Engine, and scaler together | `vz_mac_video_toolbox_device_configuration.gen.go`, `vz_mac_neural_engine_device_configuration.gen.go`, `vz_mac_scaler_accelerator_device_configuration.gen.go`; attach with `SetAcceleratorDevices`. Availability and necessity need testing. |
| Audio / vsock | Host audio input/output; vsock unless `noVphoned` | Public classes. A vsock device alone does not supply a compatible guest agent. |
| Battery | Synthetic source: charge `100.0`, connectivity `1` | `vz_mac_synthetic_battery_source.gen.go`, `vz_mac_battery_power_source_device_configuration.gen.go`, `SetPowerSourceDevices`. |
| Debug | AP and SEP GDB stubs enabled by default upstream | `vzgdb_debug_stub_configuration.gen.go`; cove should use its existing debug controls and verify boot with debugging disabled before making that the default. |
| Start | Force DFU; both iBoot stop flags false | `vz_mac_os_virtual_machine_start_options.gen.go`: exported setters return errors. |

Compare the pinned module files above with the [active VM builder][vp-vm].

The earlier constructor/ECID questions are resolved at the source level. SEP
storage also has both create and open constructors:
`NewVZSEPStorageCreatingStorageAtURLError` and `NewVZSEPStorageWithURL`.
However, the active [bundle creator][vp-bundle-ops] writes a 512 KiB zero-filled
`SEPStorage` file; it does not call the VZ creation API. Compare formats before
substituting one method for the other. An overwrite API is not needed for normal
reopening, and implicit overwrite would destroy persistent state.

Not every binding has a `CanSetX`/error-returning wrapper. Descriptor setters and
some device properties call `objc.SendIfResponds` directly and return no error.
Before using them, verify class availability, selector availability, nonzero
constructor results, and relevant method encodings. In particular, do not infer
an `NSNumber` ABI from Swift's Dynamic call syntax or add a speculative fallback
to the typed ECID accessor. Verify the host's method signature first.

Private configuration arrays accept `objectivec.IObject`; construct actual
`NSArray` objects rather than passing Go slices. Manage object lifetimes and VM
queue affinity using the project's existing patterns. If a binding is wrong or
missing, fix its generator in the apple repository and update cove's dependency.

## Host support and signing

Keep three checks separate:

- **Host research policy.** Apple's PCC VRE setup requires Apple silicon, at least
  16 GB of unified memory, macOS 15.1 or later, and enabling research guests in
  recoveryOS. These are Apple's PCC VRE requirements, not a tested minimum for
  cove's modified iOS guest. [Apple VRE setup][apple-vre-setup].
- **Entitlement acceptance.** vphone requests both
  `com.apple.private.virtualization` and
  `com.apple.private.virtualization.security-research`, in addition to the public
  virtualization entitlement. Its README describes host security changes for
  accepting its locally signed executable. Merely adding entitlement keys or
  enabling research guests does not demonstrate that cove's process is authorized.
  [vphone entitlements][vp-entitlements], [host prerequisites][vp-readme].
- **Framework/device support.** The hardware helper comments describe a model
  check using `(entitlements & 0x12) != 0`: either bit suffices for that particular
  check. This does not mean both entitlements are required there, or that either
  alone permits the entire device graph. `IsSupported`, configuration validation,
  VM start, and DFU enumeration are separate results. [Hardware helper][vp-hardware].

Upstream declares macOS 15.0 as its deployment target and reports that PV=3 boot
cannot nest. Use a physical Apple silicon Mac for the initial test matrix, record
its exact model and OS build, and determine the minimum supported cove host from
results. Do not turn the draft's macOS 27 host description into compatibility
coverage. [Package manifest][vp-package], [upstream troubleshooting][vp-readme].

The reviewed cove tree has a signing-path discrepancy:

| Path | Current state |
| --- | --- |
| [cmd/cove/vz.entitlements](../../cmd/cove/vz.entitlements) | Public virtualization and client/server network entitlements; embedded by `autosign.go`. |
| [cmd/cove/autosign.go](../../cmd/cove/autosign.go) | Checks those public keys and may re-sign/re-exec the running binary. |
| [internal/autosign/vz-research.entitlements](../../internal/autosign/vz-research.entitlements) | Already exists; adds `com.apple.private.virtualization`, but not the security-research key. |
| [cmd/cove/macgo_bundle.go](../../cmd/cove/macgo_bundle.go) | Configures public keys for the app bundle; must be included in research-signing validation. |
| [Makefile](../../Makefile), release scripts | Still refer to `internal/autosign/vz.entitlements`, which does not exist at the reviewed revision. |

Resolve the stale public signing paths as a separate prerequisite. Reuse the
existing research plist through an explicit research signing mode; do not add a
second competing iOS plist. Verify the entitlements on the final executable after
any autosign or app-bundle relaunch. Keep private keys out of release signing.
Cove should report preflight failures, not change the host's security policy.
This review did not modify signing or host security settings.

## Proposed cove integration

### Guest type and commands

Add `GuestIOS` throughout [internal/vmrun](../../internal/vmrun), including its
string form, validation, and execution plan. Add the CLI selector and option
snapshot/dispatch handling; an `iosMode` boolean alone is insufficient.

In [runtime_private.go](../../cmd/cove/runtime_private.go),
`startVMWithRunConfig` currently enters the Mac start-options branch only for
`rc.OS == vmrun.GuestMacOS`. Extend it deliberately for iOS DFU/boot-stop options,
retain macOS-only recovery semantics, and propagate private setter errors to the
completion callback. Otherwise iOS can silently take the ordinary start path.
Validate conflicts between OS selectors and boot modes before touching VM files.

Proposed commands, **not implemented**:

```text
cove install -ios -restore-image <prepared-bundle> <name>
cove run -ios -force-dfu <name>
cove run <name>
```

`-force-dfu` is cove's existing spelling; the original `--dfu` example was not.
The install input is a versioned prepared bundle containing ROMs and a restore
tree, not a stock IPSW or an already-restored disk. Phase 2 below exposes DFU
running with a manually staged bundle; the full install command arrives only
with restore and post-restore support. Raw `-ipsw`/`-cloudos` acquisition is deferred.

Detect iOS from an explicit `ios` config block **before** the existing `hw.model`
check in [detect.go](../../internal/vmconfig/detect.go). `sep.img` is not a reliable
OS marker: it describes a device, not the installed OS. An invalid iOS config
must fail during validation instead of falling back to macOS. Preserve legacy
marker behavior for bundles without the new block. Update
[registry.go](../../internal/vmconfig/registry.go), Finder opening in
[covevm_bundle.go](../../cmd/cove/covevm_bundle.go), and CLI run selection together.

### Bundle and persistent state

Use `vmconfig.Path(name)` rather than hard-coding `~/.vz`; `COVE_STATE_DIR` and
legacy paths are supported. A new bundle normally lives under
`<state>/vms/<name>.covevm/`.

| File | Purpose and lifetime |
| --- | --- |
| `config.json` | Existing CPU/memory plus optional versioned `ios` block. |
| `hw.model`, `machine.id` | Persist the model and machine identity; validate on reopen. |
| `aux.img`, `sep.img`, `disk.img` | Mutable NVRAM, SEP state, and guest disk; create once, reopen on ordinary runs. |
| `avpbooter.rom`, `sep.rom` | Exact ROM artifacts identified by the prepared manifest. |
| `firmware.json` | Prepared-input provenance and relative artifact paths/checksums. |
| `restore/` | Staged payload with an explicit active restore tree; owned by the restore attempt. |
| `restore-state.json` | Last completed stage, attempt identity, artifact references, tool versions, and failure details. |
| `logs/` | Separate host/tool and serial logs for each attempt. |

Persist a schema version, hardware profile (initially one `vresearch101` profile),
iOS/cloudOS builds, firmware-manifest digest, display settings, and boot arguments
in the `ios` block. Reuse existing CPU/memory and GDB runtime options. Do not expose
arbitrary board ID/ISA combinations before they have matched firmware profiles.
A nil `Config.IOS` with `omitempty` leaves the new block absent from existing VMs;
this does not promise byte-for-byte preservation of arbitrary input JSON.

Record input image hashes, product/build identity, patcher commit and variant,
patch report, ROM hashes, prepared payload manifest, and required post-restore
recipe in `firmware.json`. Validate its version, relative paths, files, and hashes
before creating mutable state. This manifest describes cove's adapter contract;
it is not an existing vphone export format.

Load existing identity/state without overwriting it. If a restored bundle loses
`machine.id`, fail rather than generating a new ECID. Treat disk, auxiliary
storage, SEP storage, model, and identity as one consistency set for backup.
Gate generic clone/fork and save/resume operations until their iOS semantics are
tested; a copied restored disk is not evidence that a new identity will work.

Upstream recreates auxiliary storage with `.allowOverwrite` in its VM builder.
Cove's proposed persistence policy intentionally differs and needs a repeated-boot
test. For an initial reproduction, record any need to reproduce upstream's NVRAM
reset behavior explicitly. [VM implementation][vp-vm].

### Firmware and restore adapter

Start with one exec-backed adapter for the pinned vphone pipeline. Do not claim
that stock `idevicerestore` or an arbitrary pymobiledevice3 version is interchangeable.
Upstream's dependencies include `pymobiledevice3>=9.5.0`; that lower bound is not a
reproducible environment lock. Record the actual Python and package versions used.
[Resource requirements][vp-resources], [bridge implementation][vp-bridge].

The adapter must resolve these concrete layout differences:

- The bridge's `--vm-dir` must contain exactly one `iPhone*_Restore` directory;
  passing cove's bundle root with only `restore/` will fail.
- vphone uses `config.plist`, `Disk.img`, `nvram.bin`, `SEPStorage`, and named
  AVPBooter files. Its CFW host script explicitly opens `Disk.img`. Cove's proposed
  filenames do not satisfy that interface automatically, especially on a
  case-sensitive volume.
- Generate an adapter workspace/manifest that maps to cove's authoritative files.
  Preserve the same machine identifier and ECID; never let the external workflow
  create a second identity. Use deliberate mappings, not a renamed directory and
  assumptions about compatibility. [Bundle schema][vp-manifest], [CFW script][vp-cfw].

The pinned bridge exposes the following operations (argument notation, not a
ready-to-run restore recipe):

```text
python <bridge> recovery-probe --ecid <0xECID> --timeout <seconds>
python <bridge> restore-get-shsh --vm-dir <adapter-dir> --ecid <0xECID> --out <ticket>
python <bridge> restore-update --vm-dir <adapter-dir> --ecid <0xECID> --erase [--tss <ticket>]
```

Require ECID targeting on every operation; never use automatic first-device
selection. The bridge interprets ECID text as hexadecimal, so always include
`0x`. Its probe accepts DFU **or recovery** and does not report which mode it saw;
add a mode-aware result before treating it as proof of DFU. Verify identity across
reenumeration. The bridge's restore defaults to erase. [Bridge source][vp-bridge].

Use `exec.CommandContext`, explicit argument vectors, streaming `io.Writer`
outputs, bounded discovery/stage deadlines, and cancellation that reaps child
processes. Keep the VZ event loop responsive while restore runs. Hold the bundle
lock for the whole operation; ensure no VM process has the disk open before CFW
host mounting, and ensure all mounts are detached before starting again.

Separate ticket acquisition from restore, and key cached tickets by the target
and firmware identity, including nonce requirements reported by the backend.
An old `.shsh` file alone does not establish offline restorability. Offline mode
also needs all payloads, decryption keys, and post-restore resources locally.

For AEA, inspect file contents rather than assuming every iOS 18+ asset is
encrypted. Upstream's offline helper detects `AEA1`, runs `ipsw fw aea`, and keeps
the original `.aea` filename with decrypted contents for manifest compatibility.
Cove should decrypt into staging, validate the output, and replace atomically;
absence of AEA magic alone does not validate a disk image. The tool supports
explicit key/PEM inputs, so document whether a given attempt needs network key
retrieval. [Upstream AEA helper][vp-aea], [ipsw AEA guide][ipsw-aea].

Cove's [ipsw.go](../../cmd/cove/ipsw.go) now uses curl-based resumable downloads,
not NSURLSession as the original draft stated. Its minimum-size and ZIP-tail
checks are download sanity checks, not firmware compatibility or integrity
verification. Reuse transfer mechanics later; validate the selected build
identity, complete artifact set, and hashes independently.

### Restore lifecycle and success criteria

Persist completed stages atomically:

```text
prepared -> dfu-observed -> restored -> customized -> boot-verified
```

A failure records its stage and error while preserving logs and mutable state.
A fresh process must revalidate device discovery and mounts; a persisted
`dfu-observed` value cannot establish that a device is still present. Resume only
from a stage the adapter can safely repeat. Never turn an interrupted attempt
into an automatic erase of an existing restored VM.

`restored` means the restore tool succeeded. `customized` means the selected
post-restore recipe completed with host mounts detached (or a tested profile
explicitly needs none). `boot-verified` requires a normal boot and positive guest
evidence, such as SpringBoard/setup UI plus an identified guest service response.
Neither a VZ start callback nor serial iBoot output alone establishes that result.

Display, touch, audio, networking, and guest services are separate capabilities.
The Mac graphics configuration is promising for rendering, but cove's VNC and
viewer input routes need tests. vphone's gestures and hardware-key handling are
implemented in its [custom view][vp-view]; attaching a touch device does not
supply equivalent behavior. The view includes a guest-daemon touch-injection
fallback described for iOS 18 bases on the 26.x kernel, alongside private native
multitouch event injection. Include host/guest version and input transport in the
compatibility matrix. Existing macOS/Linux agents should not be reported as
installed merely because a vsock device exists.

## Implementation phases and acceptance gates

| Phase | Changes | Required evidence before proceeding |
| --- | --- | --- |
| 0: host and bindings | Fix public signing references; define research signing through the existing plist and relaunch paths. Probe private constructors/selectors and ECID in the pinned dependency. | Record host model/build, executable hash/effective entitlements, model support, configuration validation, and errors separately. Save/reload identity and confirm identical ECID. No PV=3 success claim based only on a selector responding. |
| 1: prepared input and guest routing | Add `GuestIOS`, config schema/validation, detection precedence, CLI/Finder routing, and a prepared-artifact adapter. Keep hardware construction in a small vzkit helper if reusable. | Table-driven tests reject corrupt identity, mismatched profiles, invalid manifests, and OS/start-option conflicts. Existing macOS/Linux/Windows detection still passes. |
| 2: VM definition and DFU | Add `cmd/cove/ios.go`; build the audited graph, extend `startVMWithRunConfig`, and propagate errors. | On a prepared host, start with `-force-dfu` and enumerate the expected ECID in **DFU mode**. Attach machine-readable discovery results and logs. Serial output is supplementary; DFU may be quiet. |
| 3: restore and customization | Add the exec adapter, attempt state, ticket handling, cancellation, and stopped-VM CFW orchestration. Integrate progress UI only after the headless lifecycle works. | A pinned firmware pair restores, customizes, and boots normally. Stop/reopen and verify identity/state persistence. Inject failures at discovery, restore, and customization; verify cleanup and safe retry behavior. |
| 4: usability and broader support | Validate viewer input, networking, guest services, save/resume if offered, and additional host/firmware profiles. Consider image acquisition convenience. | Publish a tested compatibility matrix and explicit unavailable features. Native patching remains a separately scoped project. |

Use `rsc.io/script` fixtures for CLI behavior and fake restore executables to
exercise targeting, arguments, cancellation, and stage transitions without
firmware or host security changes. Real PV=3 tests are opt-in integration tests.
Ordinary `go test ./...` does not prove guest boot. Before landing implementation,
run the repository build/test gates and sign any emitted cove binary with the
appropriate entitlement profile.

The first decision point is Phase 2: can cove expose the expected DFU device with
the pinned firmware and bindings? Until then, full installer UX and a native
firmware patcher are premature.

## Sources

Upstream links are pinned to the reviewed commit. Apple and ipsw documentation
were retrieved during this review; their host/tool requirements may change.

[apple-installer]: https://developer.apple.com/documentation/virtualization/vzmacosinstaller
[apple-vre-setup]: https://security.apple.com/documentation/private-cloud-compute/vresetup
[vp-readme]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/README.md
[vp-package]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/Package.swift
[vp-hardware]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/sources/vphone-cli/VPhoneHardwareModel.swift
[vp-vm]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/sources/vphone-cli/VPhoneVirtualMachine.swift
[vp-entitlements]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/sources/vphone.entitlements
[vp-restore]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/sources/vphone-cli/VPhoneRestoreCLI.swift
[vp-bridge]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/scripts/pymobiledevice3_bridge.py
[vp-aea]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/sources/VPhoneCore/VPhoneRestoreOps.swift
[vp-cfw]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/scripts/cfw_install_host.sh
[vp-manifest]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/sources/VPhoneCore/VPhoneVirtualMachineManifest.swift
[vp-bundle-ops]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/sources/VPhoneCore/VPhoneBundleOps.swift
[vp-resources]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/sources/VPhoneCore/VPhoneResources.swift
[vp-view]: https://github.com/Lakr233/vphone-cli/blob/87f796c62a7cb385cd37afce121f6e222d83e5b5/sources/vphone-cli/VPhoneVirtualMachineView.swift
[ipsw-aea]: https://blacktop.github.io/ipsw/docs/guides/aea/
