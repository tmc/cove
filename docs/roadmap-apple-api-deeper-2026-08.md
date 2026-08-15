# Addendum: Deeper tmc/apple API Scan (2026-08)

This document is an **addendum** to
[`roadmap-apple-api-opportunities-2026-08.md`](roadmap-apple-api-opportunities-2026-08.md).
It does not restate that roadmap's themes, ranked table, proposal details, or
its section 4 "do not re-litigate" list. Read the base document first; the
numbering used here (`3.1`, `3.12`, §4, …) refers to it.

Everything below comes from a second, deeper pass over `github.com/tmc/apple`
covering surfaces the first six scans did not enumerate: the private VZ
diagnostic/config verbs, the temporal and tuning halves of Vision, the public
`network`/`security`/`oslog` host surfaces, the unclaimed `x/` packages, and
host OS power/disk/notification integration. Every symbol citation was checked
against the generated sources on 2026-08-15, and each proposal below records
the corrections applied during that check — including where the original
scan's rationale was wrong.

## 1. Executive summary

Five findings shape this addendum.

1. **Cove already probes more private VZ surface than it ships.** Core dump,
   restricted mode, reset, memory overcommit, `_setName`, and the panic device
   all have selector probes or live tests in `cmd/cove/private_api_*_test.go`
   and no production call path. The cheap work is productizing probes, not
   discovering APIs. Two independent scan entries proposed guest core dump
   (`cove dump` and `cove core`); they are **merged as item 2.1** here.
2. **The pointer/input gap that actually blocks cuabench is scroll.** The CUA
   adapter fakes scrolling with PageUp/PageDown
   (`cmd/cove/agent_sandbox_anthropic.go:81`), which is wrong on any
   non-focused or non-text surface. The fix belongs on the settled AppKit view
   delivery path, not on the private HID path that memory records as landing
   clicks at the wrong position.
3. **Vision's tuning and temporal halves are entirely unclaimed.**
   `x/vzkit/ocr` sets exactly two knobs and always OCRs the full frame; region
   of interest alone is the largest latency lever in the automation loop.
   `VNSequenceRequestHandler`, feature prints, and revision pinning are
   unused.
4. **Host OS integration is absent.** No `NSProcessInfo` activity, no
   `NSWorkspace` sleep/wake observer, no thermal state, no DiskArbitration, no
   `NSURL` backup exclusion. A headless fleet worker is silently killed by host
   idle sleep, and a default-configured Mac backs up every VM disk to Time
   Machine.
5. **Several first-pass rationales were wrong and are corrected here** rather
   than carried forward: DiskArbitration does not protect a *running* VM (VZ
   opens the image as a plain file, with no `/dev/diskN` to auto-mount);
   `SetExcludesCurrentProcessAudio(true)` would discard exactly the guest audio
   it was proposed to capture; `SetCustomWords` is inert with language
   correction disabled; `NLEmbedding` cannot rescue a lexical `Contains`
   pre-filter it never reaches.

Ranking convention below: value and effort are the **post-correction** ratings.
Where a scan's original rating was revised, both are shown (`high -> medium`).

## 2. Private VZ: diagnostics, crash policy, identity

### 2.1 Guest core dump (`cove ctl create-core`)

Merged from two scan entries (`cove dump` and `cove core`).

- Value medium-high -> **medium**. Effort **M** (plus a separately time-boxed
  **S** discovery spike). Risk **M-H**.
- Symbols: `apple/private/virtualization/vz_virtual_machine.gen.go:237`
  (`CreateCoreWithCompletionHandler`), `:247` (`CanCreateCore…`), `:256`
  (`CreateCoresWithCompletionHandler`), `:275`
  (`CreateSharedMemoryCoresWithOptions…`); `apple/x/vzkit/privatevm/privatevm.go:86`
  (`CanCreateCore`, the only helper vzkit exposes).
- Existing cove prior art: `cmd/cove/private_api_live_test.go:772`
  (`_canCreateCore` probe), `cmd/cove/private_api_control_test.go:73-76`
  (selector presence for all four verbs), `:309`
  (`TestPrivateControlAPICreateCores`, which already invokes both create verbs
  against a live running VM with a 10 s timeout),
  `cmd/cove/private_api_diagnostics_test.go:215`.
- Cove touchpoints: `cmd/cove/runtime_private.go`, `control_runtime_status.go`,
  `control_socket_commands.go`, `ctl.go`, `pit.go`.
- Gates: private selectors, `objc.SendIfResponds` + `Can*` probes; no known
  entitlement; no hard macOS floor — probe at runtime.

Corrections applied:

- **The discovery spike is mostly wired already.** The fastest path to the
  unknown artifact location is to run the existing
  `TestPrivateControlAPICreateCores` against a running VM and `find`/`fswatch`
  for the emitted file — not to write new probe code.
- **`_canCreateCore` SIGTRAPs on stopped VMs**
  (`private_api_diagnostics_test.go:216` records this). So "gate on
  `_canCreateCore`" is itself unsafe. The command must check cove-side VM state
  first and *refuse* when not running, rather than probe. That narrows the
  value: this cannot serve the wedged-and-stopped post-mortem case, only
  running-but-hung.
- **No public consumer for the artifact.** The near-term deliverable is "a file
  to attach to a Feedback Assistant report", not "cove can diagnose a hang".
  If the artifact path proves unrecoverable, ship as `cove ctl create-core`
  (logs where VZ says it went), not a top-level `cove dump` promising a path.
- **v1 uses `_createCoreWithCompletionHandler:` only.** Leave
  `_createSharedMemoryCoresWithOptions…` alone — its `options` parameter is
  `unsafe.Pointer` with an undocumented dictionary shape, i.e. a crash surface.
- A `privatevm.CreateCore` helper is desirable for queue symmetry with
  `CanCreateCore` but is **not** a blocker: cove already imports
  `apple/private/virtualization` directly.
- Storage: a 16-64 GB core interacts with the design 040 budget; the write path
  needs a budget guard.

### 2.2 Guest crash policy: panic device + panic/restart/fatal-error actions

- Value high -> **medium**. Effort S/M plumbing, **M/L** including
  characterization. Risk **M-H**.
- Symbols: `vz_virtual_machine_configuration.gen.go:466`, `:574`, `:592`,
  `:628`; `vz_pv_panic_device_configuration.gen.go:75`
  (`NewVZPvPanicDeviceConfiguration`); `vz_panic_device_configuration.gen.go:99`.
- Cove touchpoints: `cmd/cove/macos.go`, `linux.go`, `runtime_lifecycle.go`,
  `control_runtime_status.go`, `vm_state.go`.
- Gates: config-time private setters with `Can*` probes; config rebuild, not
  hot-attachable; ship behind a hidden flag / `COVE_` env until action values
  are characterized.

Corrections applied:

- **pvpanic is a Linux paravirt device.** Cove's primary target is macOS
  guests, which will not drive it. The payoff applies to the Linux/microvm
  path, not to cuabench or the macOS fleet worker.
- **Host-side crash observability is not absent today.**
  `VZVirtualMachineDelegate guestDidStopWithError` and VM state transitions are
  public and already wired in `vm_state.go` / `runtime_lifecycle.go`. The delta
  is guest-originated panic *signalling* plus action selection. The claimed
  design-046 precondition was overstated.
- Strike "cove's own grep shows zero use" from the rationale — cove has
  selector-level probes but no feature path.
- **Validation risk.** It is unverified that `validateWithError:` accepts a
  `_VZPvPanicDeviceConfiguration` on `_setPanicDevice:`. A failure would break
  VM start, so the attach must be try/validate/detach-on-failure;
  `SendIfResponds` silently no-ops and would hide it.
- **A green test suite is not evidence the selectors exist** — the tests
  `t.Skipf` when `Can*` is false. Require a logged `Can*` result before
  scoping.
- Characterize action values by observing post-set read-back (which values
  stick, which VZ rejects) before any disassembly effort.

### 2.3 VM identity and crash-context attribution (`_setName` / `_setCrashContextMessage`)

- Value medium -> **low-medium**. Effort **S**. Risk **low**.
- Symbols: `vz_virtual_machine.gen.go:418` (`_setCrashContextMessage`), `:436`
  (`_setName`), `:584` (`_crashContextMessage` getter);
  `cmd/cove/private_api_diagnostics_test.go:182`.
- Cove touchpoints: `runtime_lifecycle.go`, `apple_logs.go`,
  `control_runtime_status.go`, `vm_registry.go`.

Corrections applied:

- **Binding trap.** Do *not* use the generated property setter `Set_name`
  (`vz_virtual_machine.gen.go:723`) — it sends `set_name:` and silently no-ops
  through `SendIfResponds`. Use `SetName` (`:441`, sends `_setName:`). Same
  hazard for `Set_crashContextMessage` (`:601`). Cove's own test documents this
  at `private_api_diagnostics_test.go:169`. Acceptance criterion: read back
  `_name` / `_crashContextMessage` after setting.
