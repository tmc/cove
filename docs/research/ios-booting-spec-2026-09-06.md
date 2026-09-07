# Spec: vphone-style iOS guest booting in cove

Drafted 2026-09-06 on macOS 27.0, Apple Silicon, arm64. Design doc only — no code
here. Symbol/file references are `path:line` against the repos as they stand today.

## Summary

Booting an iOS guest under Virtualization.framework is **not** a new boot-loader path.
It reuses the *macOS* VZ classes — `VZMacOSBootLoader`, `VZMacPlatformConfiguration`,
`VZMacHardwareModel`, `VZMacAuxiliaryStorage`, `VZMacMachineIdentifier` — but swaps in a
**PV=3 "research guest" hardware model** and attaches a small set of private devices (SEP
coprocessor, PL011 serial, USB touch screen, synthetic battery, GDB debug stub). The guest
firmware is not installed through a VZ installer object at all: the VM is started in **DFU
mode** and restored over a virtual DFU/USB channel by an external `idevicerestore`/
pymobiledevice3-class tool, exactly as a physical iPhone is restored.

Three findings shape the whole plan:

1. **The Go bindings are already complete.** Every private class and selector vphone-cli
   uses already exists in `github.com/tmc/apple/private/virtualization`. No new SPI needs to
   be generated for a first boot. This is the single biggest de-risker.
2. **cove already has DFU plumbing.** `RunConfig` already carries `ForceDFU`, `StopIBoot1`,
   `StopIBoot2`, and `startVMWithRunConfig` already calls `privateOpts.SetForceDFU(...)`
   (`cmd/cove/runtime_private.go:330,365-375`). The mechanism vphone relies on is wired.
3. **The hard part is firmware, not virtualization.** vphone-cli is ~90% a firmware
   pipeline (download + merge IPSWs, decrypt AEA, and 4–141 binary patches to iBoot / kernel
   / AVPBooter / DeviceTree). The VM definition itself is one ~420-line Swift file. cove can
   own the VM definition cleanly; the firmware pipeline is the real cost and risk.

Recommendation: implement iOS as a fourth guest OS mode (`macOS` / `Linux` / `Windows` /
`iOS`) mirroring the existing macOS restore/run split, but treat "restore" as *DFU boot +
external restore tool* rather than `VZMacOSInstaller`. Ship the VM-definition and DFU-boot
half first (which cove is well positioned to do); depend on an external firmware pipeline
for the IPSW/patch half rather than porting vphone's patcher.

## Background: how vphone-cli boots an iPhone

Reference file, fully translated below: `research/VPhoneVirtualMachineRefactored.swift`
in Lakr233/vphone-cli (Swift, uses the `Dynamic` reflection shim to reach private
selectors). Its device graph:

