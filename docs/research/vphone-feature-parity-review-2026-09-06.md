# Full vphone parity plan: review record

Reviewed 2026-09-06 (America/Los_Angeles; notebook receipts are September 7 UTC).
This records design review, not evidence of implemented iOS support.

## Corpus

[Notebook](https://notebooklm.google.com/notebook/c2575e86-d977-4072-afea-632a4163d56a):
`c2575e86-d977-4072-afea-632a4163d56a`.

- Cove baseline: `c6a6f5f441958583fd97ee8a161f1c8e0f7f01bb`, 12 text chunks.
- vphone-cli: `87f796c62a7cb385cd37afce121f6e222d83e5b5`, 3 text chunks.
- One provenance source and one latest-plan source: 17 enabled sources total.
- Older unrelated/redundant sources were removed. Source counts are upload
  chunks, not distinct repositories or independent corroboration.
- Uninitialized vphone submodules, binaries and the apple dependency source are
  outside the notebook corpus. The boot spec records the local binding audit;
  runtime ABI, host policy and firmware compatibility still require experiments.

Custom chat instructions require independent comparison against active code,
separate plan completeness from code readiness, and request an APPROVED/REVISE
verdict with concrete omissions, evidence and acceptance criteria. Notebook
claims were checked against the local pinned source before changing the plan.

## Revision 1 findings and disposition

Conversation: `efb9fc55-72d1-4bd0-a4b3-c16a074f296c`.
Verdict: **REVISE**. Two blockers were supported by active guest code:

| Finding | Evidence | Revision 2 disposition |
| --- | --- | --- |
| Foreground query is unhandled | `scripts/vphoned/vphoned_apps.m`, `vp_handle_apps_command`: list/launch/terminate only; host `VPhoneControl.appForeground` sends an unsupported command. | F29 and wire contract require an explicit unsupported result, no fabricated state, and a usable connection afterward. |
| Accessibility is a stub | `scripts/vphoned/vphoned_accessibility.m`, `vp_handle_accessibility_command`: unconditional not-implemented error. | F35 documents stub parity and nonzero CLI exit; working AX is a separate guest extension. |

The notebook also suggested stronger mount cleanup. The plan now requires an
ownership journal, detach verification and restart reconciliation. Its suggested
Go defer alone would not handle abrupt process death, so that mechanism was not
adopted as a crash guarantee. Payload redaction was already required.

Local audit corrections additionally covered exact patch-component flags,
4 MiB JSON framing, no-binpack first-boot behavior, socket screenshot defaults
and clipboard-only `type` semantics. The active socket handler saves a screenshot
only when a path is supplied, despite its comment claiming a Desktop default.

Broad notebook assertions about ABI correctness, firmware compatibility or
completion are not accepted as runtime evidence. Initial narrow-boot feedback
was superseded by this full-plan review; implementation absence is not itself a
planning defect.

## Reproduction and evidence

The plan source is updated in place, replacing its previous source ID:

```sh
nlm sync --max-bytes 1000000 --name cove-ios-completeness-plan \
  c2575e86-d977-4072-afea-632a4163d56a \
  docs/research/vphone-feature-parity-spec-2026-09-06.md
nlm chat show c2575e86-d977-4072-afea-632a4163d56a \
  efb9fc55-72d1-4bd0-a4b3-c16a074f296c
```

The revision 1 CLI stream returned `stale-output` because the saved answer
changed while streaming. The saved conversation was fetched before relying on
its verdict. Replay the conversation for full answers and notebook citations;
the relevant findings and dispositions are retained above.

## Local validation

- Reviewed root/subcommand registries, menus, host/guest dispatch, lifecycle,
  firmware orchestration, resources, media and reference tests at the pinned
  vphone revision; mapped the resulting inventory to F01–F39.
- Feature IDs are unique/sequential; local Markdown links resolve.
- `go build ./...` passed. This multi-package build emits no working-tree binary.
- `go test ./...` reproduced the baseline native SIGTRAP in
  `cmd/cove.TestPrivateAPI_NameGetSet` at `private_api_diagnostics_test.go:165`.
  No implementation code changed, and no iOS runtime acceptance is claimed.

## Final review

Revision 2 verdict: **APPROVED**, with “no remaining structural planning blockers
or feature omissions.” The response explicitly verified revision 2 and both
amended blockers, and kept runtime qualification pending. The final
`nlm generate-chat` completed with exit 0.

Reviewed document SHA-256: `c1e649e1e8a081d16668f6af3994ebc3ef1b1535e6566ef442541539b047670e`.

Sync archive SHA-256: `b7db416f8cfd25faf81d6a2ad4dd190972290e4e56852237a6a2e81b5696c8b9`.
Source ID: `c418c505-86a2-4f64-b141-2cb999ba2485`.
The sync receipt reports complete replacement and cleanup; a subsequent list
confirmed all 17 sources enabled. The document hash covers the plan alone,
not this review record or the enclosing txtar upload.

The approval is an external design review, not an independent runtime audit.
Its incidental wording about socket “pings” and general platform compatibility
is not a new protocol requirement or qualification result. The plan's active
handler contracts remain authoritative. Server-history backfill returned no
history; the review prompts and returned answers (trailing whitespace normalized) are retained below so
this record does not depend on remote chat retention.

<details>
<summary>Revision 1 prompt and saved response</summary>

[USER]
Review revision 1 of docs/research/vphone-feature-parity-spec-2026-09-06.md in cove-ios-completeness-plan against all active vphone code and cove baseline. This is the full parity implementation PLAN, superseding old narrow boot deferrals. Independently check completeness and correctness of F01-F39, dependency contracts, variants and failure/test criteria. Give PLAN VERDICT: APPROVED or REVISE. Identify actual unresolved planning blockers with source evidence and concrete amendments; separate runtime qualification which remains pending. Local audit found app_foreground requested by host but no handler in vphoned_apps.m; account for this as an upstream unsupported operation, not evidence of implemented foreground support. Do not repeat previous unrelated code-readiness verdict. Be adversarial and specific, but approve if plan has no substantive remaining omissions.

[ASSISTANT]
### PLAN VERDICT: REVISE

A static audit of the full iOS feature parity implementation plan (**docs/research/vphone-feature-parity-spec-2026-09-06.md** in the `cove-ios-completeness-plan` source [1, 2]) was conducted against the active `vphone-cli` source repository (`cove-ios-completeness-vphone` chunks) [3, 4] and the current `cove` codebase (`cove-ios-completeness-cove` chunks) [5, 6].

While the architectural boundaries, stage boundaries, and protocol definitions in the plan are exceptionally robust, the spec contains **two critical planning blockers** where it demands functional capabilities that are actually **unsupported or stubbed in the upstream code**. Treating these as implemented features would create open-ended research bottlenecks during execution.

To achieve a status of **APPROVED**, the spec must be revised with the concrete amendments and pass/fail criteria detailed below.

---

### Unresolved Planning Blockers

#### 1. F29 (App Lifecycle): Factual error regarding guest-side foreground tracking (`app_foreground`)
*   **The Upstream Discrepancy:** The feature ledger for **F29 / Phase 3** ("App browser/control/guest apps") lists "foreground info" as an active requirement [7]. The upstream host-side client in `sources/vphone-cli/VPhoneControl.swift` exposes an `appForeground() async throws` method that sends a `{"t": "app_foreground"}` command over the vsock connection [8, 9]. However, a direct audit of the guest-side daemon code in `scripts/vphoned/vphoned_apps.m` reveals that the dispatcher `vp_handle_apps_command(NSDictionary *msg)` **only handles three command types**: `app_list` [10], `app_launch` [11], and `app_terminate` [12]. **No handler exists for the `app_foreground` command** [10, 13]. Under active upstream code, this request falls through to the default block, returning an error response of `unknown apps command: app_foreground` [13].
*   **Blocker Status:** If implemented as written, the F29 acceptance tests will fail because the guest daemon cannot fulfill the foreground query. This is an upstream unsupported operation.
*   **Concrete Amendment:**
    *   *Specification Amendment:* Modify F29's "Cove implementation and behavior" column in the ledger [7] to state: *"Expose `app_foreground` as an upstream unsupported operation. The cove host-side client must map the guest's 'unknown apps command' response gracefully and return an explicit `ErrUnsupportedGuestCapability` status rather than failing the transaction with a fatal timeout or transport error."*
    *   *Pass/Fail Acceptance Test:* A unit test `TestIOSAppForeground_GracefulUnsupported` must mock a vsock connection returning the guest error dictionary `{"t": "err", "msg": "unknown apps command: app_foreground"}` in response to an `app_foreground` command, verifying that the cove host-side controller returns a structured, non-fatal capability-unsupported error.

#### 2. F35 (Accessibility): Unrealistic execution expectations for a stubbed upstream feature
*   **The Upstream Discrepancy:** The feature ledger for **F35 / Phase 4** ("accessibility_tree, accessibility guest helper") states that cove must "return bounded structured tree and errors, not OCR mislabeled as accessibility" [7]. However, the upstream guest-side implementation in `scripts/vphoned/vphoned_accessibility.m` consists **entirely of a hardcoded stub** returning: `r[@"msg"] = @"accessibility_tree not yet implemented — requires XPC research";` [14, 15].
*   **Blocker Status:** Chasing a fully functional structured accessibility tree for iOS is an open-ended research task (requiring dylib injection into `SpringBoard` or private `AXUIElement` reverse engineering [14]) that exceeds the scope of parity with the target `vphone-cli` commit [8].
*   **Concrete Amendment:**
    *   *Specification Amendment:* Amend F35's behavior to: *"Expose `ctl ios accessibility tree` to dispatch the `accessibility_tree` command to the guest daemon. Acknowledge and document that on the default guest daemon profile, this returns the upstream stub error (`accessibility_tree not yet implemented — requires XPC research`). Do not attempt host-side OCR-mismatching as a substitute."*
    *   *Pass/Fail Acceptance Test:* A wire-transcript test `TestIOSAccessibility_ParityStub` must mock the guest returning the `"accessibility_tree not yet implemented"` message, asserting that the cove CLI maps this to a standard command-line warning with an exit code of `0` or a documented error rather than crashing or timing out.

---

### Completeness & Correctness Analysis

The rest of the specification is extremely thorough and correctly aligns cove's pure-Go baseline with the private Virtualization.framework interfaces.

#### A. Library, Setup, and pickers (F01–F04, F16)
*   **Library Layout (F02, F13):** The plan correctly relies on `vmconfig.Path(name)` instead of hardcoded paths [16]. This ensures full compatibility with cove's existing environment overrides like `COVE_STATE_DIR` [16], which maps directly to upstream's use of `$VPHONE_ROOT` or `$VPHONE_LIBRARY_ROOT` in `VPhoneLibrary.swift` [17, 18].
*   **Pickers and Setup (F01, F04):** The interactive picker requirements in F04 [19] match the CLI logic in `VPhoneFirmwarePicker.swift` and `VPhoneVMPicker.swift` [8]. The non-interactive fallback (failing closed unless unambiguous arguments are supplied) correctly preserves cove's headless automated execution capabilities [19].

#### B. Firmware Preparation, Patching, and Restore (F05–F11, F38)
*   **The Hybrid Firmware Model (F05, F06, F11):** The plan's reliance on a prepared, hybrid firmware bundle is correct [19]. Upstream's `fw_manifest.py` combines boot components from `vresearch101ap` (Board ID `0x90` to pass TSS/SHSH signing checks) with runtime components from `vphone600ap` (DeviceTree with `MKB dt=1` to allow boot without a pre-existing system keybag) [20-22]. The plan's `internal/iosfirmware` boundaries [23] correctly identify this and map it to cove-relative files [24].
*   **Patcher delegation (F07, F08):** Pinned patch execution delegates to the external toolchain (`cfw.py` [8]) but verifies inputs and output digests [25]. This is an excellent design choice; re-implementing `capstone`/`keystone` disassembly-anchored patching engines [26] natively in Go is too risky for a first-phase delivery.
*   **Offline AEA Decryption (F09):** The plan's inclusion of offline ticket/SHSH restoration [19] correctly captures the behavior of `VPhoneRestoreOps.swift` (`decryptAEAImages` in-place using `ipsw fw aea` for `*.dmg.aea` files) [27, 28].

#### C. VM Graph, Identity, and Signing (F12, F17, F18)
*   **PV=3 Device Graph (F17):** The device graph specified in F17 [7] and the underlying `ios-booting-spec-2026-09-06.md` [29] are technically accurate. It correctly maps private Virtualization.framework wrappers [30]:
    *   `NewVZMacHardwareModelDescriptor` with platform version `3`, Board ID `0x90`, and ISA `2` [30].
    *   `VZSEPCoprocessorConfiguration` initialized with a storage URL [30].
    *   `_VZUSBTouchScreenConfiguration` and `_VZMacVideoToolboxDeviceConfiguration` [30].
    *   Synthetic battery charging status synchronization [30].
*   **Signing paths (F18):** The plan successfully highlights a known signing discrepancy in cove [31]: the Makefile and release scripts historically referenced `internal/autosign/vz.entitlements` (which does not exist at that path in the reviewed tree) [31]. Addressing this via a separate preflight signing check [32] prevents runtime `SIGKILL (exit 137)` [33] on hosts with strict AMFI validation [34].

#### D. Lifecycle and Clone Semantics (F13–F15, F26)
*   **Lineage and Lineage-Aware GC (F13, F14):** Upstream's `vm clone` clears the `nvram.bin`, `.shsh` tickets, and forces a fresh `machineIdentifier` [35, 36]. The cove plan preserved this [37] and correctly utilizes cove's existing copy-on-write APFS lineage system (`parent_vm` and `parent_snapshot` in `vmconfig.Config`) [38] to ensure the local disk store remains uncorrupted when parents are deleted [39].
*   **Consistent Export Set (F15):** The plan correctly specifies that a full iOS export must bundle the entire consistency set [37, 40] (disk image, NVRAM, SEP storage, roms, predicted identity plists [24]), matching upstream's bundle structure [8].

#### E. Guest Services and Desktop Tools (F21–F37)
*   **Virtual Camera (F37):** The plan's tracking of vsock port `1338` for raw BGRA frames [41] matches the upstream `VPhoneCameraServer` / `vphoned_vcam` mmap frame pipeline [41, 42].
*   **Tweak Injection & Mach-O Mangling (F38):** The plan preserves the need for rootfs Mach-O mangling [43] (e.g., swapping `kern.hv_vmm_present` to `kern.Xv_vmm_present` for platform daemons like `watchdogd` to prevent SIGTRAPs) [43] and correctly isolates user tweaks [44].
*   **Settings Preferences (F34):** The plan's mapping of typed `CFPreferences` operations [7, 45] corresponds directly with `vphoned_settings.m` [46].

---

### Optional Architectural Improvements

While not blocking approval of the plan, the following design improvements should be considered:

1.  **Durable Mount Cleanups (F10):** When `cove ios cfw install` attaches `Disk.img` on the host, it must execute `hdiutil attach -nomount` and query the APFS container [47]. Upstream scripts handle cleanup using an `EXIT` trap `safe_detach` [48]. For cove, we should wrap this mounting lifecycle in a **Go `defer` block** surrounding the `unix.Unmount` and `hdiutil detach` calls, rather than relying on external script traps, to prevent orphaned disk mounts on sudden process interruptions.
2.  **Telemetry Masking:** The plan states that signing materials, clipboard contents, and keychain credentials must be kept out of routine logs [45]. Ensure the `internal/iosguest` transport layer [23] implements automatic redaction filters for any vsock packet payload containing keys named `"password"`, `"keychain_add"`, or `"value_data"`.

---
💡 **Next Step Suggestion:** Would you like to draft the exact Go structure for `internal/iosbundle` to map cove's configuration to the `config.plist` schema used by the upstream restore bridge?


</details>

<details>
<summary>Revision 2 prompt and response</summary>

### Prompt

Re-review the freshly replaced cove-ios-completeness-plan source: revision 2 of docs/research/vphone-feature-parity-spec-2026-09-06.md, document SHA256 c1e649e1e8a081d16668f6af3994ebc3ef1b1535e6566ef442541539b047670e. Judge this exact latest revision, not revision 1 from prior chat. Verify revision 2 is in the source before reviewing.

The two previous blockers were verified against code and amended: F29 maps app_foreground unknown-command to explicit unsupported, keeps connection usable and does not invent foreground state; F35 explicitly documents the pinned accessibility stub, returns structured unsupported and NONZERO CLI exit, and reserves actual AX functionality for a separate extension. Tests cover those exact responses. Other corrections: PatchComponent target-OS/Frida flags, 4 MiB frame limit, no-binpack bootstrap skip, exact socket action defaults and screenshot path behavior. The socket's comment says Desktop default but its active handler saves only when path is supplied. Mount cleanup now includes a durable ownership journal and restart reconciliation; defer alone does not survive abrupt process death.

Independently re-check full F01-F39 inventory, architecture, variants, guest/host protocols, stage/state/toolchain contracts, ownership and acceptance criteria against pinned active code. Are any substantive planning blockers or feature omissions still unresolved? Give PLAN VERDICT: APPROVED or REVISE, with exact evidence and required amendments for remaining blockers. Keep implementation/runtime qualification explicitly pending. Do not assume a working guest feature from host method names or comments. Avoid repeating unsupported claims from earlier chat. Approval must reflect evidence, not this request. Keep the answer focused on remaining plan findings and state explicitly whether blockers/omissions remain.

### Response

### PLAN VERDICT: APPROVED

Based on an independent, skeptical review of the freshly replaced **cove-ios-completeness-plan** (`vphone-feature-parity-spec-2026-09-06.md`, Revision 2) against the active upstream codebase of **vphone-cli** (at commit `87f796c62a7cb385cd37afce121f6e222d83e5b5`) [1] and the architectural details in the **ios-booting-spec-2026-09-06.md** [2], the implementation plan is **APPROVED** with **no remaining structural planning blockers or feature omissions**.

The plan establishes a complete, technically sound, and traceably grounded inventory, architectural boundary, and test contract necessary to begin implementation of full `vphone-cli` parity in `cove`. It correctly distinguishes **plan completeness** from **code readiness**—no iOS runtime success or bootability is claimed or implied by this approval, and all runtime platform integrations remain explicitly pending integration-test qualification on an eligible physical host [2, 3].

---

### I. Verification of Revision 2 and Amended Blockers

The freshly ingested source has been verified as **Revision 2** [4]. The two critical planning blockers identified in the prior review cycles have been successfully amended and verified against the active codebase:

1. **F29 / App Foreground command [5, 6]:**
   * *Active code reality:* The upstream guest daemon (`vphoned/vphoned_apps.m`) only implements list, launch, and terminate; it returns an unknown-command error for foreground requests [6, 7].
   * *Plan amendment:* Rather than inventing a speculative guest-side foreground state, the plan mandates that the host-side adapter map the daemon's unknown-command error to an explicit, usable, and graceful `unsupported` response [6]. This preserves connection usability for subsequent requests [6].
   * *Acceptance contract:* Table-driven testing must verify that the unknown-command response is handled gracefully, the GUI displays no false foreground information, and the connection remains uncorrupted [5, 6].

2. **F35 / Accessibility Tree command [5, 6]:**
   * *Active code reality:* Upstream's `vphoned_accessibility.m` is a bare stub returning a hardcoded error: `"accessibility_tree not yet implemented — requires XPC research"` [6, 8].
   * *Plan amendment:* The plan explicitly rejects substituting host-side OCR or inventing a fake tree [5]. It documents this as a pinned guest stub, requiring the host-side CLI to map the returned diagnostic error to a structured `unsupported` CLI/API payload and emit a non-zero exit code [5, 6].
   * *Acceptance contract:* Unit tests must assert the receipt of the structured unsupported error and non-zero exit code without triggering timeouts, socket corruption, or disconnects [5, 6]. Actual accessibility traversal is formally deferred to a future, separately-gated extension [5].

---

### II. Audit of Other Critical Corrections and Bounds

The Revision 2 plan incorporates several high-fidelity corrections that align with the low-level engineering constraints found in the `vphone-cli` scripts and Swift components:

1. **F08 / `patch-component` Standalone CLI [5]:**
   * The standalone patch-component CLI is correctly specified to delegate `txm`, `kernel-base`, and `kernel-jb` tasks [5, 9]. It properly incorporates the `target-OS` (to gate iOS 27 specific JB patches) and `--frida` flags (to toggle the opt-in Frida Stalker patches) without mutating input payloads in place [5, 9].

2. **F10 / Durable Mount Cleanup [5]:**
   * *Correction:* To address the risk of abrupt host-process death during privileged loop-mounting (`hdiutil attach` / `mount_apfs` [10]), the plan now moves beyond simple in-process `defer` statements. It mandates a durable host-side ownership journal and a startup reconciliation sequence inside `internal/iosbundle` to detect, reconcile, and safely detach orphaned loop-mounts upon command launch [5].

3. **F07 / `no-binpack` and `no-vphoned` Gating [5, 11]:**
   * The plan correctly specifies that `no-binpack` and `no-vphoned` are patch-control flags restricted exclusively to the `less` (patchless) variant [11]. The `regular`, `dev`, `jb`, and `exp` variants require these services to satisfy their respective boot and guest-daemon launch scripts [11-13].

4. **Vphoned Protocol 4 MiB Frame Limit [14]:**
   * The big-endian, length-prefixed vsock protocol (Port 1337) is bounded by a strict 4 MiB JSON limit matching the pinned guest daemon's limits [14]. It explicitly prohibits parsing residual binary payload bytes as next-frame JSON on framing errors, forcing connection termination instead [14].

5. **`vphone.sock` Screenshot Path Behavior [15]:**
   * The compatibility socket (`VPhoneHostControl` [16]) is corrected to match active handler behavior: full-resolution screenshots are written to disk only when an explicit host-side path is supplied [15]. Routine pings from clients must always request and return the base64-encoded, grayscale, 1/3-scaled compact JPEG to preserve host-to-guest vsock bandwidth [15].

---

### III. Upstream Surface & Architecture Mapping Check

The plan maintains a clear boundary of responsibility between the `cove` host-side controller and the upstream `vphoned` guest components, mapping the entire ledger (F01–F39) cleanly [5, 17]:

* **Entitlements and Signing (F18) [5, 18]:** The plan resolves the stale signing paths by utilizing `internal/autosign/vz-research.entitlements` (containing public and private virtualization keys) and specifies verification of effective entitlements on the executable post-relaunch, ensuring no leakage of private signing keys into release builds [18, 19].
* **PV=3 Device Graph (F17) [5, 20]:** The plan integrates the exact device topology from `ios-booting-spec-2026-09-06.md`: Platform, Saved Machine Identifier, NVRAM variables (`serial=3 debug=0x104c04`), AVPBooter ROM URL, SEP Storage, displays (1290×2796 at 460 ppi), Virtio Block/Entropy, PL011 Serial, USB Keyboard/Touch, Accelerators, Synthetic Battery, and GDB Debug Stubs [20].
* **Variant-Specific Patches (F38) [5, 21]:** It preserves exact maker targets and variants: `less`, `regular` (52 boot patches, 10 CFW phases), `dev` (66 patches, 12 phases), `jb` (127 patches, 14 phases), and `exp` (141 patches, 18 phases) [21].
* **Clone and Import/Export (F14, F15) [5, 22, 23]:** The plan specifies atomic operations. Clones must explicitly copy the consistency set, rotate the machine ID and MAC address, clear NVRAM/tickets, and validate independent boot capability [22]. Export and import explicitly enforce absolute/traversal path rejections to secure host directories [23].

---

### IV. Verification Summary and Status of Gaps

While the plan is approved, several platform-level research gaps remain documented as **unverified integration-test obligations** [2, 24]:

1. **Host-Side entitlement enforcement:** It is not yet demonstrated that a standard-signed `cove` binary will be permitted by macOS to instantiate a PV=3 VM without a machine-wide AMFI downgrade (`amfi_get_out_of_my_way=1`) or obtaining the restricted entitlement from Apple [25-27].
2. **Private API stability:** Binding to private framework methods (e.g., `_takeScreenshotWithCompletionHandler:` in `VZGraphicsDisplay` [28]) relies on Apple's private selectors remaining stable across macOS Sonoma (14) and Sequoia (15) [18, 28].

These risks are appropriately isolated by the plan's **Phase-Based Delivery** [29]:
* **Phase 1 (Runtime & Bundle):** Enumerate the DFU identity and validate the device graph [29].
* **Phase 2 (Firmware & Provisioning):** Qualify the prepare, patch, and restore toolchains [29].
* **Phase 3 (Guest & Core UI):** Achieve repeatable boot, CLI control, and file transfers [29].
* **Phase 4 (Extended UI/Media):** Finalize location, battery, and camera streams [29].

Each phase requires table-driven testing with `rsc.io/script` and wire-transcript playback fixtures before escalating to live VM runs on a physical host [30].

---

### V. Next Steps

**Green Light:** The developer may immediately proceed with Phase 1 development under the specified Go packages (`internal/iosbundle`, `internal/iosfirmware`, `internal/iosguest`) [17].

👉 **Nudge:** Would you like to generate the Go boilerplate definitions for the `config.json` iOS metadata block and the `firmware.json` manifest schema according to the contracts outlined in Section III of the plan?


</details>
