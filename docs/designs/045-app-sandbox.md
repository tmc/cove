# Apple App Sandbox proof lane

Status: v0.7 proof lane. The entitlement fixture, opt-in smoke harness,
host-process status reporting, elevation fail-closed guard, package-shape claim
boundary, macgo `.app` non-mutating proof, listener proof, scratch VM
start/stop proof, security-scoped bookmark binding proof, durable bookmark
store proof, non-interactive bookmark consumption, the first opt-in Powerbox
prompt command, a mocked Powerbox fallback router, and automatic `status <vm>`
Powerbox fallback are implemented. Ambient host-path and mutating command
surfaces now fail closed under Apple App Sandbox. A sandboxed run-worker IPC
proof can receive an explicit descriptor from the unsandboxed CLI. Broader
automatic command fallback and temporary-RAM overlay proofs are still queued.

This design tracks whether cove can run selected host-side runtime surfaces with
Apple App Sandbox enabled. This is separate from cove's existing guest
containment flags in `sandbox.go`: `-sandbox-level strict` changes the VM
configuration, while `com.apple.security.app-sandbox` changes what the host
process itself may access.

## Goal

Produce a small, repeatable proof that says which cove process shape can run
under Apple App Sandbox:

- raw CLI binary;
- bundled `.app` runtime;
- sandboxed run-worker launched by the CLI;
- unsandboxed CLI plus sandbox-aware status/proof mode.

Do not claim cove is "fully sandboxed" until a sandboxed process can start
normally, report its state directory, create or find the control socket, and run
one scratch VM without mutating existing user VMs.

## Current evidence

The normal entitlement file only grants local networking and
Virtualization.framework access:

- `internal/autosign/vz.entitlements`
- `macgo_bundle.go`
- `doctor_host.go`

The App Sandbox proof entitlement lives at
`internal/autosign/app_sandbox.entitlements`:

```xml
<key>com.apple.security.app-sandbox</key>
<true/>
<key>com.apple.security.files.bookmarks.app-scope</key>
<true/>
<key>com.apple.security.files.user-selected.read-write</key>
<true/>
<key>com.apple.security.network.client</key>
<true/>
<key>com.apple.security.network.server</key>
<true/>
<key>com.apple.security.virtualization</key>
<true/>
```

The opt-in local smoke harness is `TestAppSandboxSmoke` in
`app_sandbox_smoke_test.go`. It builds and signs a throwaway binary:

```bash
COVE_APP_SANDBOX_SMOKE=1 go test -count=1 -run TestAppSandboxSmoke -v .
```

Observed result:

- `codesign` succeeded and embedded the App Sandbox entitlement.
- `spctl --assess --type execute` rejected the ad-hoc binary.
- `--version`, `help`, and `list` failed before normal CLI output with
  `signal: trace/BPT trap`.

That is enough to make raw-binary sandboxing a proof problem before any live VM
mutation test.

The macgo-backed `.app` smoke harness is `TestAppSandboxMacgoBundleSmoke`:

```bash
COVE_APP_SANDBOX_MACGO_SMOKE=1 go test -count=1 -run TestAppSandboxMacgoBundleSmoke -v .
```

Observed result:

- macgo creates an ad-hoc signed `.app` with App Sandbox,
  Virtualization.framework, local networking, and user-selected read/write file
  entitlements.
- LaunchServices starts the app and `security status` reports
  `apple app sandbox: true`, `apple app sandbox id: com.tmc.cove`, and the
  effective `home`, `state root`, and `vm root` paths.
- On this host, `APP_SANDBOX_CONTAINER_ID` is empty for the ad-hoc bundle; the
  reliable active-sandbox signal is that `HOME` is rewritten to
  `~/Library/Containers/com.tmc.cove/Data`.
- macgo's FIFO child check-in path is not compatible with this sandbox proof.
  The macgo workspace now uses LaunchServices `--stdout` and `--stderr` file
  redirection for App Sandbox launches.
- `cove list` starts through the sandboxed macgo bundle. On the current host it
  can still enumerate the operator's VM registry, so this is only a startup
  proof. It is not an isolation claim for VM discovery until the file-access
  model is reduced to explicit grants or security-scoped bookmarks.