| Concern | vphone Swift (private selector) | cove binding (already present) |
| --- | --- | --- |
| Hardware model | `_VZMacHardwareModelDescriptor` → `setPlatformVersion(3)`, `setBoardID(0x90)`, `setISA(2)`; `VZMacHardwareModel._hardwareModelWithDescriptor(_)` | `vz_mac_hardware_model_descriptor.gen.go` `SetPlatformVersion:120`, `SetBoardID:108`, `SetISA:111`; `vz_mac_hardware_model.gen.go` `HardwareModelWithDescriptor:151` |
| Platform | `VZMacPlatformConfiguration` + `machineIdentifier` + `auxiliaryStorage` | public `vz` package (used already in `cmd/cove/macos.go:698,713`) |
| NVRAM boot-args | `auxStorage._setDataValue(_,forNVRAMVariableNamed:"boot-args")` = `"serial=3 debug=0x104c04"` | `vz_mac_auxiliary_storage.gen.go` `SetDataValueForNVRAMVariableNamedError:311` |
| Boot ROM | `VZMacOSBootLoader._setROMURL(romURL)` (points at patched **AVPBooter**) | `vz_mac_os_boot_loader.gen.go` `SetROMURL:103` (`_setROMURL:98`) |
| SEP | `_VZSEPCoprocessorConfiguration(storageURL:)`, `setRomBinaryURL(_)`, `setDebugStub(_)`; attached via `config._setCoprocessors([_])` | `vzsep_coprocessor_configuration.gen.go` `NewVZSEPCoprocessorConfigurationWithStorageURL:117`, `SetRomBinaryURL:143`, `SetDebugStub:136`; `vzsep_storage.gen.go` `NewVZSEPStorageCreatingStorageAtURLError:100`; `vz_virtual_machine_configuration.gen.go` `SetCoprocessors:394` |
| Kernel debug | `_VZGDBDebugStubConfiguration(port:)`; `config._setDebugStub(_)` | `vzgdb_debug_stub_configuration.gen.go`; `vz_virtual_machine_configuration.gen.go` `SetDebugStub:448` |
| Battery | `_VZMacSyntheticBatterySource` (`setCharge`, `setConnectivity`), `_VZMacBatteryPowerSourceDeviceConfiguration.setSource(_)`; `config._setPowerSourceDevices([_])` | `vz_mac_synthetic_battery_source.gen.go` `SetCharge:104`/`SetConnectivity:111`; `vz_mac_battery_power_source_device_configuration.gen.go` `SetSource:98`; `SetPowerSourceDevices:610` |
| Touch | `_VZUSBTouchScreenConfiguration`; `config._setMultiTouchDevices([_])` | `vzusb_touch_screen_configuration.gen.go` `NewVZUSBTouchScreenConfiguration:74`; `SetMultiTouchDevices:538` |
| Serial | `_VZPL011SerialPortConfiguration` + `VZFileHandleSerialPortAttachment` | `vzpl011_serial_port_configuration.gen.go` `NewVZPL011SerialPortConfiguration:74` (public attachment already used in cove) |
| Display | `VZMacGraphicsDeviceConfiguration` + `VZMacGraphicsDisplayConfiguration(1290×2796 @ 460ppi)` | public `vz` package |
| Keyboard | `VZUSBKeyboardConfiguration` | public `vz` package |
| Net / sound / socket / disk | `VZVirtioNetworkDeviceConfiguration`+`VZNATNetworkDeviceAttachment`, `VZVirtioSoundDeviceConfiguration`, `VZVirtioSocketDeviceConfiguration`, `VZVirtioBlockDeviceConfiguration` | public `vz` package (all used already) |
| Start | `VZMacOSVirtualMachineStartOptions._setForceDFU(_)`, `_setStopInIBootStage1(_)`, `_setStopInIBootStage2(_)` | `vz_mac_os_virtual_machine_start_options.gen.go` `SetForceDFU:120`, `SetStopInIBootStage1:138`, `SetStopInIBootStage2:156` |
| ECID (device identity) | `machineIdentifier._ECID` | `vz_mac_machine_identifier.gen.go` `_ECID:83` |

The PV=3 hardware model is the gate. vphone's own comment documents the framework check:

> `default_configuration_for_platform_version(3)` validity byte = `(entitlements & 0x12) != 0`
> where bit 1 = `com.apple.private.virtualization`, bit 4 = `com.apple.private.virtualization.security-research`.

So the *only* thing standing between cove and a bootable iOS hardware model is the
entitlement being honored on the calling process (see next section). `boardID=0x90`, `ISA=2`,
`platformVersion=3` reproduce the `vresearch101` research device.

### The restore is external, not a VZ installer

vphone does **not** use anything like `VZMacOSInstaller`. Its flow (README + `VPhoneRestoreCLI.swift`,
`VPhoneRestoreOps.swift`):

1. `fw prepare` — download an iPhone IPSW **and** a matching "cloudOS" IPSW (PCC research
   OS), merge, cache under `~/.vphone/ipsws/`.
2. `fw patch` — 4–141 binary patches to the boot chain (iBoot, kernel, AVPBooter, device
   tree, cryptex filesystem seal). This is `sources/FirmwarePatcher/**` — the bulk of the
   project.
3. `vm launch --dfu &` — start the VM in DFU (`SetForceDFU(true)`).
4. `restore --get-shsh` then `restore` — a Python **pmd3 bridge** (pymobiledevice3-class)
   connects to the DFU device and performs an `idevicerestore`-style TSS/SHSH + restore over
   the virtual USB. AEA-encrypted images are decrypted with `ipsw fw aea`
   (`VPhoneRestoreOps.decryptAEAImages`).