- **The observability payoff is a hypothesis, not a fact.** Existing tests only
  round-trip the property on the same object. Nothing shows the string reaches
  os_log subsystem metadata, Activity Monitor (which shows the cove/helper
  process name), or a crash report field. Prove it in the same slice:
  `log show --predicate 'subsystem CONTAINS "virtualization"'` plus a forced VZ
  crash. If it does not surface, drop the item rather than ship a
  self-describing no-op.
- Demote from a standalone item to a rider on the OSLogStore work (base
  roadmap 3.5 / addendum 4.2), gated on that check.
- Privacy: write VM name and run id only, never user data.

### 2.4 `cove reset`: in-place hard reset via `_resetWithType:`

- Value medium -> **low-medium**. Effort S -> **M**. Risk **M-H**.
- Symbols: `vz_virtual_machine.gen.go:386`, `:396`;
  `cmd/cove/private_api_live_test.go:381`; `private_api_control_test.go:107`.
- Cove touchpoints: `runtime_lifecycle.go`, `control_socket_commands.go`,
  `ctl.go`, `agent_state.go`, `private_api_control_test.go`.

Corrections applied:

- **The cuabench payoff does not exist.** Per the soft-reset empirical work
  (`b4be650`) and design 047, task isolation requires fork / RAM-overlay
  restore; a hard reset gives a clean boot, not restored state. The remaining
  payoff is narrower: fleet workers and long-lived GUI sessions recovering a
  wedged guest without dropping the `VZVirtualMachine`, window frame, VNC
  server and control-socket runtime state.
- **Keeping the process alive is the work.** After reset the agent vsock
  channels, `agent_state`, runtime status and hot-added devices are stale;
  `runtime_lifecycle.go`'s state machine only models stop→start. The objc call
  is the easy part.
- **A hard reset is an unclean shutdown of an APFS guest** — expect fsck churn
  and possible data loss. Document as destructive; offer an agent-mediated
  graceful path first.
- **The probe evidence is weaker than it looks**: both existing tests only
  `t.Logf` and never assert, so nothing records that type 0 even succeeds.
  Gate shipping on a live run of `private_api_control_test.go:107` with the
  observed semantics committed. Expose exactly one characterized type.
- Drop `fork_ephemeral.go` from touchpoints unless the fork path is shown to
  want a non-restoring reset.

### 2.5 Memory density knobs: overcommit + termination-under-memory-pressure

- Value medium -> **medium-low** until characterized. Effort S -> **M**.
  Risk: config-time low, runtime/behavioral high.
- Symbols: `vz_virtual_machine_configuration.gen.go:520`, `:530`, `:646`,
  `:656`; class method
  `VZVirtualMachineConfiguration.MaximumAllowedOvercommittedMemorySize`
  (exercised at `cmd/cove/private_api_config_test.go:313-322`).
- Cove touchpoints: `memory_limits.go` (cap/validation), `memory.go`,
  `macos.go`, `linux.go`, `fork_ephemeral.go`, `control_runtime_status.go`
  (status readout), `private_api_config_test.go` (extend existing probes).

Corrections applied:

- **Rationale was wrong**: cove does not "use neither" — both are probed at
  `private_api_config_test.go:46-55` and `private_api_live_test.go:740`. The
  correct claim is bound and probe-tested, never applied to a real VM config.
- **Add `MaximumAllowedOvercommittedMemorySize`** to the status readout; a
  density knob without the host's overcommit ceiling is unusable.
- **Characterization comes first**: does overcommit actually let
  `sum(guest RAM) > host RAM`, or merely relax a validation check? Is
  termination a jetsam-style kill or a graceful VZ stop? Re-rate to medium once
  answered.
- **Termination interacts with suspend/resume and `fork_ephemeral`** — a
  jetsam-style kill bypasses the `suspend.vmstate` write and looks like a
  corrupt-VM bug to a fleet worker. Surface a stop reason in
  `control_runtime_status.go`, not just a boolean.
- Both flags stay opt-in env/flag gated. Never a default.

### 2.6 Restricted mode for untrusted sandbox VMs

- Value medium -> **low-medium**. Effort **S** (spike only). Risk **high**.
- Symbols: `vz_virtual_machine.gen.go:312`, `:322`
  (`CanEnterRestrictedModeWithCompletionHandler`), `:487`, `:496`; unexported
  sync wrapper `_enterRestrictedMode(ctx)` at `:883` (unexported — use the
  completion-handler form). Probes: `private_api_live_test.go:677`,
  `private_api_control_test.go:159`, `:67-68`.
- Cove touchpoints (**only after the spike**): `disposable.go`,
  `runtime_lifecycle.go`, `control_runtime_status.go`, `ctl.go`.

Corrections applied:

- **The security claim is unverified.** "This VM cannot be escalated from the
  host side after handoff" must not appear in any security-facing copy on the
  strength of a selector name. Value is informational until characterized.
- **Schedule a spike, not a feature.** Deliverable: a throwaway-VM
  characterization run plus a recorded result. Only if usable does the M-sized
  CLI plumbing get scheduled separately. Do not scope the CLI surface first.
- Risk framing: it is not merely that `_leaveRestrictedMode` is absent
  (verified) — the whole post-transition device / save-restore / input contract
  is unknown, the same epistemic bucket as §4's `x/vzkit/exp/*` verdict. Run
  only on a disposable VM with no snapshots of value, never on a
  suspend/resume path, and require
  `ValidateRestrictedModeSupportWithError` to pass first.
- **Kill criterion**: if entry breaks save/restore, hot-add, or input for the
  boot, move it to §4 with the evidence and stop re-litigating.

### 2.7 Key management for encrypted save states: `VZWrappingKey` + aux-storage UID key

- Value **medium** (as a falsifiable characterization). Effort **M**. Risk
  **high**.
- Symbols: `vz_wrapping_key.gen.go:101`, `:115`, `:129`;
  `vz_mac_auxiliary_storage.gen.go:257` (`_initializeUIDKeyWithWrappingKey:error:`),
  `:265` (`CanInitializeUIDKeyWithWrappingKeyError`);
  `vz_virtual_machine_save_options.gen.go:108`; `vz_virtual_machine.gen.go:399`
  (`SaveMachineStateToURLOptionsCompletionHandler`).
- Cove touchpoints: **the encrypt flag is already wired** —
  `cmd/cove/runtime_private.go:335`/`:389`
  (`privateSaveOptionsEnabledForRun` + `options.SetEncrypt`), plumbed via
  `vmrun_adapter.go` `RunConfig.SaveEncrypt`. The real work is the
  wrapping-key/UID-key probe. Drop `macos.go` and `fork_ephemeral.go` from the
  touchpoint list; keep `snapshots.go`, `runtime_private.go`.

This is the missing half of base-roadmap 3.6, which ships compression and
defers encryption for want of "a key-management story". `VZWrappingKey` is the
candidate story: three constructors (AES key, asymmetric `SecKey`, password)
and exactly one consumer in the private package.

Corrections applied:

- **Risk raised to HIGH.** `_initializeUIDKeyWithWrappingKey:` mutates the aux
  storage that carries VM identity, created once at install with the hardware
  model. A botched probe bricks the VM. Mandate a fresh throwaway VM directory
  plus a pre-probe copy of `aux.img`; never expose on an existing VM path.
- **The linkage is unproven.** `VZVirtualMachineSaveOptions` exposes only
  Compress/Encrypt with no key parameter (verified: the file has exactly those
  four accessors). If `SetEncrypt` turns out to be UID-key-independent, or the
  selector is absent on the current floor, the deliverable is a §4
  do-not-re-litigate entry — which is itself worth the M.

## 3. Input and perception

### 3.1 Real scroll injection (highest-value item in this addendum)

- Value **high** (scroll only). Effort M -> **M/L**. Risk **M**.
- Primary path (recommended): AppKit view `scrollWheel:` on
  `VZVirtualMachineView`, constructed via `+[NSEvent eventWithCGEvent:]` from
  `CGEventCreateScrollWheelEvent`. Selector presence already probed at
  `cmd/cove/private_api_hid_test.go:296-297` (alongside `magnifyWithEvent:` /
  `rotateWithEvent:`).
- Gated fallback / diagnostic (`COVE_POINTER_DELIVERY=hid`): private
  `VZScrollWheelEvent`
  (`apple/private/virtualization/vz_scroll_wheel_event.gen.go:119`) sent via
  `VZVirtualMachine.SendScrollWheelEventsPointingDeviceIndex`
  (`vz_virtual_machine.gen.go:526`), routed through `x/vzkit/vminput`
  (`vminput.go:192`), never raw off-queue — the `_shouldSendHIDReports`
  readiness-gate lesson applies. Selector probe: `private_api_hid_test.go:39`.
- Cove touchpoints: `agent_sandbox_anthropic.go` (replace the PageUp/PageDown
  fake at `:81-87`), `control_socket.go`, `control_socket_commands.go`,
  `ctl.go`, `automation_backend.go`.

Corrections applied:

- **Make the view path primary, not the private path.** Memory records the view
  path as the settled default since `e3d004d5`, and the HID path as
  diagnostic-only after being live-verified to land clicks at the wrong
  position. The view path also sidesteps the opaque-array unknown below.
