# vphone parity implementation status

Status: incomplete. The approved plan remains the full scope. This first
increment is configuration and planning infrastructure, not a bootable runtime.

Baseline: `a367dca86e9ffb070dc0e898e1c2bd1f9e1f5fcd`.

## Implemented in increment 1

- `internal/ios/bundle.Config`: versioned metadata, pinned research display
  defaults, all five variant names, network/MAC and digest validation, ROM
  references, boot arguments, no-binpack and runtime no-vphoned settings.
- `internal/vmconfig`: preserve the iOS object during hardware edits; reject
  invalid metadata on load/save; detect valid iOS metadata before `hw.model`.
  Malformed configuration is reported as unknown rather than guessed macOS.
- `internal/vmrun`: `GuestIOS`, full-duplex audio planning and rejection of
  incompatible generic install/Linux/virtio guest-service options.
- macOS runtime rejects iOS configuration before state creation or mutation.
  This guard is temporary containment pending the actual iOS runtime dispatcher;
  it is not counted as runtime parity.

Tests cover metadata round trips, preserving metadata through hardware edits,
invalid-save preservation, marker precedence, malformed/unknown schemas,
conflicting boot options and macOS runner rejection before guest-state mutation.
The focused package and runner tests pass. `go build ./...` passes.
The full suite's native `TestPrivateAPI_NameGetSet` failure is tracked separately.

## Increment 2: signing and architectural boundary

Cove owns the runtime adapter; firmware processing and guest binaries retain a
separately versioned toolchain boundary. Plan revision 4 records this boundary
without removing any feature from the ledger.

- Public build/release/DMG/integration-test signing paths now use the actual
  `cmd/cove/vz.entitlements` file.
- `make build` signs its output. `make build RESEARCH=1 BINARY=cove-research`
  selects `cove_research` and the research plist.
- Autosign and optional macgo bundle configuration share the selected profile's
  required keys. Research uses a separate relaunch guard and both private keys.
- The old `internal/autosign/vz-research.entitlements` was removed and replaced
  by `internal/ios/research.entitlements`; treat the old baseline path as deleted
  when reviewing the notebook's implementation overlay.
- Public and research builds passed; both actual signatures verified and their
  entitlement dictionaries matched the selected profiles. The public profile
  test passed. Research test launch after autosigning was killed by the host.
  This is a qualification failure, not proof of a specific policy cause. Pure
  XML/profile validation can run with autosigning suppressed; that does not
  establish permission to execute a research-signed VM process.

F18 is partial: explicit signing profiles are implemented, but runtime policy
preflight, successful signed relaunch/Finder execution and PV=3 qualification
remain open. No host security policy was changed.

## Source layout

The iOS implementation is rooted at `internal/ios`. Bundle metadata moved from
`internal/iosbundle` to `internal/ios/bundle`; the old path is deleted in the
implementation overlay. Research signing assets live at the parent, with a
small build-tag adapter in `cmd/cove`. Future guest, firmware, control, media and
host packages belong below this tree; no empty placeholder packages are created.
Shared guest selection/configuration hooks remain in existing cove packages.

## Increment 3: descriptor and executable preflight

`cove ios preflight` now calls `internal/ios.InspectHost` and emits JSON for
architecture, signature validity, missing/inactive required entitlements and descriptor
ABI. It creates no VM or hardware model; a successful static preflight is not
runtime qualification. A bounded context limits codesign subprocesses.

The internal hardware-model constructor checks selector availability and exact
return/argument encodings before using the pinned PV=3, board 0x90, ISA 2
setters. It validates nil results and host model support, and returns an owned
reference. It is not yet connected to a runtime builder or exercised on an
eligible research host.

Observed on this host with the signed public binary: architecture arm64,
signatureValid true, descriptorABI true, and both private entitlements missing;
CLI exit 1. Tests cover method-signature mismatches and entitlement parsing,
including false/string/nested values, duplicate keys, missing values and malformed
XML. No descriptor probe is counted as DFU evidence. The full graph, identity
persistence and runtime dispatcher remain required.

### Notebook review disposition

Increment 3 review (`f0348931-ffad-4e09-be7b-7f65354876f6`) remained INCOMPLETE.
Accepted: tolerate XML comments/processing instructions and distinguish missing
from present-but-inactive entitlements. Both have regression tests. Non-boolean
values intentionally do not grant the currently required boolean capabilities.

Rejected: the notebook inferred NSNumber arguments from upstream Swift Dynamic
syntax. Actual host method encodings read through the Objective-C runtime were
`setPlatformVersion: v20@0:8I16`, `setBoardID: v20@0:8I16`, and
`setISA: v24@0:8q16`. These match the pinned typed bindings. A differing host ABI
will fail preflight; no speculative NSNumber fallback was added. The notebook's
suggestion to use gRPC for vphoned also contradicts the pinned wire protocol.