5. `cfw install` — host-mounts the restored disk and lays in custom firmware / jailbreak
   payload (sudo).
6. `vm launch` — first normal boot.

ECID stability comes from persisting the `VZMacMachineIdentifier` data representation and
reading `_ECID` back out; the UDID is `CPID-ECID` where CPID is a fixed `0xFE01`.

## Entitlement / signing situation

iOS guest support is gated behind **`com.apple.private.virtualization`** (and, for the PV=3
research model, `com.apple.private.virtualization.security-research`). These are restricted
Apple-private entitlements; a normally code-signed third-party binary cannot carry them and
have them honored.

vphone-cli's `sources/vphone.entitlements` requests:

```
com.apple.security.virtualization
com.apple.private.virtualization
com.apple.private.virtualization.security-research
com.apple.vm.networking
com.apple.security.personal-information.location
com.apple.security.get-task-allow
com.apple.private.bmk.allow
```

cove today signs with only the public set — `internal/autosign/vz.entitlements` has
`com.apple.security.virtualization` (+ network) and nothing private; `Makefile:15-16` does
`codesign -s - -f --entitlements internal/autosign/vz.entitlements`. `macgo_bundle.go:57`
lists the same public entitlement for the headed app bundle.

**Reachability on this host is already proven.** This session demonstrated that
`com.apple.private.virtualization` can be honored for a locally-signed binary via an
**amfidont-via-LLDB hook** (an AMFI decision override attached with the debugger, no reboot
required) — the same category of relaxation vphone documents as its "Option B" (`amfidont`)
and "Option A" (`amfi_get_out_of_my_way=1` boot-arg after `csrutil disable` +
`csrutil allow-research-guests enable`). For cove development this means:

- A locally-signed `cove` binary carrying the private entitlements can start a PV=3 VM on
  this host *for development* once the AMFI hook is active. This is a dev/research posture,
  not a shippable one.
- The trust requirement is plain and non-negotiable: without either SIP/AMFI relaxation or a
  genuine Apple-issued private-virtualization entitlement, `VZMacHardwareModel.isSupported`
  returns false for platformVersion=3 and VM start is refused. This is the same restricted-
  device gate documented for Windows in `docs/research/macos27-windows-virtualization-2026-09-05.md`.
- cove must **not** ship these entitlements in release builds. Gate them behind a build tag
  / opt-in signing path (see Phase 0) so the default `make sign` is unchanged.

## New private bindings needed

**None are required for a first boot.** Every selector in the translation table above is
already generated in `github.com/tmc/apple/private/virtualization`, with both the raw
`_setX` form and the safe `SetX` wrapper plus a `CanSetX` responder check. cove already
imports this package (aliased `pvz`/`privvz` in `cmd/cove/runtime_private.go:20`,
`system_disk.go`, `control_runtime_*.go`).

Verification pass done for this spec (all present):

- `_VZMacHardwareModelDescriptor` + `SetPlatformVersion`/`SetBoardID`/`SetISA` — present.
- `VZMacHardwareModel.HardwareModelWithDescriptor` (`_hardwareModelWithDescriptor:`) — present.
- `VZMacOSBootLoader.SetROMURL` — present.
- `VZMacAuxiliaryStorage.SetDataValueForNVRAMVariableNamedError` — present.
- `VZSEPCoprocessorConfiguration` (+ `SetRomBinaryURL`, `SetDebugStub`) and `VZSEPStorage`
  (`NewVZSEPStorageCreatingStorageAtURLError`) — present.
- `VZGDBDebugStubConfiguration` — present.
- `VZMacSyntheticBatterySource` / `VZMacBatteryPowerSourceDeviceConfiguration` — present.
- `VZUSBTouchScreenConfiguration`, `VZUSBKeyboardConfiguration`, `VZPL011SerialPortConfiguration` — present.
- `VZVirtualMachineConfiguration.{SetCoprocessors,SetDebugStub,SetMultiTouchDevices,SetPowerSourceDevices}` — present.
- `VZMacOSVirtualMachineStartOptions.{SetForceDFU,SetStopInIBootStage1,SetStopInIBootStage2}` — present.
- `VZMacMachineIdentifier._ECID` — present.