- `security probe-sandbox -json` passes Unix-socket, loopback TCP, subprocess,
  and scratch VZ start/stop checks through the sandboxed macgo bundle. The
  scratch VM proof uses a deliberately long VM path under the app container
  temp directory, a hashed short control socket path, an APFS clone of an
  existing Linux disk, and EFI boot, then stops the VM before guest readiness is
  required.
- `COVE_STATE_DIR` is the first explicit state-directory grant contract. When
  set, all `vmconfig` roots move from `$HOME/.vz` to that granted root, and
  macgo forwards the variable into the sandboxed LaunchServices child. The
  current smoke proof uses a grant inside the app container and proves `list`
  sees only that granted VM root, not an ambient container `.vz`.
- Ambient command-line host paths and mutations are explicit denials in the
  sandboxed macgo proof. `-vol`, `-share-dir`, `-usb`, `-block`, explicit disk,
  ISO/IPSW, kernel/initrd, pcap paths, disk swap/resize, provisioning,
  helper install/uninstall, shared-folder mutation, install, and `up` return a
  typed Go error instead of reaching OS-level sandbox traps.
- `__run-worker probe` is the first hidden sandboxed run-worker boundary proof.
  The unsandboxed parent opens a grant file and sends its descriptor to a
  sandboxed child over a Unix socket with `SCM_RIGHTS`; the child reports
  `apple_app_sandbox: true`, receives the descriptor, and verifies the payload.
- `security bookmark-probe -json` exercises the purego Foundation bookmark
  calls through the sandboxed macgo bundle. It creates an app-scoped
  security-scoped bookmark for a temp file inside the app container, resolves
  it, starts access, reads the file, and stops access without an Objective-C
  trap. This proves the data model and binding path, not durable grant storage
  or Powerbox UI.
- `security bookmark-store save` and `security bookmark-store resolve` add the
  first durable grant store proof. The store is a JSON file under
  `~/Library/Application Support/cove/security-scoped-bookmarks.json` by
  default, which resolves inside the app container for the sandboxed macgo
  bundle. The smoke uses a test store inside the app container temp directory
  and resolves it from a separate sandboxed process.
- `status <vm>` can consume a pre-staged `vm:<name>` bookmark when the VM is
  outside the sandboxed process's normal VM root. Missing bookmarks fail closed
  with a typed Powerbox/bookmark grant-required error. The current automated
  smoke stages a bookmark for a VM under the app container temp directory but
  outside `.vz/vms`; arbitrary host VM roots still need real Powerbox UI before
  they can be bookmarked by a sandboxed process.
- `security powerbox-prompt` is the first interactive Powerbox primitive. It is
  not wired into automatic status/run fallback yet; it opens `NSOpenPanel`,
  asks the operator to choose a directory, creates an app-scoped bookmark, and
  writes it to the durable bookmark store. Its smoke is opt-in because it
  requires a human click.
- `withPowerboxFallback` is the first non-interactive fallback router. It catches
  structured grant-required errors, calls an injectable Powerbox prompt seam,
  writes the returned bookmark to the durable store, and retries once. Tests use
  a mocked prompt; regular gates do not open `NSOpenPanel`.
- `status <vm>` is the first command wired through the Powerbox fallback router.
  Missing VM-root grants route to the prompt seam only for interactive terminal
  invocations, store a `vm:<name>` bookmark, and retry once. Noninteractive
  runs keep the grant-required error so automated LaunchServices smoke tests do
  not open `NSOpenPanel`. Regular tests mock the prompt and assert that retry
  reaches the stopped-VM diagnostic instead of returning another grant-required
  error.
- `VZTemporaryRAMStorageDeviceAttachment` is not part of the passing proof. On
  this host it traps outside App Sandbox too, with `FIXME: "Implement" line 52`
  after `Starting virtual machine...`. Cove therefore fails closed before
  creating runtime temporary-RAM storage attachments.

## Expected breakage map

These surfaces need explicit proof or denial before any release claim:

- ambient access to `~/.vz` VM and image roots;
- `-vol`, shared-folder, `-usb`, ISO/IPSW, and artifact paths supplied on the
  command line;
- Unix control sockets and helper sockets outside the app container;
- listener surfaces: HTTP API, VNC, debug stub, port forwards, proxy runtime;
- temporary-RAM storage attachments and RAM-overlay forks;
- long app-container scratch paths that push Unix domain socket paths past
  Darwin's `sun_path` limit;