## Increment 4: blank bundle creation

`cove ios new [-cpu N] [-memory GB] [-disk GB] NAME` now creates an owner-only
bundle, sparse `disk.img`, zero-filled 512 KiB `sep.img`, and validated config.
Defaults are 8 CPUs, 8 GiB memory and 64 GiB disk. CPU and memory must fit the
current VZ host limits; size overflow, unaligned disk sizes, invalid names and
existing destinations fail. Config is published after storage creation; errors
remove the incomplete new directory. Existing bundles are never overwritten.
This is not crash-atomic directory publication or a completed provisioning stage.

Implementation lives in `internal/ios/create_darwin.go`; command parsing remains
in `cmd/cove`. Blank bundles are recognized by the shared registry. Recipe updates
now propagate config-load errors instead of replacing invalid iOS metadata.
A signed CLI smoke test caught and fixed creation of an unrelated default VM
directory during startup; iOS commands now use the startup exemption.
Tests cover persisted settings, storage sizes/SEP zeros, collisions, invalid
inputs, registry recognition, and CLI flows using rsc.io/script.

Machine identity, hardware model and NVRAM are not fabricated by this command.
Their create-once lifecycle remains required with the runtime graph. Config
editing, ROM staging, compatibility aliases and Finder routing remain open for
F03 and related requirements. This increment does not establish VM permission,
DFU enumeration, restore or boot. Focused tests and `go build ./...` pass.

### Notebook review disposition

Increment 4 review (`aabe7fdf-29a9-4998-8ae0-933cd78f9133`) remained INCOMPLETE.
Accepted: hard termination can leave an incomplete destination because Go defers
cannot run; atomic publication/recovery remains an open bundle-lifecycle item.
The current code rejects retries at that destination, preserving existing data.
ROM rejection is intentional for blank creation, with later staging still missing.
The notebook mislocated the startup exemption: it is in
`cmd/cove/cli_skip_vmdir.go`, not `utils.go`. Its attribution of the research
launch kill to a specific host policy is still unproven. The full suite again
failed with SIGTRAP in `TestPrivateAPI_NameGetSet`; focused tests passed.
No runtime/restore/guest acceptance was granted.

## Increment 5: platform identity lifecycle

`internal/ios/platform_darwin.go` adds `openPlatform`, intended for the main-thread
runtime builder while holding cove's bundle run lock. It creates the research
hardware model, machine identifier and auxiliary storage once, and reopens
existing NVRAM without overwrite. Saved hardware-model bytes must match the
pinned research profile. Partial, empty, symlinked or corrupt identity sets fail;
there is no automatic identity regeneration. Explicit initialization is required
when no identity files exist. Every identity-file write is exclusive and synced.
Creation interrupted between files leaves a partial set requiring recovery from
a consistent backup; resumable atomic identity publication remains open.

The `_ECID` getter's return and argument encodings are checked before invocation.
Actual host tests create a public VZ machine identifier, save/reload its opaque
representation and verify identical nonzero ECID. Tests also cover all partial
file combinations, corrupt identifier preservation and no-overwrite behavior.
This proves identifier persistence and the getter ABI on this host only.

The platform constructor itself is not yet invoked by a runtime or qualified
with PV=3, NVRAM, firmware or VM start. Machine-identity tests create no VM.
The runtime still needs the complete device graph, initialization recovery,
boot-argument policy, command/Finder dispatch and signed DFU observation.

### Notebook review disposition

Increment 5 review (`8041cf01-265b-4d42-8163-b9ed069b2efe`) remained INCOMPLETE.
Accepted: the platform is not wired into a runtime, and interrupted initialization
requires recovery. Rejected: speculative dangling references from releasing local
owned objects after assignment. Actual Objective-C property attributes on this
host are `hardwareModel: T@"VZMacHardwareModel",C,V_hardwareModel`,
`machineIdentifier: T@"VZMacMachineIdentifier",C,V_machineIdentifier`, and
`auxiliaryStorage: T@"VZMacAuxiliaryStorage",&,V_auxiliaryStorage`. The first two
copy and the third retains; a new test checks these ownership attributes. The
caller must release the returned platform and provide the documented main-thread,
autorelease-pool and run-lock context. That caller still needs implementation.

The notebook also incorrectly reused a historical nested-host diagnosis as the
current host. Current `sysctl -n hw.model kern.hv_vmm_present` returns `Mac16,8`
and `0`. Neither historical host diagnostics nor these current values establish
PV=3 execution permission. The full suite again failed at the baseline
`TestPrivateAPI_NameGetSet` SIGTRAP; the build and focused platform tests pass.