**Possible small gaps to confirm at implementation time** (each is a minor `applegen` regen,
not a design change):

- `VZMacHardwareModelDescriptor` — confirm a usable **constructor** (`objc.GetClass` path is
  present at `vz_mac_hardware_model_descriptor.gen.go:21`; confirm there is a `New…` alloc/init
  or that `NewObject`-style allocation works for this private class).
- Reading `_ECID` as a value vs. `NSNumber` — vphone tries both (`asUInt64` then `NSNumber`).
  Confirm the Go binding returns the raw `uint64` reliably; if not, add an `NSNumber` fallback
  helper in `x/vzkit/identity`.
- `VZSEPStorage` **creation options** — vphone creates SEP storage fresh; confirm there is an
  overwrite/allow-existing variant if cove needs idempotent bundle creation.

If any of the above is missing, add it under `github.com/tmc/apple/private/virtualization`
via the existing generator, matching the `_setX` + `SetX` + `CanSetX` triple convention the
package already uses — do not hand-write ad-hoc selectors in cove.

## cove-side design

### Guest-OS mode

Add `iosMode` alongside `linuxMode`/`windowsMode`. Dispatch mirrors the existing switch at
`cmd/cove/command_registry.go:401`:

```
if windowsMode { installWindowsVM(...) }
else if linuxMode { handleLinuxInstall(...) }
else if iosMode { installIOSVM(...) }        // NEW
else { installMacOSLikeVZWithProvision(...) } // macOS
```

`internal/vmconfig/detect.go:DetectOSType` gains an iOS marker check (e.g. presence of
`sep.img` + `avpbooter.rom`, or an explicit `os: "iOS"` in `config.json`) returning `"iOS"`;
`covevm_bundle.go:configureOpenedCoveVM` gets an `iOS` case in its switch (currently
`Linux`/`Windows`/default-macOS at lines 68-78).

### Subcommands (mirror macOS restore/install)

Reuse cove's existing verbs with an `-ios` selector, matching how `-linux`/`windows` already
compose:

- `cove install -ios [--ipsw <iphone.ipsw>] [--cloudos <cloudos.ipsw>] [--restore-image <dir>]`
  — create bundle, resolve/stage firmware, DFU-restore, first boot. Long-running; reuses the
  progress-window scaffolding in `installer.go:524` (`runFullInstallWithGUI`).
- `cove run -ios <name>` — normal boot of an already-restored bundle.
- `cove run -ios --dfu <name>` — DFU boot for manual re-restore (thin wrapper; the RunConfig
  `ForceDFU` field and `startVMWithRunConfig` path already exist, `runtime_private.go:330,371`).

The DFU-restore step itself (SHSH/TSS + restore over virtual USB) is delegated to an external
restore tool (see Open Questions); cove orchestrates it and streams its output, the way
`installer.go` streams install progress.

### VM bundle layout (`~/.vz/vms/<name>.covevm/`)

`StateDir()` = `~/.vz`, `BaseDir()` = `~/.vz/vms` (`internal/vmconfig/paths.go:13-36`). macOS
bundles use `hw.model`, `machine.id`, `aux.img`, `disk.img`, `config.json`. iOS adds:

| File | Purpose | Analogous macOS file |
| --- | --- | --- |
| `hw.model` | PV=3 hardware model data representation (from the descriptor) | `hw.model` |
| `machine.id` | `VZMacMachineIdentifier` (ECID stability) | `machine.id` |
| `aux.img` | `VZMacAuxiliaryStorage` / NVRAM (holds boot-args) | `aux.img` |
| `disk.img` | main NAND-equivalent virtio block image | `disk.img` |
| `avpbooter.rom` | boot ROM passed to `SetROMURL` (patched AVPBooter) | *(none — macOS ROM is host-provided)* |
| `sep.img` | `VZSEPStorage` backing file | *(none)* |
| `sep.rom` | SEP ROM binary (`SetRomBinaryURL`) | *(none)* |
| `restore/` | staged/decrypted IPSW payload used by the restore tool | *(installer streams IPSW)* |
| `udid-prediction.txt` | `ECID=`/`UDID=` cache for the restore tool | *(none)* |
| `config.json` | cove VM config (schema below) | `config.json` |