- **Two empirical unknowns on the private path, not one.** Unlike
  `sendMouseEvents:`/`sendKeyboardEvents:` (typed `VZOpaque*Events`),
  `sendScrollWheelEvents:` and every gesture sender take a bare
  `unsafe.Pointer`; the events-array packing (count prefix? NSArray? C array of
  event ids?) is ungenerated, on top of the unenumerated
  `scrollPhase`/`momentumPhase` `uint64`s.
- **Drop multi-touch from this slice.** `_setMultiTouchDevices:`
  (`vz_virtual_machine_configuration.gen.go:538`) plus
  `vz_multi_touch_device_configuration.gen.go:112` require a boot-time config
  change and reboot, have uncharacterized runtime semantics, and share the
  unknown array packing. Separate proposal.
- **Magnify/rotate/smartMagnify/quickLook are medium at best** — no named
  consumer. Ship behind the same plumbing only after scroll is live-verified.

### 3.2 OCR request tuning: region of interest, level, vocabulary, minimum text height

- Value high -> **medium-high**. Effort S -> **S/M**. Risk **low**.
- Symbols: `apple/vision/vn_image_based_request.gen.go:143`
  (`SetRegionOfInterest`); `vn_recognize_text_request.gen.go:194`, `:238`,
  `:258`, `:288`, `:306`, `:323`; current call site
  `apple/x/vzkit/ocr/ocr.go:157` (`recognizeTextInData` hardcodes Accurate +
  UsesLanguageCorrection and sets nothing else).
- Cove touchpoints: `x/vzkit/ocr/ocr.go`, `cmd/cove/ocr_search_options.go`,
  `control_socket_ocr.go`, `screen_detection_ocr.go`, `setup_assistant.go`,
  `boot_commands.go`.
- Gates: all public, no new OS floor (except `AutomaticallyDetectsLanguage`,
  macOS 13+). No regen.

Four deltas: (1) `SetRegionOfInterest` so `ocr-wait`/`ocr-click` on a known band
(menu bar, dialog button row, Setup Assistant footer) costs a fraction of a
full-frame pass; (2) `SetRecognitionLevel(Fast)` for presence polling, with
Accurate reserved for the confirming read; (3) a UI vocabulary; (4)
`SetMinimumTextHeight` to drop sub-threshold noise producing spurious
`BestMatch` hits.

Corrections applied:

- **`SetCustomWords` + `SetUsesLanguageCorrection(false)` are mutually
  defeating** — `customWords` is only consulted during language correction.
  Split into two modes: *UI-chrome* (correction ON + customWords: "Continue",
  "Go Back", "Set Up Later") and *identifier* (correction OFF, no customWords,
  for hostnames/paths/VM names).
- **The ROI coordinate space is a trap.** Vision ROI is normalized bottom-left
  of the *submitted* image, while cove's screenshots are already cropped device
  pixels (title-bar/Retina discipline in `screenshots.go`). Express ROI against
  the post-crop buffer through one shared converter; per-call math will
  silently OCR the wrong band.
- **`SetMinimumTextHeight` is a fraction of image height** and will silently
  drop legitimate small guest text (menu bar at 1920x1200). Default 0; raise
  per call site only with a measured threshold.
- Acceptance gate: a before/after timing on one `ocr-wait` loop.
- Defer `SupportedRecognitionLanguagesAndReturnError` into the existing
  `cove doctor` work — unrelated to the latency thesis.

### 3.3 Hardware dirty-rect change detection (SCStream attachments)

- Value high -> **medium-high**. Effort **M** (dirty rects alone). Risk **M**.
- Symbols: `apple/screencapturekit/global_vars.gen.go:64`, `:134`
  (`SCStreamFrameInfoDirtyRects`); `apple/coremedia/functions.gen.go:3008`.
- Cove touchpoints: `internal/sckit/stream_darwin.go`, `stream.go`,
  `cmd/cove/control_socket_ocr.go`, `screen_detection.go`, `vzscript.go`.
- Gates: macOS 12+, Screen Recording TCC (already required by sckit). Public
  API.

Base roadmap 3.12 answers "did the screen change?" with a private
`VZFramebufferObserver`. Cove's own SCStream already receives per-frame
attachments carrying the exact changed regions and drops them on the floor —
the output callback only converts pixels.

Corrections applied:

- **Two dependencies cap the value.** Dirty rects exist only while the SCStream
  backend is running (base roadmap 3.2, unshipped), and `StartStream` is
  windowID-scoped (`stream_darwin.go:29`), so headless VMs get nothing and
  still need the framebuffer path — exactly the cuabench/vzscript case that
  most wants settle detection.
- **Unlisted empirical risk**: VM windows are GPU/IOSurface-composited, and
  such surfaces commonly report a single whole-frame dirty rect, collapsing the
  region hint to a no-op. **Gate the slice on a one-hour probe** printing
  dirty-rect counts/areas for a live VM window before building the OCR-subrect
  path. If rects are whole-frame, only the boolean "changed" event survives and
  value drops to medium.
- **Split the original proposal.** Dirty rects (M, low risk) are slice 1. A
  scalar change score via `CIColorAbsoluteDifference` + `CIAreaAverage`
  (`apple/coreimage/ci_filter.gen.go:818`, `:1847`) is a separate **S**.
  `VTMotionEstimationSession`
  (`apple/videotoolbox/functions.gen.go:888`, `:914`,
  `typedefs.gen.go:77`) is a separate **M** and **speculative/optional** — it
  is macOS 26.0-only and dlsym-gated in the binding, so most hosts never
  exercise it.

### 3.4 GPU-side frame format conversion (replacing the per-pixel BGRA loop)

- Value high -> **medium**. Effort M -> **S/M**. Risk **M**.
- Symbols: current loop at `internal/sckit/stream_darwin.go:128-140`;
  `apple/corevideo/functions.gen.go:1803` (`CVPixelBufferGetIOSurface`);
  `apple/coreimage/ci_image.gen.go:1244` (`InitWithCVImageBuffer`), `:1356`
  (`InitWithIOSurface`); `ci_context.gen.go:457`, `:543`, `:628`;
  `apple/metal/device_protocol.gen.go:2462`;
  `apple/videotoolbox/functions.gen.go:348`
  (`VTCreateCGImageFromCVPixelBuffer`).
- Cove touchpoints: `internal/sckit/stream_darwin.go`, `sckit_darwin.go`,
  `cmd/cove/screenshots.go`, `screenshots_framebuffer_darwin.go`.

Corrections applied:

- **"Zero-copy" oversells it.** Every downstream Go consumer (OCR, PNG encode)
  needs an `*image.RGBA`, so a host-memory copy remains. The real win is moving
  the swizzle to the GPU: 2.3M scalar Go iterations become one bulk memcpy or
  one `VTCreateCGImageFromCVPixelBuffer` call. Reword as "GPU-side format
  conversion".
- **Sequence it after base roadmap 3.2.** The stated payoff (MJPEG/preview
  frame rate) belongs to a continuous-stream consumer that does not exist yet;
  today's only consumer is one-shot `sckit.CaptureWindow`, where the loop
  amortizes to a few ms and is not the bottleneck.
- **Ship the single-call form first.** `VTCreateCGImageFromCVPixelBuffer` is a
  drop-in that likely captures most of the win with no CIContext/Metal lifetime
  management, and retires most of the risk. Escalate to CIContext/IOSurface
  only if profiling justifies it. (Note this call exists despite the base
  roadmap's correct finding that `VTCompressionSessionEncodeFrame` is missing —
  §4's VideoToolbox verdict is too broad if read as "wholly unusable".)
- If the CIContext path is used: hoist context creation out of the per-frame
  callback, do not run long renders on the SCStream dispatch queue, and drop
  (not nest) the existing `CVPixelBufferLockBaseAddress`.

### 3.5 Live SCStream reconfiguration (resolution/crop/frame-rate without teardown)

- Value **medium**. Effort **S**. Risk **low-M**.
- Symbols: `apple/screencapturekit/sc_stream.gen.go:371`
  (`UpdateConfiguration`), `:386` (`UpdateContentFilter`);
  `sc_stream_configuration.gen.go:421`, `:437` (`SetSourceRect`), `:453`
  (`SetDestinationRect`), `:694`; current fixed sizing at
  `internal/sckit/stream_darwin.go:55-58`.
- Cove touchpoints: `internal/sckit/stream_darwin.go`, `stream.go`,
  `cmd/cove/control_socket.go`, `windows.go`.

Corrections applied:

- **Symbol fix**: `SetSourceRect` is `:437`, not `:453`.
- **The stated frame-geometry risk mostly does not exist**: sckit stores frames
  as `image.Image`, so each frame already carries its own bounds. The real
  requirement is that `Snapshot` callers stop assuming stable dimensions across
  calls.
- **Understated risk**: `stream_darwin.go` has no mutex around `cfg`/`stream`,
  so a `Reconfigure` API needs its own lock plus interaction with the existing
  `noteStop`/`stopped` path — a reconfigure racing a stopped stream must return
  the stop error, not hang on a handler that never fires. Callers need a
  bounded context.
- **The multi-consumer framing is wrong**: one SCStream has one configuration.
  Serving a cheap 640px preview and full-res OCR simultaneously needs two
  streams or a host-side downscale — a separate **M**.

### 3.6 Screen fingerprinting via `VNGenerateImageFeaturePrint`

- Value high -> **medium-high**. Effort **M**. Risk **M**.
- Symbols: `apple/vision/vn_generate_image_feature_print_request.gen.go:102`,
  `:141` (`ImageCropAndScaleOption`, setter `:144`);
  `vn_feature_print_observation.gen.go:151`
  (`ComputeDistanceToFeaturePrintObservationError`), `:174` (`Data`), `:182`
  (`ElementCount`); `vn_request.gen.go:312` (`Results`), `:323`/`:327`
  (`Revision`/`SetRevision` — the risk mitigation depends on these).
- Cove touchpoints: `cmd/cove/screen_detection.go`, `control_socket_ocr.go`,
  `vzscript.go`, `snapshots.go` (fingerprint stamped alongside a snapshot), new
  sibling helper package `apple/x/vzkit/imgprint`.

Answers a question cove cannot answer today: "is this the same screen as
before?" Uses: cuabench flake localization (store a print per episode step,
diff a failing run against a passing one), settle detection robust to cursor
blink and clock ticks, `screen-matches golden.print` vzscript assertions, and
screenshot-corpus dedup. `Data()` persists prints as ~KB blobs.