## Increment 6: device graph builder

`internal/ios/graph_darwin.go` now connects `openPlatform` to a VZ configuration
builder. It sets CPU/memory, AP ROM, portrait Mac graphics, host audio input/output,
virtio disk, configured network/MAC, entropy, USB keyboard, optional vsock and
PL011 serial pipes. Initial NVRAM boot arguments enable serial output; existing
NVRAM is reopened. A missing MAC is generated and persisted only during explicit
first initialization, never silently regenerated for an existing identity.

`internal/ios/devices_darwin.go` attaches the three required accelerators, USB
touch, synthetic battery, AP debug stub and SEP coprocessor/storage/optional ROM
with its own debug stub. Debug listeners are configured for loopback, with zero
meaning automatic port assignment. Private scalar setters and constructors are
checked against runtime encodings; missing classes/setters fail. Local owned
Objective-C objects are released after configuration assignment. The graph owns
all serial pipe descriptors until shutdown or construction failure.

The builder checks files and host hardware limits before identity creation and
calls VZ configuration validation at the end. It has not yet been invoked through
a command or exercised with actual PV=3 model construction. ROM provenance/hash
checks, runtime launch/start options, cancellation/serial pumps, VM lifetime and
run-lock integration remain required. No live device-graph validation or DFU
success is claimed. Tests verify missing-ROM rejection before mutation, input
path handling and pipe cleanup. An observational probe found the private scalar
setter and constructor ABIs available on this host; it creates no devices or VM.

### Notebook review disposition

Increment 6 review (`4dd8abef-c261-448f-b5f5-91d30d2af91c`) remained INCOMPLETE.
Its source audit found the device membership consistent with pinned vphone,
accepted the verified platform ownership semantics, and prioritized runtime
integration, serial pumps and DFU discovery. Those are required next steps, not
completed acceptance. Rejected: blanket-skipping `TestPrivateAPI_NameGetSet` to
make gates pass; the native SIGTRAP still needs an evidence-backed diagnosis.
It is a test-process crash, not evidence of a machine-wide failure. The build
passed and full tests again stopped at that baseline test.

The notebook cannot directly inspect the entire Apple module from its source
set. That limitation does not mean the dependency is unpinned or compilation is
unverified: the builder compiles against the exact go.mod module revision, and
host ABI probes cover the documented methods. Other runtime behavior remains
unverified until graph construction and start are actually exercised.

## Increment 7: headless runtime entry point

`cove ios run [flags] NAME` now reaches the graph through `internal/ios.Session`.
It supports explicit identity initialization, force-DFU, either iBoot stop flag,
AP debug port and a run timeout. Host preflight precedes acquisition of cove's
existing run lock. The early command executes on cove's locked main thread.
Session construction creates an actual VZVirtualMachine; Start applies private
start options and pumps the main run loop for asynchronous completion. Wait
observes VM state, cancellation and serial-output failure. Stop waits for startup
to settle before requesting VZ stop. Serial output is copied to command stdout;
ECID and diagnostics use stderr.

Close refuses an active VM. The command retains the bundle lock until process
exit if shutdown cannot reach a terminal state; successful close releases the VM,
graph and pipes before releasing the lock. A canceled start wait does not pretend
to cancel VZ's asynchronous operation. Serial drain has a context deadline; a blocked writer leaves session ownership
intact so cleanup can be retried or the command can exit with its lock held.
Interactive serial input and bounded steady-state output backpressure remain open.

Focused tests cover canceled construction without mutation, asynchronous wait
completion/error/cancellation, closed-session behavior and CLI argument rejection.
A signed public CLI smoke test rejects run at preflight and leaves config and
storage unchanged, without creating identity or a run lock. This does not
exercise VM construction. General `cove run`, daemon/Finder/GUI dispatch, runtime
PID/status integration, DFU enumeration and live startup/shutdown qualification
remain required. No VM boot or DFU success is claimed.

Fresh research-profile build and signing succeeded, but executing
`cove-research ios preflight` returned subprocess status -9 (SIGKILL), with empty
stdout/stderr. The process did not reach preflight output. This revalidates the
host execution qualification failure without attributing a specific policy cause.
No host security settings were changed.

### Notebook review disposition

Increment 7 review (`d03ad56a-9179-4502-9632-2b09527aea37`) remained INCOMPLETE. Accepted: serial drain
could wait forever for a blocked writer. Close now takes a context, preserves the
graph on drain timeout, and permits retry. Tests verify buffered output delivery
and canceled drain ownership/retry. The command gives serial drain five seconds.