Marker file for `DetectOSType`: presence of `sep.img` (unambiguous vs. macOS/Linux/Windows).

### config.json schema additions

`internal/vmconfig/config.go:30` `Config` is flat JSON with `omitempty`. Add an optional
nested block so non-iOS configs are byte-identical to today:

```
type IOSConfig struct {
    IPhoneBuild   string `json:"iphoneBuild,omitempty"`   // e.g. "23B85"
    IPhoneVersion string `json:"iphoneVersion,omitempty"` // e.g. "26.1"
    CloudOSBuild  string `json:"cloudOSBuild,omitempty"`
    BoardID       uint32 `json:"boardID,omitempty"`       // default 0x90
    ISA           int64  `json:"isa,omitempty"`           // default 2
    ScreenWidth   int    `json:"screenWidth,omitempty"`   // default 1290
    ScreenHeight  int    `json:"screenHeight,omitempty"`  // default 2796
    ScreenPPI     int    `json:"screenPPI,omitempty"`     // default 460
    BootArgs      string `json:"bootArgs,omitempty"`      // default "serial=3 debug=0x104c04"
    KernelDebugPort int  `json:"kernelDebugPort,omitempty"`
}
// Config gains: IOS *IOSConfig `json:"ios,omitempty"`
```

`platformVersion` is fixed at 3 (not user-tunable — it is the entitlement gate). CPU/memory
reuse the existing `Config.CPU`/`MemoryGB` (`config.go:31-32`); vphone defaults 8 vCPU / 8 GB.

### Devices to attach (iOS config builder)

New file `cmd/cove/ios.go` (mirrors `macos.go`), building on the public `vz` package for the
common devices and `pvz` for the private ones, following the pattern already in
`runtime_private.go:338` (`applyPrivateVMConfigurationWithRunConfig`, which wraps the public
config via `pvz.VZVirtualMachineConfigurationFromID(config.ID)` and calls private setters):

1. Boot loader: `VZMacOSBootLoader`, `SetROMURL(bundle/avpbooter.rom)`.
2. Platform: `VZMacPlatformConfiguration` with the PV=3 `hw.model`, `machine.id`, and
   `aux.img`; set `boot-args` via `SetDataValueForNVRAMVariableNamedError`.
