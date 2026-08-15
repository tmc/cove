# Roadmap: tmc/apple API Opportunities (2026-08)

Source: six verified API-opportunity scans over `github.com/tmc/apple` bindings,
cross-checked against binding files on 2026-08-15. Claims below were spot-checked
against the actual generated sources (paths cited inline).

## 1. Executive summary

Four big themes emerged:

1. **The ParavirtualizedGraphics verdict is settled — record it and stop
   re-litigating.** The `paravirtualizedgraphics` package fully binds the
   host-side hypervisor contract (`PGNewDeviceWithDescriptor`, memory-map /
   MMIO / raise-interrupt callbacks), but those callbacks can only be serviced
   by a Hypervisor.framework VMM. Cove's VZ stack *cannot* attach a custom
   PGDevice — VZ constructs and owns its PG device internally. What IS
   reachable: the **private VZ graphics layer**
   (`private/virtualization` VZFramebuffer / VZMacGraphicsDisplay /
   VZFramebufferObserver, wrapped by `x/vzkit/framebuffer`), plus read-only
   access to the VZ-owned live `PGDisplay` via the ivar-walking technique cove
   already ships in `cmd/cove/screenshots_private_darwin.go`. Everything
   graphics-flavored on this roadmap goes through that door.

2. **Capture and recording mature via ScreenCaptureKit, not VideoToolbox.**
   SCStream (with Go-implementable delegates via `objc.RegisterClass`) plus
   `SCRecordingOutput` deliver continuous capture and hardware H.264/HEVC
   recording with zero sample-buffer handling. Verified gap: `videotoolbox`
   has *no* `VTCompressionSessionEncodeFrame` and `avfoundation`
   `AVAssetWriterInput` lacks `appendSampleBuffer:` — a hand-rolled encode/mux
   path is blocked without binding regen. SCRecordingOutput sidesteps both.

3. **Networking gets a real substrate: vmnet networks.** Named, serializable,
   cross-process vmnet networks (`Vmnet_network_create` /
   `copy_serialization` / `create_with_serialization`, verified in
   `apple/vmnet/functions.gen.go`) + `VZVmnetNetworkDeviceAttachment` (already
   working in `x/vzkit/network`) unlock isolated inter-VM fabrics, kernel NAT
   port forwarding, packet capture, and policy knobs — the missing layer for
   the design 046 fleet control plane. Gate: macOS 26+, dlsym-gated.

4. **Host-side ops modernization.** Native `OSLogStore` tailing replaces the
   `log stream` subprocess; the high-level `xpc` package (Session/Listener +
   PeerRequirement attestation) offers an authenticated control channel;
   `SecStaticCode`/`SecTask` enable code-sign verification of injected agents.
   Perception upgrades (Vision rectangles/barcodes/feature prints) slot into
   the proven synchronous `x/vzkit/ocr` pipeline.

## 2. Ranked top proposals

Deduplication notes: battery/power simulation appeared in two scans (private-VZ
and apple/x sweeps) — merged as one item using `x/vzkit/exp/power`. Host-only /
isolated inter-VM networking appeared as both private `VZHostOnlyNetworkDeviceAttachment`
and vmnet host-mode networks — merged, with vmnet named networks as the primary
path and the private attachment as a probe/fallback.

| # | Proposal | Area | Value | Effort | Risk |
|---|----------|------|-------|--------|------|
| 1 | Windowless framebuffer screenshots (headless capture) | Graphics | High | M | Private API, selector drift |
| 2 | SCStream continuous capture backend | Capture | High | M | TCC, change-driven frames |
| 3 | `cove record`: hardware H.264/HEVC via SCRecordingOutput | Capture | High | M | macOS 15+ gate |
| 4 | Named isolated inter-VM vmnet networks | Networking | High | L | macOS 26+, entitlement |
| 5 | Native OSLogStore log streaming | Ops | High | M | logging entitlement |
| 6 | Compressed/encrypted suspend states (private save options) | VZ private | High | S | Cross-OS restore compat |
| 7 | NBD remote disks (`cove run -nbd`) | Storage | High | M | Delegate wiring, test server |
| 8 | Kernel NAT port forwarding rules | Networking | High | M | vmnet-mode only |
| 9 | Linux microvm: OCI image → ext4 instant boot | Guest | High | L | Kernel artifact sourcing |
| 10 | Vision rectangle/text-rectangle screen structure | Perception | High | M | Tuning on flat UI |
| 11 | QR-code guest handshake channel | Perception | High | M | Resolution/decode limits |
| 12 | Event-driven frame capture (VZFramebufferObserver) | Graphics | High | M | 60 Hz callback discipline |