Rejected: a canceled start is untracked. The CLI retains the Session and runs
Stop/Close even after Start returns an error. Holding the run lock when Close
rejects an active VM is intentional; early dispatch exits the process afterward,
which releases the flock. There is no claim that context cancellation aborts VZ.
The constructor's documented callbacks execute on the main queue, not an arbitrary
system thread. Inspection did reveal a different binding issue: generated
Start/Stop completion helpers discard NewErrorBlock's cleanup. Session now creates
and releases its owned callback blocks explicitly around the native async calls;
the framework owns its asynchronous copy. Live callback/start/stop qualification
still remains open.

## Increment 8: stopped configuration and ROM staging

`cove ios config NAME [flags]` reads config as JSON or edits CPU/memory, display
width/height/PPI/scale, network/MAC, AP/SEP ROM paths and no-vphoned. Flags may
also precede NAME. Mutations acquire cove's run lock before loading config;
read-only inspection does not mutate files. `internal/ios.Configure` preserves
other cove metadata and validates hardware limits and iOS schema before staging.

ROM inputs may be absolute or relative to the bundle. Staging streams into an
owner-only temporary file, computes SHA-256, syncs and publishes via an exclusive
hard link under `roms/<sha256>.rom`. Reuse verifies the existing bytes. Config is
published only after both ROM inputs stage successfully. The runtime verifies
staged ROM hashes before graph construction. These hashes prove integrity of
staged bytes, not firmware authenticity, profile compatibility or bootability.
Returned staging/save errors remove only content newly published by that update;
existing cached ROMs and config remain intact. Hard termination may still leave
unreferenced content. Staged ROM garbage collection and provisioning manifests
remain open.

Tests cover original-file removal after staging, repeat staging, corrupt staged
bytes, failed-update config preservation, metadata preservation, symlinked ROM
cache rejection, and run-lock contention. A signed public CLI smoke test stages
a synthetic nonbootable fixture, updates network/display and reads the same JSON
back. Focused tests and builds pass. These checks do not qualify a ROM or VM.

### Notebook review disposition

Increment 8 review (`e9500189-8182-4716-a0d5-83f3aa00af04`) remained INCOMPLETE.
Accepted: already-staged ROMs were unnecessarily copied during non-ROM edits.
They now return their existing verified reference without a temporary copy.
Accepted: an ordinary failed multi-ROM update left newly published content.
Configure now tracks and removes only files it created; a regression test proves
both cleanup of new content and preservation of existing cached ROMs/config.

Rejected: remove digest verification on boot. File integrity may change after
staging, and no immutable-file guarantee exists. Keep verification before graph
construction; any future optimization needs measured cost and an equivalent
integrity check. The notebook again suggested gRPC for vphoned, which contradicts
the pinned length-prefixed JSON protocol; that suggestion is not part of the plan.

## Increment 9: native diagnostic test prerequisites

The recurring public-suite SIGTRAP was reproduced in an isolated name-accessor
test and examined under LLDB. Moving `_name` onto the VM's dispatch queue did not
remove the trap. The native backtrace was `Base::assertion_trap` called by
`-[VZVirtualMachine(VZPrivate) _name]`. Disassembly shows a check of entitlement
bit 1; `entitlements_from_task` maps `com.apple.private.virtualization` to bits
0 and 1, while the public virtualization entitlement supplies only bit 0.
This identifies a private-entitlement prerequisite, not a stopped-VM failure.

`TestPrivateAPI_NameGetSet` now checks the current process's effective boolean
entitlement with `SecTaskCopyValueForEntitlement` and skips only when that
prerequisite is absent/inactive. Entitlement lookup errors fail the test. The
name test remains executable on a qualifying private-entitled test process.
This is narrower than the previously rejected blanket skip suggestion.

The crash-context getter/setter actually succeeds without private entitlements
on the VM queue, so that test remains enabled. Public `State()` coverage, which
was previously unconditionally skipped, is now enabled and proves the newly
constructed VM is stopped. The fixture releases its VM on that queue. Focused
results on the public test process: name test skips for its measured prerequisite;
crash-context and public-state tests pass. No research-profile execution or PV=3
acceptance is inferred from these tests. The research executable's pre-output
SIGKILL remains a separate unresolved qualification failure.

### Notebook review disposition and gates