3. Graphics: `VZMacGraphicsDeviceConfiguration` + display 1290×2796 @ 460ppi.
4. Storage: `VZVirtioBlockDeviceConfiguration(disk.img)`.
5. Network: `VZVirtioNetworkDeviceConfiguration` + `VZNATNetworkDeviceAttachment` (reuse
   cove's existing network wiring).
6. Input: `VZUSBKeyboardConfiguration`; `VZUSBTouchScreenConfiguration` via
   `SetMultiTouchDevices`.
7. Audio: `VZVirtioSoundDeviceConfiguration` (host in/out streams).
8. Socket: `VZVirtioSocketDeviceConfiguration` (guest agent / control channel).
9. Serial: `VZPL011SerialPortConfiguration` + file-handle attachment (console).
10. SEP: `VZSEPCoprocessorConfiguration(sep.img)` + `SetRomBinaryURL(sep.rom)` +
    `SetDebugStub(...)`, attached via `SetCoprocessors`.
11. Battery: synthetic source 100% charging via `SetPowerSourceDevices`.
12. Debug stub: `VZGDBDebugStubConfiguration` via `SetDebugStub` (optional, gated on
    `KernelDebugPort`).

Identity helpers: extend `github.com/tmc/apple/x/vzkit/identity` with a
`CreateIOSHardwareModel(boardID, isa)` that builds the descriptor and calls
`HardwareModelWithDescriptor`, plus `Save/LoadHardwareModel` reuse (already generic over the
data representation, `identity.go:72-95`). Keep the descriptor construction in `vzkit`, not
in `cmd/cove`, so cove stays a thin orchestration layer (matches how macOS identity lives in
`x/vzkit/identity`).

### IPSW acquisition / restore flow

cove already has robust IPSW handling for macOS: `ipsw.go` (`parseIPSWSource`,
`verifyIPSWFile` with zip-EOCD check, `resolveOrDownloadIPSW`) and NSURLSession resumable
downloads. Reuse it. iOS differs in three ways:

- **Two images**: an iPhone IPSW and a matching cloudOS (PCC research) IPSW. Add
  `--cloudos` alongside `--ipsw`; both go through the same download/verify path.
- **AEA decryption**: iOS 18+ restore assets are AEA1-encrypted. cove needs to detect the
  `41 45 41 31` magic (as `VPhoneRestoreOps.isAEAEncrypted` does) and decrypt via an external
  `ipsw fw aea` invocation. No native Go path — shell out and stream, verifying re-read is no
  longer AEA.
- **Restore over virtual USB**: cove starts the VM with `ForceDFU`, then runs an external
  restore tool against the DFU device and records the resulting iOS/cloudOS versions to
  `config.json` (mirrors vphone's `restore-info.json`).

## Open questions / risks

1. **No restore catalog for iOS.** macOS has `VZMacOSRestoreImage.fetchLatest` /
   `mostFeaturefulSupportedConfiguration` (`macos.go:961,967`) — cove fetches "latest"
   automatically. **There is no equivalent for iOS.** IPSWs must be user-supplied or fetched
   from a third-party firmware index (e.g. the `ipsw` tool / ipsw.me-style catalogs). This is
   the biggest UX gap vs. the macOS flow; the first cut should require explicit `--ipsw` /
   `--cloudos` paths and document sourcing rather than auto-download.
2. **Firmware patching is the real blocker.** A *stock* IPSW will not boot under PV=3 without
   boot-chain patches (vphone applies 4–141). Porting `sources/FirmwarePatcher/**` to Go is a
   large, separate effort (ARM64 assembler/disassembler, IM4P/IMG4 handling, kernel/iBoot/
   AVPBooter/DeviceTree patchers). **Recommendation: do not port it initially.** Depend on
   an external `fw prepare`/`fw patch` step (vphone-cli's or a standalone tool) that produces
   the patched `avpbooter.rom` + `sep.rom` + patched restore payload, and have cove consume
   those artifacts. Revisit a native patcher only after first boot is proven.
3. **External-tool dependency for restore.** The DFU restore (TSS/SHSH, `idevicerestore`/
   pymobiledevice3) is not something to reimplement in Go for a first cut. cove should
   orchestrate an external restore binary. Define the interface narrowly (a small
   `iosRestorer` interface: `GetSHSH(bundle)`, `Restore(bundle, ecid)`), stdlib `os/exec`
   streaming, so it can be swapped for a native implementation later.
4. **Entitlement is dev-only.** Without an Apple-issued entitlement, iOS boot only works on
   a host with SIP/AMFI relaxed or the amfidont-via-LLDB hook active. cove must gate this
   behind an explicit opt-in and never ship the private entitlements in release signing.
5. **Display/GOP.** Unlike the Windows GOP blocker
   (`docs/research/macos27-windows-virtualization-2026-09-05.md`), iOS uses the Mac graphics
   device with a fixed phone-resolution display and renders through the guest's own IOMFB —
   no GOP concern. VNC/`VZVirtualMachineView` should work as for macOS. Two-finger-click =
   home button (per vphone FAQ) needs a UI affordance in cove's viewer.
6. **Descriptor constructor / ECID readback** — the two minor binding confirmations noted
   above. Low risk (regen), but verify before Phase 1.
7. **Host nesting** — PV=3 cannot nest; the host Mac must not itself be a VM. Add a preflight
   check (cove already has `install_preflight.go`).

## Phased implementation plan (file-by-file)

**Phase 0 — Entitlements & preflight (dev enablement).**
- `internal/autosign/vz.ios.entitlements` (new): adds `com.apple.private.virtualization` +
  `com.apple.private.virtualization.security-research` + `com.apple.vm.networking`.
- `Makefile`: new `sign-ios` target using the iOS entitlements (leave `sign` at line 15
  untouched); document that it requires AMFI relaxation / amfidont hook.
- `cmd/cove/install_preflight.go`: add PV=3 host checks — not nested, host macOS ≥ 15, and a
  probe that `CreateIOSHardwareModel(...).IsSupported()` (fails fast with a clear message
  pointing at the entitlement/AMFI requirement).

**Phase 1 — Bindings confirmation (apple repo).**
- Confirm/regen in `github.com/tmc/apple/private/virtualization`: descriptor constructor,
  `_ECID` value readback, `VZSEPStorage` create-with-overwrite. Add only what is missing,
  via the generator, keeping the `_setX`/`SetX`/`CanSetX` convention.
- `github.com/tmc/apple/x/vzkit/identity/identity.go`: add `CreateIOSHardwareModel(boardID
  uint32, isa int64)` and, if needed, an ECID/NSNumber fallback helper. Table-driven tests +
  an example per the repo style.

**Phase 2 — VM definition & DFU boot (cove owns this fully).**
- `cmd/cove/ios.go` (new): `buildIOSConfiguration(...)` assembling the device graph above,
  plus `runIOSVM(name)`. Model it on `macos.go`'s config builder and reuse
  `runtime_private.go`'s `applyPrivateVMConfigurationWithRunConfig` pattern.
- `internal/vmconfig/config.go`: add `IOSConfig` + `Config.IOS`.
- `internal/vmconfig/detect.go`: `sep.img` → `"iOS"`.
- `cmd/cove/covevm_bundle.go`: `iOS` case in `configureOpenedCoveVM` switch (lines 68-78).
- `cmd/cove/command_registry.go`: `iosMode` branch in the install/run dispatch (line ~401)
  and a global `iosMode` flag next to `linuxMode`/`windowsMode`.
- **Milestone**: `cove run -ios --dfu <name>` starts a PV=3 VM in DFU with a pre-staged
  `avpbooter.rom`/`sep.*`, and the serial console shows iBoot. This proves the entitlement +
  hardware model + device graph independent of the restore pipeline.

**Phase 3 — IPSW staging & restore orchestration.**
- `cmd/cove/ios_ipsw.go` (new): extend the `ipsw.go` helpers for the two-image
  (iPhone + cloudOS) case and AEA detection/decrypt (shell to `ipsw fw aea`).
- `cmd/cove/ios_restore.go` (new): `iosRestorer` interface + an exec-backed implementation
  driving the external restore tool against the DFU device; write `config.json` restore
  metadata on success (mirror `recordRestoreVersions`).
- `cmd/cove/installer.go`: route `installIOSVM` through the existing progress-window
  scaffolding (`runFullInstallWithGUI`, line 524) — create bundle → stage firmware → DFU boot
  → restore → first boot.
- **Milestone**: `cove install -ios --ipsw … --cloudos …` (with externally-patched firmware)
  restores and first-boots to the iOS setup screen.

**Phase 4 (optional, large) — native firmware pipeline.**
- Only after Phase 3 works end-to-end. Port or wrap the `fw prepare`/`fw patch` pipeline
  (IMG4/IM4P, AVPBooter, kernel/iBoot/DeviceTree patches). This is a project of its own and
  should be scoped separately; a Go port of vphone's `FirmwarePatcher` is the reference.

## Appendix: key references

- vphone-cli VM definition: `research/VPhoneVirtualMachineRefactored.swift`
- vphone-cli hardware model + entitlements: `sources/vphone-cli/VPhoneHardwareModel.swift`,
  `sources/vphone.entitlements`
- vphone-cli restore: `sources/vphone-cli/VPhoneRestoreCLI.swift`,
  `sources/VPhoneCore/VPhoneRestoreOps.swift`
- cove macOS boot/restore analog: `cmd/cove/macos.go`, `cmd/cove/installer.go`,
  `cmd/cove/ipsw.go`, `cmd/cove/runtime_private.go`
- cove bundle/config: `internal/vmconfig/{config,detect,paths}.go`,
  `cmd/cove/covevm_bundle.go`
- cove signing: `Makefile:15-16`, `internal/autosign/vz.entitlements`,
  `cmd/cove/macgo_bundle.go:57`
- bindings: `github.com/tmc/apple/private/virtualization/*.gen.go`,
  `github.com/tmc/apple/x/vzkit/identity/identity.go`
- related prior research: `docs/research/macos27-windows-virtualization-2026-09-05.md`
  (restricted-device gate, entitlement enforcement)