Runners-up (worth doing, smaller or later): display health metrics over the
control socket (S, lowest-risk PG proof), runtime resolution switching, MJPEG
preview endpoint, install timelapse, disk caching-mode knob, vmnet policy
knobs, XPC control channel, code-sign verification of injected agents,
usbhid.Sender migration, battery simulation, GDB stub flag, restore-image
pinning, `doctor codecs`.

## 3. Top proposal details

### 3.1 Windowless framebuffer screenshots

Direct framebuffer readback from the VZ graphics stack — no NSWindow, no
CGWindowList, so `cove screenshot`/OCR work fully headless and the
title-bar/Retina crop hazards disappear.

- Symbols: `NewMacGraphicsDisplayWithVirtualMachineGraphicsDeviceIndexFramebufferIndexUuid`
  (`apple/private/virtualization/vz_mac_graphics_display.gen.go:121`, verified);
  `VZFramebuffer.TakeScreenshotWithCompletionHandlerImageConversionBlock`
  (`vz_framebuffer.gen.go:100`); `x/vzkit/framebuffer` `Display.TakeScreenshot`;
  `iosurface` Lock/GetBaseAddress/GetBytesPerRow; `metal`
  `NewTextureWithDescriptorIosurfacePlane`.
- Cove touchpoints: `cmd/cove/screenshots.go`, `screenshots_private_darwin.go`,
  `control_socket.go`, `screen_detection.go`.
- Effort: M. Risks: private selectors can shift per macOS release (guard with
  respondsToSelector probes); image-conversion block payload needs one-time
  empirical decode on macOS 26.

### 3.2 SCStream continuous capture backend

Persistent SCStream on the VM window feeding a frame ring buffer; screenshots/
OCR become a memcpy instead of a capture round-trip. Completes design 041.

- Symbols: `screencapturekit` `NewSCStreamOutput` (Go delegate via
  objc.RegisterClass, `stream_output_protocol.gen.go:79`), `SCStream`
  Init/AddStreamOutput/StartCapture (`sc_stream.gen.go:218,266,337`),
  `SCContentFilter.InitWithDesktopIndependentWindow`, `SCStreamConfiguration`
  setters; `coremedia.CMSampleBufferGetImageBuffer`; `corevideo`
  CVPixelBuffer accessors.
- Cove touchpoints: `cmd/cove/screenshots.go`, `internal/sckit/spike_darwin.go`
  (promote spike), `control_socket_ocr.go`.
- Effort: M. Risks: frames arrive only on content change (cache last frame);
  screen-recording TCC in headless contexts (keep CGWindowList fallback);
  tight buffer copies on the ObjC dispatch thread.

### 3.3 `cove record` — hardware H.264/HEVC recording

`cove record start/stop <vm>` attaches `SCRecordingOutput` to the SCStream;
encode happens inside ScreenCaptureKit/VideoToolbox, artifacts land in
`~/.vz/runs`. Serves cuabench episode replay and fleet audit trails.

- Symbols: `SCRecordingOutputConfiguration` SetOutputURL/SetVideoCodecType/
  SetOutputFileType (`sc_recording_output_configuration.gen.go:48-55`);
  `SCRecordingOutput.InitWithConfigurationDelegate`;
  `SCStream.AddRecordingOutputError` (`sc_stream.gen.go:302`);
  `coremedia` `KCMVideoCodecType_H264/HEVC` (`enums.gen.go:1510-1513`).
- Cove touchpoints: `cmd/cove/recording_cli.go`, `control_socket_commands.go`,
  `screenshots.go` (shared stream).