Increment 9 review (`53354841-94c9-4794-a10c-8dd8956fd578`) accepted the narrow
entitlement prerequisite and restored public-state coverage. Overall verdict:
INCOMPLETE. Final `go build ./...` and `go test ./...` pass on the public-profile
test process, with the name test skipped because its effective private entitlement
is absent. Crash-context and public-state tests execute and pass. This replaces
the earlier unresolved baseline-failure status; it does not qualify private APIs.
The native diagnosis was made on Mac16,8, macOS 27.0 build 26A5425a.

## Increment 10: pinned toolchain source preparation

`cove ios setup source` now prepares an isolated checkout of vphone revision
`87f796c62a7cb385cd37afce121f6e222d83e5b5` and its recursive submodules.
`-repository` can seed objects from a local checkout without changing that
checkout. `-dir` selects the cache; `-check` reports source status without fetching.
The source manifest records the root commit, recursive dependency commits and
requirements file SHA-256. Existing caches must match their manifest before reuse.
Returned failures remove only the newly created destination; an interrupted
process can leave a partial directory, which subsequent setup rejects.

Git commands have bounded output and a context deadline. Cancellation kills the
Git process group, including fetch descendants. Tests cover submodule parsing,
preservation of existing directories, cleanup on clone failure, cancellation
before mutation, output truncation and cancellation of a real child process.