Corrections applied:

- **Symbol fix**: `ImageCropAndScaleOption` is `:141`, not `:145`.
- **Value trimmed**: this is additive tooling, not unblocking, and a plain
  perceptual/dHash is a zero-dependency competitor for the settle-detection use
  case. The design must justify Vision over dHash.
- **The request crops and downscales before embedding** (hence
  `imageCropAndScaleOption`), so a 1920x1200 screen is heavily downsampled,
  amplifying the known dialog-collapse failure mode. Calibration must cover
  ScaleFill vs CenterCrop and per-region tiled prints, not just a whole-screen
  threshold.
- **Stored prints must record the pinned revision** and be invalidated on
  mismatch, or golden-state assertions silently rot across macOS updates.

### 3.7 Interaction verification via `VNSequenceRequestHandler` tracking

- Value high -> **medium**. Effort **M** (host-side spike). Risk **M-H**.
- Symbols: `apple/vision/vn_track_object_request.gen.go:135`, `:148`;
  `vn_track_rectangle_request.gen.go:137`; `vn_tracking_request.gen.go:108`
  (`InputObservation`), `:111` (`TrackingLevel`), `:115` (`SetLastFrame`);
  `vn_sequence_request_handler.gen.go:171`, `:271`;
  `vn_detected_object_observation.gen.go:163` (`BoundingBox`);
  `vn_observation.gen.go:190` (`Confidence`); `vn_request.gen.go:312`
  (`Results` returns base `VNObservation` — confirm a
  `VNDetectedObjectObservationFrom` downcast helper before scoping).
- Cove touchpoints: `cmd/cove/control_socket.go` (`sendMouseEventVMDirect`),
  `screen_detection.go`, `vzscript.go`.

Corrections applied:

- **A cheaper baseline already exists.** Framebuffer/pixel diff plus OCR diff
  over two screenshots (axmcp ships `ax_ocr_action_diff`) covers general "did
  the step take effect". Scope this slice to beat that baseline, not merely to
  exist. The non-substitutable value is narrower: continuous sub-step
  drag/scroll trajectories and a coordinate-mapping regression assertion
  ("moved by the commanded vector").
- **The cursor may not be in the frame at all.** The guest pointer is not
  guaranteed to be composited into captures on either the `CGWindowList` path
  or the private PGDisplay path. Verify before scoping any cursor tracking; if
  absent, only window/element chrome tracking survives.
- **Synthetic UI is the documented weak case** for `VNTrackObjectRequest` (flat
  untextured regions, teleporting cursor, instant redraw). Lost or
  low-confidence track must be a hard requirement to report "unknown", never
  "failed", with OCR/pixel checks authoritative — otherwise this becomes a
  flaky-signal generator in cuabench.
- The `CVPixelBuffer`/`CMSampleBuffer` overloads only pay off after base
  roadmap 3.2; do not let this slice pull SCStream forward.
- **Gate on a one-afternoon probe** (seed a track on known window chrome, drag,
  read `BoundingBox` deltas). If confidence collapses on flat UI, fall back to
  `VNTrackRectangleRequest` seeded from base roadmap 3.10 and kill the
  cursor-tracking half.
- The sequence handler is stateful and not thread-safe — one handler per
  tracked sequence, pinned to one goroutine/queue.

### 3.8 `NLEmbedding` distance as an OCR match tie-breaker

- Value **low**. Effort **S**. Risk **medium** (raised).
- Symbols: `apple/naturallanguage/nl_embedding.gen.go:227`, `:356`, `:457`,
  `:492`; scoring site `apple/x/vzkit/ocr/ocr.go:115`.
- Cove touchpoints: `x/vzkit/ocr/ocr.go` (`BestMatch`),
  `cmd/cove/ocr_search_options.go`, `vzscript.go` (`ocr-click`). Opt-in
  `SearchOption`, default off.

Corrections applied:

- **The headline example does not work.** `BestMatch` pre-filters candidates
  with `strings.Contains(strings.ToLower(obs.Text), needle)` *before* ranking,
  so a semantic tie-breaker can only reorder observations that already contain
  the needle. `ocr-click "accept the license"` -> "Agree" yields zero
  candidates and never reaches the scorer. The in-scope payoff is exactly the
  "agree" inside "Agreement" disambiguation from project memory. Drop the
  "survives macOS wording changes" claim — that needs loosening the `Contains`
  gate, which the proposal's own never-primary-matcher guardrail forbids.
- **Word embeddings are single-token.** Multi-word labels ("Continue Anyway")
  need `SentenceEmbeddingForLanguage`, whose language coverage is narrower, so
  the nil-check fallback fires more often than implied.
- **Risk raised to medium** because semantically-near/consequentially-opposite
  pairs (Continue vs Cancel, Agree vs Disagree) sit directly on the Setup
  Assistant path where a wrong click is destructive and hard to detect. Mandate
  a hardcoded destructive-label denylist the semantic path can never select,
  plus a minimum lexical-score floor.

### 3.9 Guest audio capture in `cove record`

- Value **low-medium**. Effort **M** (file-recording half only). Risk **M**.
- Symbols: `apple/screencapturekit/sc_stream_configuration.gen.go:710`/`:713`
  (`CapturesAudio`/setter), `:727`/`:730` (SampleRate), `:744`/`:747`
  (ChannelCount), `:761`/`:764` (ExcludesCurrentProcessAudio), `:806`
  (`CaptureMicrophone` — leave off, needs mic TCC);
  `screencapturekit/enums.gen.go:359` (`SCStreamOutputTypeAudio`);
  `sc_stream.gen.go:266`; `apple/coremedia/functions.gen.go:2840`
  (`GetFormatDescription`), `:2882` (`GetNumSamples`).
- Cove touchpoints: `internal/sckit/stream_darwin.go`,
  `cmd/cove/recording_cli.go`, `control_socket_commands.go`.

Base roadmap §4 correctly kills guest-audio capture via the VZ audio device and
says it must go through ScreenCaptureKit `SetCapturesAudio` — but never
proposes it, so `cove record` (3.3) ships silent video.

Corrections applied:

- **The key config line was backwards.** VZ guest audio is emitted by cove's
  *own* process via `VZHostAudioOutputStreamSink`, so
  `SetExcludesCurrentProcessAudio(true)` would exclude exactly the audio being
  captured, yielding a silent track. Leave it **false** (default); handle
  feedback by not routing cove's own UI sounds through the same process output.
  Validate live: if SCK cannot separate guest playback from cove's own output,
  the feature degrades to "captures whatever cove plays", still usable but
  differently framed.
- **Floor correction**: base roadmap 3.3 pins `cove record` to
  `SCRecordingOutput`, which is macOS **15+**, not 13+. So record-with-audio is
  15+ and needs no regen; anything below 15, or any PCM analysis, needs regen.
- **PCM extraction is blocked**:
  `CMSampleBufferGetAudioBufferListWithRetainedBlockBuffer` is absent from the
  bindings (0 hits in `coremedia/functions.gen.go`). Bundle it with
  `AVAssetWriterInput.appendSampleBuffer:` (already flagged absent in §4) as
  **one** binding-regen request.
- **Justify on cuabench episode replay alone** — the boot-chime diagnostic is
  weak (a VZ firmware chime through the paravirtual audio path is unverified).
- Split the activity-signal half out as a separate gated follow-up; it is not
  M. Drop `screenshots.go` from touchpoints (frame capture, not audio).

## 4. Host and network surfaces

### 4.1 Control-socket peer attestation via `SecCode`/`SecTask`

- Value high -> **medium**. Effort **M**. Risk **M**.
- Symbols: `apple/security/functions.gen.go:6331` (`SecCodeCheckValidity`),
  `:6352`, `:6394` (`SecCodeCopyGuestWithAttributes`), `:6478`
  (`SecCodeCopySigningInformation`), `:8063`
  (`SecRequirementCreateWithString`), `:8231`
  (`SecTaskCopySigningIdentifier`), `:8252`
  (`SecTaskCopyValueForEntitlement`), `:8315`
  (`SecTaskCreateWithAuditToken`); `security/global_vars.gen.go:955`
  (`KSecGuestAttributeAudit`), `:975` (`KSecGuestAttributePid`).
