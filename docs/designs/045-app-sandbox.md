# Apple App Sandbox proof lane

Status: v0.7 proof lane. The entitlement fixture and opt-in smoke harness are
implemented; the `.app` or run-worker packaging proof is still queued.

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
mutation test. The next useful proof should use a minimal `.app` bundle or a
macgo-backed launcher rather than assuming a standalone Mach-O is the product
shape.

## Expected breakage map

These surfaces need explicit proof or denial before any release claim:

- ambient access to `~/.vz` VM and image roots;
- `-vol`, shared-folder, `-usb`, ISO/IPSW, and artifact paths supplied on the
  command line;
- Unix control sockets and helper sockets outside the app container;
- listener surfaces: HTTP API, VNC, debug stub, port forwards, proxy runtime;
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

## Queued commits

1. Done: add `internal/autosign/app_sandbox.entitlements`.
2. Done: add an opt-in App Sandbox smoke harness that builds, signs, and runs
   non-mutating commands (`--version`, `help`, `list`) while recording traps or
   sandbox denials.
3. Add host-process App Sandbox detection to `security status` or
   `doctor host`, reported separately from cove's guest sandbox level.
4. Make elevation paths fail closed when App Sandbox is detected.
5. Document supported package shapes and the exact proof gates before any
   "full sandbox" product claim.

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
```

Stop before disk resize, provisioning mutation, helper install, or shared-folder
mutation until startup, state-dir, control-socket, and listener behavior are
understood and documented.