- Effort: M. Risks: macOS 15+ (gate with availability check; design 041 floor
  is 14); window-tied (headless VMs can't record via this path); clean stop on
  suspend/window close.

### 3.4 Named isolated inter-VM vmnet networks

`cove network create/list/rm <name>` + `-network net:<name>`: host-mode vmnet
networks persisted via serialization, rehydrated across processes, attached
via `VZVmnetNetworkDeviceAttachment`. The L2 substrate for fleet sandboxes
and multi-VM cuabench tasks.

- Symbols: `Vmnet_network_configuration_create`/`VMNET_HOST_MODE`,
  `Vmnet_network_create`, `Vmnet_network_copy_serialization` (verified
  `apple/vmnet/functions.gen.go:536`), `Vmnet_network_create_with_serialization`,
  NAT/DHCP disable knobs; `NewVmnetNetworkDeviceAttachmentWithNetwork`
  (used in `apple/x/vzkit/network/network.go:258`).
- Cove touchpoints: `cmd/cove/networking.go`, `network_runtime.go`, `main.go`,
  new `network_named.go`.
- Effort: L. Risks: macOS 26+ dlsym gate; non-shared modes may need root or
  `com.apple.vm.networking` entitlement (verify live); serialized-network
  lifetime semantics undocumented. Fallback probe: private
  `NewVZHostOnlyNetworkDeviceAttachment`
  (`private/virtualization/vz_host_only_network_device_attachment.gen.go:74`,
  verified) for pre-26 hosts.

### 3.5 Native OSLogStore streaming

Replace `apple_logs.go`'s `exec.Command("log", "stream", ...)` with in-process
`OSLogStore` enumeration using the existing NSPredicate string — structured
entries (subsystem/category/level/pid) for JSON emission, support bundles,
and control-socket tailing.

- Symbols: `oslog.OSLogStoreClass.LocalStoreAndReturnError`,
  `PositionWithDate`, `EntriesEnumeratorWithOptionsPositionPredicateError`
  (`apple/oslog/os_log_store.gen.go`); `OSLogEntryLog` accessors
  (`os_log_entry_log.gen.go`).
- Cove touchpoints: `cmd/cove/apple_logs.go`, `control_socket.go`.
- Effort: M. Risks: local-store access needs `com.apple.logging.local-store`
  entitlement or admin — verify acceptance under ad-hoc signing; tailing is
  polled re-enumeration (~1 s granularity), not a live stream.

### 3.6 Compressed/encrypted suspend states

Use private `_saveMachineStateToURL:options:` with
`VZVirtualMachineSaveOptions{Compress, Encrypt}` for suspend/snapshot saves.
Cuts fork/ephemeral and cuabench reset storage under the design 040 budget.

- Symbols: `privatevz.NewVZVirtualMachineSaveOptions`/`SetCompress`/`SetEncrypt`
  (`private/virtualization/vz_virtual_machine_save_options.gen.go`, verified);
  `SaveMachineStateToURLOptionsCompletionHandler`
  (`vz_virtual_machine.gen.go:399-404`), which uses `objc.SendIfResponds` so
  it degrades gracefully.
- Cove touchpoints: `cmd/cove/snapshots.go`, `runtime_lifecycle.go`,
  `fork_ephemeral.go`.
- Effort: S. Risks: restore compatibility of compressed states across macOS
  versions unverified — flag-gate with fallback to public save; encryption
  needs a key-management story before default-on.

### 3.7 NBD remote disks

`cove run -nbd nbd://...`: public, fully bound attachment with timeout,
forced-read-only, sync mode, and reconnect delegate. Enables fleet lazy image
distribution and shared read-only golden disks.

- Symbols: `NewNetworkBlockDeviceStorageDeviceAttachmentWithURLTimeoutForcedReadOnlySynchronizationModeError`
  (`apple/virtualization/vz_network_block_device_storage_device_attachment.gen.go:236`),
  `ValidateURLError`, delegate protocol file, `VZDiskSynchronizationMode`.
- Cove touchpoints: `cmd/cove/block_device.go`, `system_disk.go`, `macos.go`,
  `linux.go`, `vm_config_codec.go`.
- Effort: M. Risks: delegate callbacks need cove's existing purego
  delegate-class machinery; tests need an NBD server (qemu-nbd or small Go
  server); guest disk stalls on disconnect.

### 3.8 Kernel NAT port forwarding

`cove port-forward add/rm/list <vm> tcp:8080:80` via
`Vmnet_interface_add/remove/get_port_forwarding_rule` (+ config-time variant),
so external hosts reach guest services without the in-guest agent relay.

- Symbols: rule add/remove/get + `_ip_` variants and
  `Vmnet_ip_port_forwarding_rule_get_details`
  (`apple/vmnet/functions.gen.go`); completion-handler blocks in
  `vmnet/blocks.gen.go`.
- Cove touchpoints: `cmd/cove/port_forward.go`, `internal/controlserver`,
  `networking.go`, `network_runtime.go`.
- Effort: M. Risks: interface-level rules require cove to hold the vmnet
  `Interface_ref` (vmnet-mode networks only; plain VZNAT VMs keep the vsock
  relay fallback); block-callback ABI needs a stress test.

### 3.9 Linux microvm: OCI image → instant boot

`cove run -linux -image <oci-ref>`: unpack OCI layers (`x/guest/tarfs`, with
whiteout + path-escape handling), build an ext4 disk (`x/guest/ext4.Build`),
generate an initramfs (`x/guest/initramfs.PackTree`), direct-kernel-boot via
the existing `VZLinuxBootLoader` path (`cmd/cove/linux_installer.go:642`).
Sub-second Linux sandboxes; fits disposable VMs and the fleet sandbox story.

- Effort: L. Risks: kernel/initrd acquisition (pin an ARM64 kernel artifact);
  ext4 builder maturity on large trees; keep registry access pull-only
  (privacy gate).

### 3.10 Vision structural screen detection

Replace hand-tuned brightness heuristics in `screen_detection.go` with
`VNDetectRectanglesRequest` + `VNDetectTextRectanglesRequest` (+ contours),
exposed as `x/vzkit/ocr`-style helpers — same synchronous
`VNImageRequestHandler.PerformRequestsError` pipeline already proven for OCR.

- Symbols: `vision/vn_detect_rectangles_request.gen.go`,
  `vn_rectangle_observation.gen.go` (TopLeft/BottomRight),
  `vn_detect_text_rectangles_request.gen.go`, `vn_detect_contours_request.gen.go`,
  `vn_image_request_handler.gen.go`.
- Cove touchpoints: `screen_detection.go`, `screen_detection_ocr.go`,
  `control_socket_ocr.go`, `apple/x/vzkit/ocr/ocr.go`.
- Effort: M. Risks: low-contrast macOS dialogs may need tuned thresholds;
  keep pixel heuristics as fallback, A/B on the screenshot corpus.

### 3.11 QR-code guest handshake

Guest renders a QR encoding structured state (agent port, boot phase, error
codes); host scans screenshots with `VNDetectBarcodesRequest` /
`VNBarcodeObservation.PayloadStringValue`. A machine-readable status channel
that works before vsock/agent is up, plus cuabench completion beacons.

- Symbols: `vision/vn_detect_barcodes_request.gen.go:116,147,170`,
  `vn_barcode_observation.gen.go:123`.
- Cove touchpoints: `control_socket_ocr.go`, `unattended.go`, `vzscript.go`,
  `agent_state.go`.
- Effort: M. Risks: QR must be large enough at VM resolution/Retina scale;
  pre-login rendering surfaces are limited.

### 3.12 Event-driven frame capture (VZFramebufferObserver)

Register a `VZFramebufferObserver` for per-frame callbacks — sub-frame-latency
"screen changed" signals for `ocr-wait`/`detect-screen`, cheap recording of
benchmark runs, no more fixed-interval polling.

- Symbols: `private/virtualization/vz_framebuffer_observer_protocol.gen.go:49-58`
  (FramebufferDidUpdateFrame/Cursor/ColorSpace); in-package
  `delegate_class_counter.gen.go` proves the purego delegate pattern;
  fallback dirty-counter: `PGDisplayObject.GuestPresentCount/HostPresentCount`
  (`pg_display_protocol.gen.go:202,217`).
- Cove touchpoints: `control_socket_ocr.go`, `screen_detection.go`,
  `vzscript.go`, cuabench harness.
- Effort: M. Risks: 60 Hz callbacks on the display queue — handler must be
  allocation-free and hop off the queue fast; private frame-arg shape needs
  one-time empirical decode.

## 4. Not now / infeasible (do not re-litigate)

- **Custom PGDevice on the VZ stack — architecturally impossible.** All
  symbols are bound and callable (`PGNewDeviceWithDescriptor`,
  descriptor map-memory/MMIO/raise-interrupt setters, block wrappers), but the
  descriptor contract requires cove to be the VMM servicing guest-physical
  memory — Hypervisor.framework territory. VZ owns guest memory and its own
  internal PG device with no external attachment point. At most a time-boxed
  design note for a hypothetical hv backend (XL, low value); the private-VZ
  proposals above deliver ~80% of the capability.
- **Hypervisor.framework as a feature substrate.** One VM per process and VZ
  owns it; a VZ-based cove process cannot also own an `hv_vm`. Only
  non-VM-owning queries are safely reachable. No proposals depend on it.
- **Hand-rolled VideoToolbox encode loop.** Verified gap:
  `videotoolbox/functions.gen.go` has `VTCompressionSessionCreate` but no
  `VTCompressionSessionEncodeFrame(WithOutputHandler)` (grep: 0 hits).
  Blocked without binding regen. Use SCRecordingOutput.
- **AVAssetWriter mux path.** `AVAssetWriterInput` lacks generated
  `appendSampleBuffer:`/`requestMediaDataWhenReady` — needs one-off objc.Send
  or regen. Also why HLS preview was rejected in favor of MJPEG.
- **Guest audio capture from the VZ audio device.** Neither public nor private
  virtualization bindings expose any output sink besides
  `VZHostAudioOutputStreamSink` — no file/tap sink. Guest-audio capture must
  go through ScreenCaptureKit `SetCapturesAudio`.
- **Host `cove ctl speak` into the guest mic.** `AVSpeechSynthesizer.SpeakUtterance`
  plays to the host *output* device; the VZ bindings offer no input-source
  override beyond the host default input. Requires a loopback audio device
  (BlackHole-style) or degrades to in-guest `say` via the agent (already
  possible). Ship only with a documented loopback prerequisite.
- **SMAppService helper for dev builds.** Ad-hoc-signed, frequently re-signed
  binaries get repeatedly invalidated; prior memory already judged it overkill
  for one-shot file ops. Scope only to a packaged/released cove.app; keep
  AuthorizationCopyRights + `sudo -n` for dev.
- **XPC same-team peer requirements.** Meaningless for ad-hoc-signed builds
  (no team ID). Use entitlement-exists / lightweight-code-requirement forms.
- **`x/vzkit/exp/{accelerator,biometric,mailbox,custommmio,customvirtio}`**
  (SEP, Bifrost, VideoToolbox device, mailbox): symbols exist but runtime
  semantics explicitly uncharacterized — not product features.
- **Stale CLAUDE.md file map.** `cmd/cove` has no `pcap.go` or
  `network_userspace.go` despite the file listing; capture features are
  greenfield files.

## 5. Suggested first three slices

1. **Slice 1 — PGDisplay reachability proof + display metrics (S).**
   Extend the `screenshots_private_darwin.go` ivar walk to locate the live
   `PGDisplay`; expose GuestPresentCount/HostPresentCount/mode/cursor as a
   `display` status command (`control_runtime_status.go`). Read-only,
   lowest-risk, and de-risks proposals 3.1, 3.12, and resolution switching in
   one step. Pair with the disk caching-mode knob (also S, public API) as a
   same-week win.
2. **Slice 2 — Windowless framebuffer screenshots (3.1) behind a flag.**
   `VZMacGraphicsDisplay` + `TakeScreenshot` via `x/vzkit/framebuffer`,
   selected as a screenshot backend with the existing capture path as
   fallback. Unlocks headless OCR/automation, the biggest single capability
   gap. Follow immediately with 3.12 (observer) since it shares the plumbing.
3. **Slice 3 — Compressed suspend states (3.6) + SCStream backend start (3.2).**
   3.6 is a small, gated, high-payoff change to `snapshots.go`/
   `fork_ephemeral.go` (verify restore compat empirically first). In parallel,
   promote `internal/sckit` spike to a stream package feeding the screenshot
   ring — the prerequisite for `cove record` (3.3) and MJPEG preview.