- Cove touchpoints: `cmd/cove/control_socket.go`, `control_http_listener.go`,
  `gateway_token.go`, `security_cli.go`, `keychain.go`.

Base roadmap's runner-up "code-sign verification of injected agents" is about
the guest agent *binary*. The unmined half is the *host peer*:
`authorizeRequest` (`control_socket.go:743`) is a plain string compare against
a 0600 token file (`:369`), so any process running as the user that can read
`~/.vz/vms/<vm>/control.token` gets full VM control — screenshots, keystroke
injection, disk ops. Logging `peer_identifier`/`peer_cdhash`/`peer_valid` in
the audit trail turns "a token was presented" into "this signed binary asked".

Corrections applied:

- **This does not close the hole it motivates.** As scoped (observe-only) it
  improves the audit trail. The enforcing form is deferred, and under cove's
  constant ad-hoc re-signing (`codesign -s - -f` after every build) a
  cdhash-pinned requirement churns on every rebuild — enforcement may never be
  practical for dev builds. Ship the logging slice on its own merits.
- **`SO_PEERPID` does not exist on Darwin.** Use
  `getsockopt(fd, SOL_LOCAL, LOCAL_PEERPID)` and `LOCAL_PEERTOKEN` (which
  returns the `audit_token_t` feeding `SecTaskCreateWithAuditToken`'s
  `[32]byte`). `LOCAL_PEERTOKEN` is the TOCTOU-free path and should be
  **primary**; the pid / `SecCodeCopyGuestWithAttributes` route is the
  fallback, not the other way round.
- Getting the raw fd requires the connection be a `*net.UnixConn` plus a
  `SyscallConn().Control` call — verify the accept path in
  `control_socket.go` does not wrap conns behind an interface that loses the
  fd before this lands.
- A wrong requirement string locks the user out of their own socket: advisory
  log-only before enforcing, with a configurable requirement.

### 4.2 Offline `.logarchive` ingestion + signpost boot timing

- Value **medium**, asymmetric (see below). Effort S -> **M**. Risk: half (a)
  low, half (b) **M-H**.
- Symbols: `apple/oslog/os_log_store.gen.go:147`
  (`NewOSLogStoreWithURLError`), `:186`
  (`PositionWithTimeIntervalSinceLatestBoot`), `:221`
  (`EntriesEnumeratorWithOptionsPositionPredicateError`), `:241`
  (`LocalStoreAndReturnError` — the contrast symbol the gate argument depends
  on); `oslog/enums.gen.go:129` (`OSLogEnumeratorReverse`);
  `os_log_entry_signpost.gen.go:150`, `:224`, `:232`, `:240`.
- Cove touchpoints: `cmd/cove/apple_logs.go`, `support_bundle.go`, `logs.go`,
  `run_metrics.go`.
- Gates: `NewOSLogStoreWithURLError` over a user-owned `.logarchive` needs **no**
  `com.apple.logging.local-store` entitlement — that gate applies only to
  `LocalStoreAndReturnError`, which is the form base roadmap 3.5 uses. This is
  therefore the ad-hoc-signing-safe half of the OSLog story and the working
  fallback if 3.5's entitlement is refused. macOS 10.15+.

Two halves: (a) capture a bounded archive on the failing machine and parse it
later on a different host, structured, with the same NSPredicate — the piece
that makes fleet triage of a remote worker possible; (b) `OSLogEntrySignpost`
begin/end pairs plus `OSLogEnumeratorReverse` and
`PositionWithTimeIntervalSinceLatestBoot` for real phase timing (install,
restore, boot) in `run_metrics.go`.

Corrections applied:

- **Effort S -> M**: this is two features across four files. Half (a) alone is
  S.
- **Half (b) is gated on a sampling probe.** If VZ / coreaudiod /
  mobile-restore emit no signposts, half (b) delivers literally nothing and the
  "real phase timing" rationale evaporates. Require: enumerate
  `OSLogEntrySignpost` over a captured install/boot archive and count distinct
  `Subsystem`+`SignpostName` before any `run_metrics.go` work. Ship (a)
  regardless.
- **Unstated dependency**: the proposal never says how the `.logarchive` is
  produced. `NewOSLogStoreWithURLError` only reads one; capture still shells
  out to `log collect --output … --last <dur>`, so support-bundle capture keeps
  a subprocess dependency even though parsing becomes in-process. Pair the size
  cap with a bounded `--last` window — a `.logarchive` is a directory bundle
  and cannot be safely trimmed after the fact.
- **Value is asymmetric**: half (a) is load-bearing and justifies the slice
  alone.

### 4.3 `NWPathMonitor`-gated dial-out and host network health

- Value **medium**. Effort **S**. Risk **low**.
- Symbols: `apple/network/functions.gen.go:5983` (`NWPathMonitorCreate`),
  `:6090` (`SetQueue`), `:6113` (`SetUpdateHandler`), `:6133` (`Start`),
  `:5774` (`NWPathGetStatus`), `:5921` (`NWPathIsExpensive`), `:5734`
  (`NWPathEnumerateInterfaces`).
- Cove touchpoints: `cmd/cove/fleet_health.go`, `doctor_host.go`,
  `serve_gateway.go`, `run_worker.go`, `networking.go`.
- Gates: macOS 10.15+, no entitlement, no TCC (path status is not the Local
  Network permission surface). Pure additive telemetry.

Design 046's worker model is dial-out; a worker that loses uplink currently
discovers this by failing a request and retrying blind. Event-driven
satisfied/unsatisfied/requiresConnection gates reconnect backoff, and
`NWPathIsExpensive` lets a tethered worker refuse large image pulls.

Corrections applied:

- **The "enabling work for Bonjour" argument is overstated** — block bridging
  and retention already exist in the generated bindings. Value rests solely on
  dial-out gating plus the expensive-link guard, not on sequencing.
- **The exported `NWPathMonitor*` wrappers panic when the symbol is
  unresolved** (`try*` returns `symbolCallError`; the exported form panics). A
  long-lived worker must call the `try*` variants or recover.
- **Drop `NWPathMonitorCreateWithType`** (`:6025`) from the first slice — the
  proposal itself concedes the type enum misleads on multihomed hosts. Use
  plain `NWPathMonitorCreate` plus `NWPathEnumerateInterfaces` for the
  interface list.
- Path status reflects host reachability, not controller liveness — still needs
  an app-level health check.

### 4.4 Bonjour fleet discovery (`_cove-worker._tcp`)

- Value high -> **medium**. Effort **M**. Risk **M**.
- Symbols: `apple/network/functions.gen.go:232`
  (`NWAdvertiseDescriptorCreateBonjourService`), `:376`
  (`NWBrowseDescriptorCreateBonjourService`), `:501`
  (`NWBrowseResultCopyEndpoint`), `:522` (`…CopyTXTRecordObject`), `:670`
  (`NWBrowserCreate`), `:697` (`SetBrowseResultsChangedHandler`), `:762`
  (`NWBrowserStart`), `:4337` (`NWListenerCreateWithPort`), `:4379`
  (`NWListenerGetPort`), `:4401` (`NWListenerSetAdvertiseDescriptor`), `:8746`
  (`NWTXTRecordCreateDictionary`), `:8893` (`NWTXTRecordSetKey`).
- Cove touchpoints: `cmd/cove/serve_gateway.go`, `fleet_cli.go`,
  `fleet_health.go`, `serve.go`, `daemon_cli.go`.
- Gates: dlsym-gated `try*` wrappers, macOS 10.15+. No entitlement for
  advertise/browse on a non-sandboxed binary, but the browsing side **does**
  trip the macOS 15+ Local Network privacy prompt
  (`kTCCServiceLocalNetwork`) — probe live and document a fallback to explicit
  host lists. Works under ad-hoc signing.

`serve_gateway.go:382` `discoverConfiguredVMs` only enumerates local
`~/.vz/vms` via fsnotify; every cross-host path assumes a configured address.
Cove's existing `-vnc-bonjour` flag (`runtime_private.go:29`) is the private VZ
VNC server advertising itself, not cove's control plane.

Corrections applied:

- **Value trimmed**: design 046's controller already gets workers via dial-out
  registration, so this is a zero-config convenience for lab LANs, not "the
  single highest-leverage missing piece". Soften that rationale sentence.
- **§4's XPC trust verdict applies here too**: ad-hoc signing means no
  team-ID-based peer trust, so the TXT payload and any discovered endpoint are
  untrusted and must still be gated by the existing `control.token` auth.
  Discovery must not become an auth bypass.
- TXT limits: ~1300 B is a practical single-packet limit; the hard per-key
  limit is 255 B. Host id + version + capacity fits comfortably.
- A pure-Go mDNS library is a viable alternative; the justification for the NW
  path is dependency avoidance plus first-class TXT control, not necessity.