The signed public CLI successfully prepared the source cache on the current host.
All nine recursive submodules (including Capstone's nested repository) are clean.
The requirements digest is
`fec8a19e8016b6b851af57458d04979181f459b8e94e73a7ba66673828a007ea`.
Read-only inspection and cached reuse return byte-identical reports. The original
vphone checkout remains clean with its eight direct submodules uninitialized.
Focused firmware, iOS and CLI tests pass, and the public CLI builds and signs.

This is source preparation only. Upstream Python requirements are not version
locked; the digest identifies their text, not a reproducible resolved environment.
Python dependency locking/install/probes, native and Swift tool builds, artifact
manifests, firmware preparation and restore remain required. F01 is partial.

### Notebook review disposition and gates

Review `2e949aba-1e85-479f-85e3-618bb52b3da5` returned INCOMPLETE. Full
`go build ./...` and `go test ./...` passed for the source-preparation increment.
Accepted the missing-cache diagnostic improvement: inspection now returns a
structured source problem when the source directory is absent or is a file.
The CLI prints this JSON and exits unsuccessfully. Tests cover both cases.
The previous behavior returned a handled command error, not the claimed crash.

Keep exact requirements-byte verification: the manifest identifies source bytes,
not semantically normalized text or an installed Python environment. Automatic
deletion of an existing incomplete directory is not implemented; rejecting it
preserves user data. Crash recovery still needs an explicit ownership/publication
contract. The notebook's description of implemented runtime/configuration code as
stubs is inaccurate; that code exists and has tests, but lacks live qualification.
No host security change or boot success follows from this review.

## Increment 11: reusable Go device packages

VZ hardware/identity/start helpers now live in `apple/x/vzkit/exp/research`.
Cove calls them while retaining VM and bundle ownership. New sibling packages
provide usbmux device listing/connections, libusb recovery endpoint access,
restored plist framing and TSS request transport. `cove ios devices` successfully
queried both host transports with no attached endpoints. This does not demonstrate
DFU, restore or boot. See [package boundaries](ios-device-packages-2026-09-07.md)
for the published dependency, validation and missing restore stages.
F01–F39 acceptance remains incomplete; new transport primitives do not complete
F09 restore or qualify the current host.

## Increment 12: native DFU/recovery image transfer

The published Apple dependency now includes `irecovery.Conn.Upload`, with
fixture-tested DFU block/footer transfer and recovery bulk/ZLP handling. Polling
checks bStatus and the full 24-bit timeout. The notebook's overbroad recovery
size-limit finding was fixed; initial DFU error recovery sends clear/abort and
returns an error requiring explicit retry. See the package-boundaries document
for source discrepancies, tests and exact limits. This is a partial F09 building
block: Cove has no complete restore command, and manifestation/reset/reconnect,
personalization, ASR and live qualification remain outstanding.

## Increment 13: DFU finalization and reconnect primitives

Apple commit `3e8ad6225` adds `FinalizeDFU` and `WaitOpen`. Finalization sends the
next zero-length block, polls manifestation status, resets USB and retires the
connection. Cancellation returns control to the caller while retaining native
resources until reset and cleanup finish. Reconnect rejects ambiguous ECIDs and
waits for the requested mode. Race tests exercise ordering, lifetime, polling,
errors and endpoint selection. No live firmware writes were performed.

This remains partial F09 support. Cove still needs restore sequencing,
personalization, ASR, durable stage tracking and live qualification. Native reset
has no timeout; `Close` waits for any outstanding reset before releasing resources.

## Remaining ledger


F01 (toolchain sources), F03 (configuration), F09 (restore transports), F12 (dispatch), F17 (device planning), F18 (signing), and
F39 (serial/debug configuration) have partial foundations.
None is accepted as complete. F02, F04–F08, F10–F11, F13–F16 and F19–F38 remain
unimplemented for iOS. In particular, the CLI exposes preflight, blank-bundle creation, stopped configuration and the experimental headless
`ios run` entry point. The graph builder is not runtime-qualified; DFU observation, firmware adapter,
guest transport and GUI integration remain absent.
No host/firmware profile has been qualified and no iOS boot success is claimed.

Next: qualify the existing runtime on a research-capable host, integrate general
CLI/Finder dispatch, and prove signed DFU enumeration before restore.
The planned durable stage manifests and all guest/desktop/media requirements
remain required. Notebook review must assess actual implementation against every
ledger row, never treat the previous plan approval as implementation completion.

## Increment 14: isolated firmware preparation

`cove ios firmware prepare -source PATH -iphone IPSW -cloudos IPSW OUTPUT`
now runs the pinned prepare/manifest recipe for explicit local archives. Each
attempt owns its cache, extraction directory and log. Success publishes
`firmware.json` after checking the hybrid identity, required component paths,
plist readability and hashes of every output file. Input archives are retained.
A repeated invocation checks the input and output hashes before reuse; changed
inputs require a new output directory. Failed attempts remain separate and never
publish prepared state. Output locking prevents concurrent prepare writers.

The real pinned recipe passes a synthetic IPSW integration test. That test found
that upstream's stale-tree cleanup deletes a cloudOS directory containing
`Restore` in its name; Cove stages cloudOS under a fixed name without that token.
ZIP traversal, links and case-colliding entries are rejected before extraction.

F05/F06 are partial: URL/catalog resolution, firmware-pair compatibility,
real-image qualification, ROM outputs and bundle stage integration remain
unfinished. This command prepares the common firmware tree; less-variant sealing
tool acquisition remains separate and unimplemented with mount journaling.
Python is still required by the delegated manifest generator. No restore or boot
completion follows from preparing synthetic archives.

## Increment 15: built patcher toolchain

`cove ios setup patcher [-dir TOOLCHAIN] [-developer-dir XCODE_DEVELOPER_DIR]`
builds the pinned Swift executable and verifies its `patch-firmware` interface.
It publishes a content-addressed executable and `patcher.json` containing the
source revision, selected developer directory, Swift version, SDK path/version,
and executable SHA-256. The build has exclusive cache ownership, supports
cancellation, preserves incremental compilation, and rejects changed generated
build info or a corrupt published executable.

The selected Command Line Tools failed to compile `.macOS(.v15)` in the pinned
package manifest. Its arm64 PackageDescription interface lacks that member.
A command-local `DEVELOPER_DIR=/Applications/Xcode-beta.app/Contents/Developer`
resolved the failure; no system-wide developer selection was changed. The full
pinned executable built and its patch command ran. The Go setup integration also
passed under race testing with the real source cache and Xcode installation.

This completes another part of F01, not F07 patch acceptance. The `less` Swift
pipeline mounts filesystem images through absolute `hdiutil` calls and uses
process-local defers for detachment. Cove still needs durable mount ownership and
crash reconciliation before exposing that pipeline. Non-less patch execution,
patch-record/skip validation, ROM staging and all real firmware qualification
also remain required. No disk image was mounted or guest firmware patched here.

## Increment 16: mount ownership and crash recovery

`firmware.MountJournal` records durable intent before attaching through a private
hard link in the attempt directory. It rejects an already-attached source inode.
Cleanup checks the private link's filesystem identity, live image path, owner,
complete device set and helper PID when available before detaching the backing
device. A reused device number alone never authorizes detachment. Recovery must
observe the owned image absent before recording completed cleanup. An unresolved
attach with no observable image remains pending, since a killed command might
still complete; callers must not start another writer in that state.

An opt-in integration test created temporary 32 MiB HFS+ and 64 MiB APFS images and killed a
helper after successful `hdiutil attach` but before recording returned device
nodes. A fresh journal recovered the orphaned attachment and confirmed its
absence. The baseline's unrelated Metal toolchain attachments remained intact,
and no test attachment remained. Fake-backend/race tests cover changed ownership,
reused nodes, pre-existing attachments, busy detachment and malformed inventory.

This is a prerequisite for F07/F10, not completed patch integration. The pinned
Swift filesystem patcher still calls hdiutil directly; the controlled bridge,
privileged remount routing, bundle operation locking and stage gating remain to
be wired. No firmware image was patched, and no guest restore or boot occurred.

## Increment 17: Swift mount bridge

The patcher build now applies a compiler file overlay to the pinned
`CryptexFilesystemPatcher`. Tracked upstream files stay unchanged. `patcher.json`
records the patched source digest as `overlaySHA256`. Exact call-site checks
reject source drift. The overlay routes attach/detach, volume unmount and writable
remount operations through Cove's internal mount helper. There is no direct-mount
fallback if the helper or journal environment is absent.

The helper validates live journal ownership for unmount/remount, propagates
privilege failures without trying to elevate, and verifies operation results.
Swift drains helper stdout before waiting and keeps stderr separate from plist
output. The high-level patch runner must provide `COVE_MOUNT_HELPER` and
`COVE_MOUNT_JOURNAL` while holding the workflow lock, then recover mounts after
child exit. That runner and its stage gates remain unimplemented.

An opt-in integration test compiled the actual overlaid Swift class and exercised
its attach, unmount and detach methods against a temporary 64 MiB APFS image
through the signed Cove CLI. It checked the resulting journal, confirmed live
absence and removed the fixture. This qualifies the bridge path, not privileged
writable remounts, full filesystem patching, restore or boot. The test runner
emitted duplicate Objective-C class warnings from host developer frameworks but
completed successfully; no host framework or security settings were changed.

The overlay also drains the vendor's general subprocess output before waiting.
The live Swift integration verifies a 128 KiB child output before performing the
APFS mount sequence, covering output larger than a pipe buffer. Both checks pass.

## Increment 18: firmware patch runner

`cove ios firmware patch -prepared DIR -rom AVPBooter.bin [flags] OUTPUT`
now invokes the pinned Swift pipeline for less, regular, dev, jb and exp. It
validates variant-only options and the tool's source, overlay and binary receipt.
`-quiet` suppresses terminal progress while retaining verbose diagnostic output;
`-records-out` exports validated records. Less requires explicit `-python` and
`-seal-dir`; the Python dependency probe and versioned sealing executable check
run before patching. Frida requires a cloudOS version of at least 26.4.

Each attempt copies the prepared tree and ROM, records input/tool/options hashes,
and holds an exclusive output lock. The Swift child inherits that lock so an
abrupt Cove exit cannot permit a concurrent retry. Temporary image files and
mount journals live on the attempt filesystem. After child exit, a separate
recovery timeout detaches journal-owned images. Retry recovers prior attempts
under the same workflow lock. Failed attempts retain their logs and state.

Publication requires valid records for the selected component/variant families,
records for explicitly requested Frida/EXC_GUARD patches, no `[-]` or failed-copy
diagnostics, changed firmware bytes, readable hybrid manifests and clean mount
recovery. `patched.json` records all output hashes and the log/record digests;
reuse verifies them. This is structural validation, not a firmware-profile
conformance result. Optional upstream `[~]` skips remain visible in the log.

Fixture tests cover all variants, input isolation, result reuse/tampering,
concurrent retry, inherited process locks, child/cancellation/cleanup failures,
missing records, diagnostics and unchanged output. A real pinned-patcher test
reached AVPBooter and rejected intentionally invalid fixture firmware without
publishing a result. The compiler overlay also passes the resize path as a shell
argument, removing upstream path interpolation into shell code.

Real firmware output comparison for every supported profile, less filesystem
patching with privileged remounts, complete checksummed host-resource provisioning,
and integration with bundle create/restore stages remain unqualified or
unimplemented. The runner does not establish F07 acceptance or full F01–F39 parity.

## Increment 19: native ASR and restore identity probes

Apple `x/iosrestore` now provides native ASR `SendImage` and restored `QueryInfo`.
The ASR protocol has dedicated bounded XML framing, random-access OOB validation,
128 KiB payload chunks, optional SHA-1 checksums, renegotiation and cancellation.
Payload completion is explicitly distinct from restore completion.

Cove adds `ios restore probe -ecid N [-udid SERIAL]`. It queries hardware identity
on USB restored services, rejects ambiguity and incomplete discovery, and sends
no restore/reboot command. Protocol and discovery tests pass; see the
[package evidence](ios-device-packages-2026-09-07.md#native-asr-and-restored-identity).
No live ASR transfer, complete restore dispatcher, image personalization, AEA
staging, offline ticket binding or guest boot is claimed. F09 remains incomplete.

## Increment 20: native IMG4 assembly

Apple `x/img4` now assembles IMG4 from supplied IM4P/IM4M with optional FourCC
retagging and typed IM4R properties. Payload compression/encryption bytes, PAYP
fields and ticket bytes remain intact. It does not authenticate tickets or choose
restore policy. Synthetic wire vectors and OpenSSL checks support the encoding;
no real firmware/ticket pair or device restore is qualified. See the
[package boundary](ios-device-packages-2026-09-07.md#img4-container-assembly).
Cove's identity-bound ticket and personalization policy adapter remains required.

## Increment 21: ticket assertions and component policy

Cove now implements pinned component FourCC selection, nonce-slot precedence and
TBM property assembly atop Apple `x/img4`. BNCN reversal occurs only in the Cove
adapter, on copied bytes. Unknown components preserve their existing IM4P type.
Apple adds structural MANP/image property inspection with duplicate and private
tag checks. Cove compares ticket assertions with supplied observed identity,
current nonces and signing-build digests, preserving high-bit ECIDs.

Golden wire, malformed-input, mismatch, race and parser fuzz tests pass. These
checks do not authenticate tickets or establish patched-payload eligibility.
The adapter is not wired into a complete restore controller; automatic signing
request construction and reacquisition after nonce changes remain required.
See [the policy boundary](ios-device-packages-2026-09-07.md#ticket-assertions-and-component-policy).
Full F09 and F01–F39 acceptance remain incomplete.

## Increment 22: native AP signing request path

Cove now derives AP IMG4 signing requests from a selected build identity and
current device observations, sends them through native TSS transport, and checks
the returned ticket against next-stage image requirements. Board/chip mismatch
fails before requesting a ticket. Rules use typed boolean equality, including
false, and reject unsupported conditions. The path checks SDOM/CPRO/CSEC as well
as ECID, board/chip IDs, AP/SEP nonces and selected signing digests.

Request, rule and HTTP integration tests pass. The work remains internal restore
controller code: it does not launch DFU, observe nonces automatically, dispatch a
complete restore or prove boot. Separate ticket flows and offline persistence
remain required. See the [signing boundary](ios-device-packages-2026-09-07.md#native-ap-signing-request-path).

## Increment 23: recovery signing observations

Apple adds fresh serial/nonce observations on an already-selected recovery
connection. Cove converts complete observations into signing inputs and exposes
`ios restore recovery-probe -ecid N -libusb PATH`. Missing board/security values
remain distinct from zero, and unknown demotion policy cannot match a false-valued
rule. The path is read-only and does not qualify a complete restore or guest boot.
See the [observation boundary](ios-device-packages-2026-09-07.md#recovery-signing-observations).

## Increment 24: recovery commands and component controller

Cove connects observation, AP signing, personalization and native USB transfer in
`restore.TransferComponent`. It records durable intent, rechecks nonces before
writing, verifies device identity across DFU reset or recovery `go`, and blocks
replay of recorded attempts. Per-device locking covers separate attempt
directories. Apple adds recovery commands, getenv and old-handle disconnect
observation, with transfer errors preserved.

This controller handles explicit component operations, not the complete restore
graph. Restored/ASR dispatch, separate tickets, whole-bundle lifecycle and live
qualification remain required. See the [controller contract](ios-device-packages-2026-09-07.md#recovery-commands-and-component-controller).

## Increment 25: recovery OS root signing

Cove now builds a distinct recovery-root request and validates its returned IMG4
ticket through `restore.SignRecovery`. Its manifest selection follows the pinned
recovery path rather than the ordinary AP component filter. Focused race and HTTP
integration tests pass. Local-policy signing and full restore orchestration
remain unfinished; no live signing or restore run is claimed. See the
[request boundary](ios-device-packages-2026-09-07.md#recovery-os-root-signing).

## Increment 26: recovery-stage local-policy signing

Cove now requests and checks the empty recovery local policy, binding it to an
AP ticket validated against current observations. Component transfer selects this
flow for `Ap,LocalPolicy`, records the ticket hashes, and runs `lpolrestore`.
Retained AP responses can be revalidated and reused for the next-stage component
so its ticket matches the policy binding. HTTP and transfer tests pass, including
stale-ticket rejection before device writes. See the
[local-policy boundary](ios-device-packages-2026-09-07.md#recovery-stage-local-policy).
Volume-bound restored policy and the full restore controller remain unfinished;
no live signing, restore or boot is claimed.

## Increment 27: restored volume-bound policy

Cove now constructs the `LocalBoot=true` signing request from restored's supplied
hashes and volume UUID, checks the returned policy bindings, and produces the
personalized service response. Input, assertion, HTTP and framed-service tests
pass under the race detector. See the
[volume-policy boundary](ios-device-packages-2026-09-07.md#restored-volume-bound-policy).
Build-identity selection, service routing and the complete restore controller
remain required. This does not establish live restore or F01–F39 completion.