- subprocess-heavy paths that call `codesign`, `hdiutil`, `diskutil`, `curl`,
  `bsdtar`, `rsync`, or `go build`;
- privilege paths in `helper.go`, `elevated_run.go`, `elevated_exec.go`,
  `provision_mount.go`, and `agent_inject.go`;
- TCC and Full Disk Access probes that inspect host or guest user paths.

## Architecture options

### A. Sandboxed bundled app owns the VM runtime

This is the cleanest App Sandbox story, but it changes cove from a CLI-first
tool into an app-first tool. It needs Powerbox or bookmarks for existing VMs,
ISOs, IPSWs, and shared folders.

### B. Sandboxed app as GUI/launcher, unsandboxed CLI remains primary

This matches the current operator model better. The CLI keeps shell-native
paths, provisioning, image operations, and helper installation. The app owns
GUI runtime surfaces and may display/operate a VM after explicit grants.

### C. Sandboxed run-worker child

The unsandboxed CLI resolves paths and opens resources, then launches a
sandboxed worker with explicit file descriptors or bookmarks. This is likely
the best long-term runtime architecture, but it requires a real process
boundary and a clear protocol.

### D. Unsandboxed CLI with App Sandbox proof mode

This is the first implementation step. It adds fixtures, status reporting, and
smoke tests so the project can measure breakage without rewriting the product.

## Supported package shapes

Current supported shape:

- Unsandboxed `cove` CLI, signed with `internal/autosign/vz.entitlements`.
  This is the only supported production shape today. It owns normal install,
  run, provision, helper, image, and control-socket workflows.

Current proof-only shape:

- Ad-hoc sandbox-signed `cove` test binary, signed with
  `internal/autosign/app_sandbox.entitlements` by
  `COVE_APP_SANDBOX_SMOKE=1 go test -run TestAppSandboxSmoke`.
  This shape is only a negative proof harness: it currently traps before
  non-mutating CLI commands start.
- Sandboxed `.app` launcher or bundled runtime using the existing
  `macgo_bundle.go` direction, signed and launched by macgo when
  `COVE_APP_SANDBOX_MACGO=1`. This shape passes the non-mutating
  `security status` and `list` startup proof. `security status` reports the
  effective App Sandbox home and VM root for each invocation.

Queued proof shapes:

- Sandboxed run-worker child launched by the unsandboxed CLI after the CLI has
  resolved paths and grants. The first descriptor-passing proof is implemented;
  VM runtime handoff still needs a typed protocol before any mutation path moves
  into it.

Unsupported claims:

- Do not describe the production CLI as Apple App Sandbox protected.
- Do not describe guest `-sandbox-level strict` or `host-containment` as Apple
  App Sandbox. They are VM configuration policies, not host-process sandboxing.
- Do not claim helper, provisioning, shared-folder mutation, disk resize, or
  arbitrary command-line host paths are sandbox-compatible until each has a
  passing proof gate.

## Queued commits

1. Done: add `internal/autosign/app_sandbox.entitlements`.
2. Done: add an opt-in App Sandbox smoke harness that builds, signs, and runs
   non-mutating commands (`--version`, `help`, `list`) while recording traps or
   sandbox denials.
3. Done: add host-process App Sandbox detection to `security status` and
   `doctor host`, reported separately from cove's guest sandbox level.
4. Done: make elevation paths fail closed when App Sandbox is detected.
5. Done: document supported package shapes and the exact proof gates before any
   "full sandbox" product claim.
6. Done: add macgo `.app` proof mode and opt-in smoke for non-mutating
   `security status` and `list`.
7. Done: add `security probe-sandbox` listener checks and a sandboxed macgo
   scratch VM start/stop smoke using a short app-container path and APFS-cloned
   Linux disk.
8. Done: investigate `VZTemporaryRAMStorageDeviceAttachment` trap outside App
   Sandbox and fail closed before creating runtime temporary-RAM attachments.

## Next implementation queue

NotebookLM re-review after the scratch VM proof ranked the next App Sandbox
work in this order:

1. Done: mitigate Unix socket path length. Long per-VM socket paths fall back to
   a hashed short path under the process temp directory, with a sidecar
   `control.token` for existing clients.
2. Done: define explicit state-directory grants. `COVE_STATE_DIR` is now the
   process contract and is forwarded through macgo; real user-selected existing
   VM access still needs Powerbox or security-scoped bookmark storage.
3. Done: make ambient host paths and mutating surfaces explicit denials.
   `cove-helper`, provisioning, offline injection, shared-folder mutation,
   install, and disk mutation remain out of the sandboxed runtime claim until
   they have Powerbox/bookmark or descriptor-based grants.
4. Done: prove the sandboxed run-worker IPC boundary. `__run-worker probe`
   launches a sandboxed child and passes an explicit descriptor over a Unix
   socket with `SCM_RIGHTS`.
5. Done: prove the security-scoped bookmark data model and Foundation binding
   path under the sandboxed macgo bundle.
6. Done: add a durable bookmark store proof. The store can save and resolve
   app-scoped bookmark bytes across separate sandboxed macgo process launches.
7. Done: consume pre-staged VM bookmarks from `status <vm>` and fail closed
   with a typed grant-required error when a bookmark is missing.
8. Done: add the first opt-in Powerbox prompt command to create durable
   directory bookmarks through `NSOpenPanel`.
9. Done: add the CI-safe Powerbox fallback router with a mocked prompt seam.
10. Done: wire `status <vm>` missing VM-root grants to the headed Powerbox
    fallback router with mocked retry/cancel tests and a noninteractive guard.
11. Next: wire missing-grant errors to headed Powerbox fallback for ISO/IPSW
    media and shared-folder host paths.

## Proof gates

Minimum non-mutating gate:

```bash
go test -count=1 ./...
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
go build -o /Users/tmc/tmp/cove-sandboxed .
codesign -s - -f --entitlements internal/autosign/app_sandbox.entitlements /Users/tmc/tmp/cove-sandboxed
codesign -d --entitlements :- /Users/tmc/tmp/cove-sandboxed
spctl --assess --type execute -vv /Users/tmc/tmp/cove-sandboxed
/Users/tmc/tmp/cove-sandboxed --version
/Users/tmc/tmp/cove-sandboxed list
COVE_APP_SANDBOX_MACGO_SMOKE=1 go test -count=1 -run TestAppSandboxMacgoBundleSmoke -v .
COVE_APP_SANDBOX_MACGO_SMOKE=1 go test -count=1 -run TestAppSandboxMacgoBundleHostPathDenialSmoke -v .
COVE_APP_SANDBOX_MACGO_SMOKE=1 go test -count=1 -run TestAppSandboxRunWorkerSmoke -v .
COVE_APP_SANDBOX_MACGO_SMOKE=1 go test -count=1 -run TestAppSandboxBookmarkProbeSmoke -v .
COVE_APP_SANDBOX_MACGO_SMOKE=1 go test -count=1 -run TestAppSandboxDurableBookmarkStorageSmoke -v .
COVE_APP_SANDBOX_MACGO_SMOKE=1 go test -count=1 -run TestAppSandboxMacgoBundleBookmarkConsumeSmoke -v .
COVE_TEST_POWERBOX_UI=1 COVE_APP_SANDBOX_MACGO_SMOKE=1 go test -count=1 -run TestAppSandboxPowerboxPromptSmoke -v .
COVE_APP_SANDBOX_MACGO_BOOT_SMOKE=1 go test -count=1 -run TestAppSandboxMacgoBundleScratchBootSmoke -v .
```

Disk resize, provisioning mutation, helper install, install/up, and
shared-folder mutation are allowed in the sandboxed proof only as denial tests
until Powerbox/bookmark grants or a sandboxed worker protocol are implemented.

A future "full sandbox" claim requires all of the following:

- a sandboxed process starts without `Trace/BPT trap`;
- `security status` reports `apple app sandbox: true`;
- state-directory behavior is recorded and does not silently hide existing VMs;
- a control socket path is created or found inside the expected boundary;
- a sandboxed worker can receive explicit grants from an unsandboxed parent;
- one scratch VM proof runs without mutating existing user VMs;
- helper, provisioning, shared-folder, and disk-resize paths remain explicitly
  denied or have separate passing proofs.