- Bonjour is LAN-scoped — no help across subnets/WAN, where dial-out still
  applies.

### 4.5 Host sleep/wake-aware VM lifecycle

- Value **high** for headless/fleet and long installs, modest for interactive
  GUI. Effort M -> **M/L**. Risk **M**.
- Symbols: `apple/foundation/ns_process_info.gen.go:607`
  (`BeginActivityWithOptionsReason`), `:618` (`EndActivity`);
  `foundation/enums.gen.go:121`/`:123`
  (`NSActivityIdleDisplaySleepDisabled`/`IdleSystemSleepDisabled`);
  `apple/appkit/global_vars.gen.go:2783` (`WorkspaceWillSleepNotification`),
  `:2756` (`WorkspaceDidWakeNotification`);
  `appkit/ns_workspace.gen.go:1439` (`SharedWorkspace`), `:1177`
  (`NotificationCenter`);
  `foundation/ns_notification_center.gen.go:241`
  (`AddObserverForNameObjectQueueUsingBlock`);
  `apple/iokit/functions.gen.go:6114` (`IOPMAssertionCreateWithDescription`,
  fallback only).
- Cove touchpoints: `cmd/cove/runtime_lifecycle.go`, `snapshots.go`,
  `main.go`, `control_runtime_status.go`, new `cmd/cove/host_power.go`.
- Gates: macOS 10.9+ for NSActivity; NSWorkspace notifications need a running
  NSApplication/CFRunLoop. No entitlement, no TCC. `iokit` has **no**
  `global_vars.gen.go`, so the `kIOPMAssertionType*` CFStrings are unbound and
  the IOPM path needs hand-built CFStrings — prefer the NSProcessInfo activity
  API, which needs no constants.

Cove has zero host power integration today (no `IOPMAssertion`, no
`NSWorkspace`, no `caffeinate` anywhere; `ctl_power.go` is guest-side sleep
settings). A long install or a headless fleet worker is silently killed when
the host idle-sleeps, and a woken VM has no resume hook.

Corrections applied:

- **Citation fixes**: `WorkspaceWillSleepNotification` is `:2783`;
  `SharedWorkspace` is `:1439`.
- **The headless path is the real work.** `cmd/cove` has no
  `CFRunLoopRun`/`NSApp.Run` in headless `cove run`, so this needs new
  main-thread run-loop plumbing plus the documented main-queue hazard
  workaround (CLAUDE.md, "Main Queue Dispatch from Background Threads"). The
  GUI path alone is S.
- **`NSActivityIdleSystemSleepDisabled` only blocks *idle* sleep** — a lid
  close or explicit Sleep still sleeps the host, so the best-effort
  `WillSleep` handler is load-bearing, not optional. WillSleep gives only a few
  seconds, which a full `_saveMachineStateToURL` can exceed; treat it as
  best-effort pause plus optional save.
- **Sleep inhibition must be released on every exit path**, or the host never
  sleeps again. An NSActivity dies with the process (safe failure mode); an
  IOPM assertion would leak — another reason to stay on NSProcessInfo. Note the
  exported IOPM wrapper panics on load failure; use the internal `try*` form.
- **Slice it**: ship the NSActivity inhibition-while-running half first (small,
  public, no run-loop work); gate the WillSleep/DidWake observer behind the
  headless run-loop work.

### 4.6 Thermal / low-power admission control

- Value high -> **medium-high**. Effort **S** (thermal + low-power only). Risk
  **low**.
- Symbols: `apple/foundation/ns_process_info.gen.go:893` (`ThermalState`),
  `:931` (`IsLowPowerModeEnabled`), `:948` (`ProcessInfoClass.ProcessInfo`);
  `foundation/enums.gen.go:3778-3784` (`NSProcessInfoThermalState`,
  Fair=1/Critical=3); `foundation/global_vars.gen.go:216`
  (`ProcessInfoThermalStateDidChangeNotification`).
- Cove touchpoints: `cmd/cove/doctor_host.go`, `control_runtime_status.go`,
  `disk_bench.go`, design 046 fleet worker admission logic.
- Gates: no entitlement, no TCC, macOS 10.10+.

Corrections applied:

- **`internal/guibench` does not exist in this repo** — the cuabench corpus
  landed externally on `cove-cuabench-current`. Drop it from the in-repo
  touchpoint list.
- **Annotate before gating.** Nothing consumes the signal today, so value is
  contingent on a fleet worker and a bench runner reading it. Ship
  `thermalState` + `lowPowerMode` in `doctor` and runtime status, stamped into
  `disk_bench` samples, before any admission gate.
- **On a mains-powered desktop host `ThermalState` is effectively pinned at
  Nominal**, so the gate is near-inert on exactly the hosts a fleet runs on and
  only bites on laptops. Gate behind a flag, default off, with a bounded wait
  and an override — over-strict gating could stall a worker indefinitely.
- **Keep IOPS out of slice 1.** `IOPSCopyPowerSourcesInfo`
  (`apple/iokit/functions.gen.go:6575`) and `IOPSGetTimeRemainingEstimate`
  (`:6701`) need hand-built CFString keys (no `iokit/global_vars.gen.go`) and
  both **panic** on dlsym failure — never call the panicking wrappers from a
  scheduler path. Separate S/M.
- Thermal state is coarse (4 levels) and lags real throttling: an admission
  gate and result annotation, not a measurement.

### 4.7 DiskArbitration for the inject/verify window

- Value high -> **medium**. Effort **M** (re-sliced). Risk **M**.
- Symbols: `apple/diskarbitration/functions.gen.go:197`
  (`DADiskCopyDescription`), `:260` (`DADiskCreateFromBSDName`), `:529`
  (`DADiskUnmount`), `:548` (`DADissenterCreate`), `:612`
  (`DARegisterDiskAppearedCallback`), `:692`
  (`DARegisterDiskMountApprovalCallback`), `:751` (`DASessionCreate`), `:814`
  (`DASessionSetDispatchQueue`); callback typedefs at
  `diskarbitration/typedefs.gen.go:15`, `:60`.
- Cove touchpoints: `cmd/cove/provision_mount.go`, `agent_inject.go`
  (currently parses `hdiutil info` text at `:481-510`), `guest_tools.go`,
  `provision_verify.go`. Note `cmd/cove/diskguard.go` now exists (untracked in
  the working tree) — check it before creating a new file.

Corrections applied:

- **The headline premise is false.** A running VZ VM's disk is opened by VZ as a
  plain file via `VZDiskImageStorageDeviceAttachment` — there is no host
  `/dev/diskN` node, so diskarbitrationd/Finder have nothing to auto-mount and
  a mount-approval callback has no subject. The claimed "host auto-mounts a
  running VM's APFS volume" corruption vector does not exist on the run path.
  **Do not pitch this as protecting a running VM.**
- The durable win is the other half: structured DA descriptions replacing
  `hdiutil`-plist / awk / `diskutil` text scraping, plus
  `DADiskAppearedCallback`-driven wait-for-volume replacing poll loops during
  inject. The veto only has a subject during cove's own inject/verify window,
  which `-nobrowse`/`-nomount` already largely covers.
- **Callback risk was misstated both ways.** Overstated: the bindings type the
  callbacks as ordinary Go funcs, so no hand-rolled `purego.NewCallback` is
  needed. Understated: purego's process-wide callback slot limit means
  callbacks must be registered **once per session**, not per attach —
  register/unregister churn across repeated inject runs can exhaust slots.
- **Re-slice**: slice 1 replaces the scraping; slice 2 adds a mount-approval
  veto scoped to cove-attached devices only. Drop `DADiskUnmount` from slice 1
  — it needs the same privileges as `hdiutil detach` and buys no new
  capability.
- A crashed cove leaves the approval callback gone, i.e. fail-open, which is
  the safe direction. Approval callbacks need a scheduled run loop or a
  dispatch queue — use `DASessionSetDispatchQueue` with cove's own queue to
  avoid the main-queue trap.

### 4.8 Storage hygiene via NSURL resource keys

- Value **medium** (split, see below). Effort **S**. Risk **low**.
- Symbols: `apple/foundation/nsurl.gen.go:1249`
  (`ResourceValuesForKeysError`), `:1319` (`SetResourceValueForKeyError`);
  `foundation/global_vars.gen.go:1957` (`URLIsExcludedFromBackupKey`), `:2159`
  (`URLVolumeAvailableCapacityForImportantUsageKey`), `:2167`
  (`URLVolumeAvailableCapacityKey`).
- Cove touchpoints: `cmd/cove/doctor_host.go`, `utils.go`, `compact.go`,
  `gc.go`, `disk_caching.go` (untracked, in flight), `internal/vmconfig`
  (BaseDir creation).
- Gates: macOS 10.8+/10.13+; no entitlement, no TCC. Under App Sandbox the
  write needs an existing powerbox grant for the VM root (modeled in
  `app_sandbox_guard.go`).

Cove sizes free space with `syscall.Statfs`, which ignores APFS purgeable
space, and nothing marks `~/.vz` excluded from Time Machine.

Corrections applied:

- **All three global keys are resolved lazily via `purego.Dlsym` at package
  init** (`global_vars.gen.go:8453`, `:9159`, `:9179`) and are left as the
  zero-value `NSURLResourceKey` when missing. Check for the empty key and fall
  back to `statfs` rather than issuing `setResourceValue:forKey:` with an
  empty key.
- **Value splits.** Backup exclusion is the higher-value half — a default Mac
  backing up hundreds of GB of sparse images is immediate user harm. The
  purgeable-aware preflight is closer to low value alone and can over-promise
  (`ForImportantUsage` counts purgeable space that may fail to purge).
- **Report both numbers** in `doctor` (statfs floor + `ForImportantUsage`
  ceiling) rather than replacing `statfs`; that also neutralizes the
  over-promise risk.
- The backup-exclusion write is a **user-visible policy change**: opt-in via a
  doctor suggestion / `--fix` / config flag, never implicit at
  `vmconfig.BaseDir()` creation, with a documented unset path.

### 4.9 `UNUserNotificationCenter` alerts for long-running VM events

- Value **medium**. Effort M -> **S/M**. Risk **M**.
- Symbols: `apple/usernotifications/un_user_notification_center.gen.go:328`
  (`RequestAuthorizationWithOptions…`), `:359`
  (`AddNotificationRequestWithCompletionHandler`), `:519`
  (`CurrentNotificationCenter`), `:614`/`:629` (synchronous convenience
  wrappers — prefer these over hand-rolled completion plumbing);
  `un_notification_request.gen.go:82` (type), `:184`
  (`NewUNNotificationRequestWithIdentifierContentTrigger`);
  `un_mutable_notification_content.gen.go:1`; `usernotifications/blocks.gen.go:18`
  (`BoolErrorHandler`), `:43` (`ErrorHandler`).
- Cove touchpoints: new `cmd/cove/notify.go`; call sites `installer.go`,
  `up.go`, `runtime_lifecycle.go`, `status_item.go`.

Corrections applied:

- **The probe must never call into UN.** Under purego there is no `@try`, so a
  raised `NSInternalInconsistencyException` from an unbundled context is an
  **uncatchable process abort**, not a recoverable error. Pre-check
  `NSBundle.mainBundle.bundleIdentifier != nil` *and* that the executable path
  is inside a `.app`; nil-center checking after the fact is too late.
- **The bundle prerequisite is already done on main** —
  `macgo_bundle.go` needs no work beyond reading the bundle id, so effort drops
  to S/M.
- Authorization state is keyed to bundle id + install path; the stable
  `~/Applications/cove.app` path is what makes it survive re-signing. Any
  change to the macgo bundle location re-prompts.
- `VZMAC_NO_MACGO=1` and any raw `./cove` invocation must no-op silently, and
  headless/CI installs must default to no-op rather than firing an async
  authorization prompt mid-install.

## 5. Unclaimed and half-used `x/` packages

### 5.1 Window-addressed input and off-space awareness via `x/skylight`

- Value high -> **medium**. Effort **M** diagnostic slice / **M-L** input
  slice. Risk **high**.
- Symbols: `apple/x/skylight/skylight.go:47` (`ActiveSpace`), `:60`
  (`SpacesForWindow`), `:114` (`IsWindowOffSpace`), `:162` (`WindowOwnerPID`),
  `:215` (`FocusWithoutRaise`), `:253` (`ActivateForMenuShortcut`), `:273`
  (`WithMenuShortcutActivation`); `x/skylight/mouse.go:47`
  (`RouteMouseEventToWindow`), `:91` (`PostEventToPID`);
  `x/skylight/event_record.go:112` (`EventRecordFromCGEvent`).
- Cove touchpoints: `cmd/cove/screenshots.go`, `control_socket.go`,
  `gui_control.go`, `window_frame.go`, `windows.go` (QEMU/VNC viewer window).
- Gates: private SkyLight SPI, version-sensitive — probe each symbol
  (`x/skylight` wraps `private/skylight` and returns a typed `spiError`).
  Accessibility TCC for CGEvent creation. No entitlement, no team id, so
  ad-hoc-signing compatible.

Host-side only. This does **not** revisit the settled "no public pointer
injection into protected guest UI" finding — it addresses host windows and
cove's own window.

Corrections applied:

- **Gap 1 (off-space/blank screenshots) overlaps base roadmap 3.1/3.12.** If
  framebuffer capture lands, off-space staleness for the VM window disappears.
  Reframe it as a cheap precondition/status check for the `CGWindowList` path
  and for non-VM host windows, not a headline. The durable value is
  window-addressed click delivery to out-of-process host windows plus
  `WithMenuShortcutActivation` without focus theft.
- **`cmd/cove/ax*.go` does not exist on main** — `cove ax` lives only on the
  unpushed `feature/embed-axmcp-cli` branch, and nothing under `cmd/` or
  `internal/` imports `axuiautomation`. Drop that touchpoint or make the slice
  explicitly depend on that branch landing.
- **`RouteMouseEventToWindow`'s window-local coordinate space needs the same
  view-points vs device-pixels audit that already burned the HID pointer
  path.** Budget a coordinate-transform validation step, not just a probe.
- Delivery success is not signalled by return status (the package documents
  this), so every use needs an effect check (screenshot/OCR diff).
- **Split into two slices**: a diagnostic slice
  (`IsWindowOffSpace`/`SpacesForWindow`/`WindowOwnerPID` in status and as a
  screenshot precondition), then window-addressed input with
  effect-verification and fallback.

### 5.2 PL011 / 16550 early boot console for Linux guests

- Value **medium** (contingent, see below). Effort **S**. Risk **low-M**.
- Symbols: `apple/x/vzkit/exp/serial/serial.go:14-15` (PL011, UART kinds),
  `:19` (`Available`), `:31` (`New`); only cove caller
  `cmd/cove/windows.go:246`; the virtio path Linux/macOS use today is
  `apple/x/vzkit/storage.go:23` (`CreateStdioSerialConsole`).
- Cove touchpoints: `cmd/cove/linux.go`, `linux_installer.go`, `windows.go`
  (share the constructor), `cli_help.go`.
- Gates: private `VZPL011SerialPortConfiguration` /
  `VZ16550SerialPortConfiguration`; `Available()` is a class-presence probe.
  No entitlement.

Unlike the `x/vzkit/exp/*` packages §4 excludes, this one's semantics **are**
characterized: it probes the private config classes and returns a plain
`VZSerialPortConfiguration`. Linux and macOS VMs today get a virtio-console the
guest can only use once virtio drivers are up, so EFI, early kernel, and
initramfs failures — including the documented "slow boot / black screen, use
serial console" issue — are invisible.

Corrections applied:

- **Drop macOS guests.** A VZ macOS guest boots Apple firmware with no
  PL011/16550 console concept; the port would be a config no-op. Retitle to
  Linux-only and drop `cmd/cove/macos.go` from touchpoints.
- **The work is a small refactor, not a new feature.** `windows.go` inlines
  mode parsing (`:40-64`), attachment creation, and the virtio branch;
  extracting a shared `serialMode`/`newSerialPort` helper and generalizing
  `-windows-serial` into a guest-agnostic `-serial-device virtio|pl011|16550`
  (keeping the old flag as an alias) is the bulk of the diff.
- **Value is contingent on automatic cmdline injection.** Append
  `console=ttyAMA0,115200` / `earlycon` when `pl011` is selected, in `linux.go`
  and `linux_installer.go`. Without it the user must hand-edit `-cmdline` and
  the feature is low value; make injection part of the slice.
- **`Available()` is not sufficient.** `exp/serial` retains the private config
  and hands back a `VZSerialPortConfigurationFromID`; a class-presence probe
  does not prove `validateWithError:` accepts the device. The fallback must
  trigger on validate failure, not only on `Available() == false`.

### 5.3 USB passthrough capture/release lifecycle and signature-pinned identity

- Value medium -> **low-medium**. Effort **M** (capture/release only), **M/L**
  with identity + CLI. Risk **M**.
- Symbols: `apple/x/vzkit/usbpassthrough/usbpassthrough.go:83`
  (`HostDevice.Release`), `:91` (`DeviceSignature`), `:115`
  (`NewControllerConfiguration`), `:120` (`NewController`), `:137`
  (`Controller.Capture`), `:165` (`Controller.Release`); cove uses only the
  config constructors at `cmd/cove/control_runtime_usb.go:318`.
- Cove touchpoints: `control_runtime_usb.go`, `usb.go`,
  `runtime_lifecycle.go` (release on stop), `vm_config_codec.go` (persist
  signature).
- Gates: private `VZIOUSBHostPassthrough*` / `VZXHCIController` selectors,
  probe-gated; macOS 15+ in practice. No extra entitlement.

Corrections applied:

- **Cove does call the per-device release** —
  `releaseRuntimeUSBDevice` (`control_runtime_usb.go:525-533`) invokes
  `ReleaseDevice()` — but only on the explicit detach path (`:365`). It is
  never called on VM stop, crash, or mid-attach failure, and the device is
  retained at `:326`. Restate the gap as "release-on-teardown and
  release-on-attach-failure are missing".
- **No user-facing surface exists.** Passthrough is reachable only via the
  control socket; `cove run -usb` is mass storage only. The "pin the same
  YubiKey in a VM config" story requires building that surface too — scope the
  effort estimate accordingly.
- **`Controller.Capture` blocks on a completion-handler channel**
  (`usbpassthrough.go:137-161`); calling it from the VM dispatch queue will
  deadlock. Drive it off-queue with a context timeout.
- Cove obtains a public `vz.VZUSBController` via
  `runtimeUSBControllerAtIndex`, so wiring Capture/Release requires bridging to
  `pvz.VZXHCIControllerFromID` with a class-name probe
  (`VZXHCIController`/`_VZXHCIController`) and a no-op fallback.
- Capturing a device from its host driver is disruptive (it disappears from the
  host): explicit opt-in, guaranteed release on VM stop/crash. Treat signature
  bytes as an opaque equality token; never parse.

### 5.4 Per-run energy accounting via `x/powersample`

- Value **medium**. Effort M -> **S/M**. Risk **M**.
- Symbols: `apple/x/powersample/powersample.go:18` (`Report`:
  Duration/Samples/Energy/Average), `:36` (`Start`), `:80` (`Meter.Stop`);
  `powersample/parse.go:11` (`Power`: per-rail CPU/GPU/ANE).
- Cove touchpoints: `cmd/cove/disk_bench.go`, `pit.go` (attach to run
  artifacts).
- Gates: root or `sudo -n powermetrics` (the package's error text carries the
  exact grant command). No entitlement, no private API. Numbers are Apple
  estimates — valid for comparing configurations on one machine only; never
  publish cross-machine.

Distinct from the base roadmap's battery-simulation item (`x/vzkit/exp/power`,
a *guest*-facing fake battery). This is host-side measured energy per rail.
Cove reports only wall-clock for `disk_bench`, fork/ephemeral reset benchmarks,
and the cuabench corpus; joules-per-episode discriminates between VM
configurations where wall-clock ties.

Corrections applied:

- **Drop `internal/guibench`** — the harness landed externally
  (`cove-cuabench-current`), not in this repo.
- **Drop `control_runtime_status.go`.** A status command implies an ambient
  path, and `Start()` shells `sudo -n` when not root — exactly the
  "minimize sudo prompts" collision. Keep the surface to an explicit
  `cove disk-bench`-style opt-in flag plus `pit` run artifacts.
- **Effort trimmed**: the package owns process management and parsing; the cove
  side is a Start/Stop wrapper, a `Samples == 0` guard (the documented
  silent-format-change detector — a stale parser fails loudly instead of
  reporting zeros), and a report field.
- **Missing confound**: powermetrics is whole-SoC, so cove's own host-side work
  (screenshot/OCR/Vision, VMM threads, the GUI window) is inside the
  measurement, not just other apps. Record a null-control run (meter an idle
  interval of the same length) alongside every benchmark region, or the deltas
  are not attributable.
- Parser is pinned to macOS 26.6.1 output shape and is version-brittle.

## 6. Suggested next slices

These are additive to the base roadmap's section 5 slices, not a replacement.

1. **Slice A — Automation loop latency and correctness (S/M).**
   OCR request tuning (3.2): one shared ROI converter against the post-crop
   buffer, the two vocabulary modes, opt-in Fast level, threaded through
   `ocr_search_options.go` / `control_socket_ocr.go` / `setup_assistant.go`.
   Land real scroll injection (3.1) on the AppKit view path in the same slice —
   it removes the PageUp/PageDown fake that makes scroll-dependent cuabench
   tasks unscorable. Acceptance: a before/after timing on one `ocr-wait` loop,
   and a live-verified scroll in a guest scroll view. Both are public/settled
   paths with no new gates.

2. **Slice B — Host-side survival for headless workers (S, then M/L).**
   Start with the strictly additive telemetry: thermal state + low-power mode
   in `doctor` and runtime status, stamped into `disk_bench` samples (4.6), and
   `NWPathMonitor` dial-out gating via the `try*` wrappers (4.3). Then the
   `NSActivity` sleep-inhibition-while-running half of 4.5 — small, public, no
   run-loop work. Defer the `WillSleep`/`DidWake` observer until the headless
   run-loop plumbing is scoped. Pair with the backup-exclusion half of 4.8
   (opt-in) as a same-week win.

3. **Slice C — Private VZ diagnostics, spike-first (S spikes, then M).**
   Three time-boxed spikes with explicit kill criteria, run before any CLI
   surface is designed: (a) core-dump artifact discovery by running the
   existing `TestPrivateControlAPICreateCores` against a live VM and watching
   the filesystem (2.1); (b) restricted-mode characterization on a throwaway VM
   with `ValidateRestrictedModeSupportWithError` as the entry gate (2.6); (c)
   `VZWrappingKey` / UID-key linkage against a fresh VM directory with a
   pre-probe `aux.img` copy (2.7), which either unblocks base roadmap 3.6's
   encryption or produces a §4 entry. Each spike's honest outcome may be a
   do-not-re-litigate note; that is a successful slice.

## 7. Scanned but empty, killed, or dominated

Recorded so future scans do not re-litigate these.

- **`NWEthernetChannel` L2 side channel — dominated, not merely
  entitlement-blocked.** All symbols are bound
  (`apple/network/functions.gen.go:2951` create, `:2971` create-with-parameters,
  `:3003` max-payload, `:3018` send, `:3063` receive handler, `:3108` start).
  Gated on the restricted `com.apple.developer.networking.custom-protocol`
  entitlement, which ad-hoc signing cannot grant. Even if provisioned,
  `create()` binds to a host `NWInterface`, so it only ever sees VM traffic
  under bridged networking, never VZNAT. That bridged-only footprint is already
  fully served, unentitled and bidirectionally, by BPF (`/dev/bpf*` +
  `BIOCSETIF`), and cove ships frame capture on the `-network` filehandle path
  (`cmd/cove/main.go:263 -pcap`). **No probe is warranted** — a successful
  probe would not change the recommendation. The Windows-guest justification
  ("vsock doesn't work for Windows") does not apply either: cove's Windows path
  is QEMU-backed.
- **vmnet raw-packet plane as a pcap source.** `Vmnet_read`
  (`apple/vmnet/functions.gen.go:629`), `Vmnet_write` (`:700`),
  `Vmnet_interface_set_event_callback` (`:273`) and
  `VMNET_INTERFACE_PACKETS_AVAILABLE` (`vmnet/enums.gen.go:14`) are all bound,
  but require cove to hold the `Interface_ref`, which it does not under
  `VZVmnetNetworkDeviceAttachment` — and cove already ships `-pcap`. Duplicate
  work.
- **`SoundAnalysis` + `Speech` over captured VM audio — deferred, blocked at
  step 0.** Symbols exist (`apple/soundanalysis/sn_audio_stream_analyzer.gen.go:160`,
  `:193`, `:256`; `sn_classify_sound_request.gen.go:188`, `:216`;
  `apple/speech/sf_speech_recognizer.gen.go:580`). The blocking gap is the
  `CMSampleBuffer` → `AVAudioPCMBuffer` bridge: **confirmed absent** (zero
  `CMSampleBuffer` hits in `avfaudio`). Any spike must make that bridge step 1
  and its own kill gate. Also: for live transcription the relevant request is
  `SFSpeechAudioBufferRecognitionRequest`, not the file-based
  `SFSpeechURLRecognitionRequest`; and macOS 26 introduces `SpeechAnalyzer`
  while positioning `SFSpeechRecognizer` as legacy — check whether the bindings
  track that before committing. Effort L, arguably XL once the bridge counts as
  in-scope. Recorded as examined-and-deferred, not a numbered item.
- **Private `_createSharedMemoryCoresWithOptions…`** — bound
  (`vz_virtual_machine.gen.go:275`) but its `options` parameter is
  `unsafe.Pointer` with an undocumented dictionary shape. A crash surface, not
  a feature. Use the singular `_createCore` form.
- **`Set_name` / `Set_crashContextMessage` generated property setters**
  (`vz_virtual_machine.gen.go:723`, `:601`) — send `set_name:` /
  `set_crashContextMessage:` and silently no-op. Never use; see 2.3.
- **Multi-touch device configuration** (`_setMultiTouchDevices:`,
  `vz_multi_touch_device_configuration.gen.go:112`) — split out of the scroll
  slice: boot-time config change plus reboot, uncharacterized runtime
  semantics, and the same unknown event-array packing. Not part of an M-sized
  input slice.
- **`VTMotionEstimationSession`** (`apple/videotoolbox/functions.gen.go:888`,
  `:914`) — macOS 26.0-only and dlsym-gated in the binding, so most hosts never
  exercise it. Speculative/optional, explicitly out of accepted scope for
  dirty-rect work (3.3).
- **Stale doc references encountered during this scan.** CLAUDE.md's file map
  still lists files that do not exist in `cmd/cove` (`pcap.go`,
  `network_userspace.go` were already noted in the base roadmap's §4;
  `diskguard.go` now exists but only as an untracked working-tree file).
  `internal/guibench` does not exist in this repo — the cuabench harness landed
  externally on `cove-cuabench-current`. Do not cite either as a touchpoint
  without checking first.
