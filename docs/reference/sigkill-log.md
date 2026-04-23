# Helper SIGKILL Log

This log tracks host-side helper processes that exited due to `SIGKILL`
during VM provisioning and control flows. These are not guest process kills.

## 2026-04-21

### Short-lived helper commands killed while VM stayed healthy

Observed during a headed run of `hermes-mlx-go-60g-v3`.

Commands that were seen to fail intermittently with `signal: killed` or noisy
`macgo`/autosign stderr before producing a useful result:

- `./cove ctl -vm hermes-mlx-go-60g-v3 ready`
- `./cove ctl -vm hermes-mlx-go-60g-v3 agent-exec ...`
- `./cove vzscript run -vm hermes-mlx-go-60g-v3 hermes-agent`
- `./cove vzscript run -vm hermes-mlx-go-60g-v3 mlx-go`

Commands that continued to work against the same running VM:

- `./cove ctl -vm hermes-mlx-go-60g-v3 status`
- `./cove ctl -vm hermes-mlx-go-60g-v3 gui status`
- `./cove ctl -vm hermes-mlx-go-60g-v3 screenshot ...`
- `./cove ctl -vm hermes-mlx-go-60g-v3 agent-status`

Current workaround:

- `VZMAC_NO_MACGO=1 go run . ctl ...`
- `VZMAC_NO_MACGO=1 go run . vzscript run ...`

Notes:

- The long-lived `./cove up ...` VM runtime remained alive and the guest stayed
  usable while these helper failures occurred.
- The workaround points at a host-side helper startup problem, likely around
  `macgo` initialization or the short-lived helper execution path, not a guest
  failure.
- The workaround is diagnostic only. It should not become the intended steady
  state.

Next checks:

- capture stderr and exit metadata for each killed helper path
- compare `./cove ctl ...` against `go run . ctl ...` under the same VM
- isolate `macgo` setup for non-UI helper commands
- test whether the kill is tied to autosign, bundle startup, or process-group
  cleanup

### Root cause confirmed

Crash reports showed `SIGKILL (Code Signature Invalid)` with
`termination.namespace = CODESIGNING` on short-lived `cove` and `covectl`
launches.

Local repro confirmed that a plain helper command such as
`./cove ctl ... status` could rewrite the on-disk `./cove` binary, changing
its inode and hash and leaving `codesign -vvv ./cove` reporting an invalid
signature.

The failing path was `initMacgo()` enabling macgo single-process launch for
all real commands. That launcher re-signed the current executable in place on
every launch.

### Mitigation in tree

- default bootstrap now uses `autosign` only
- macgo is opt-in via `VZMAC_ENABLE_MACGO=1`
- helper commands no longer pay the self-rewrite path by default
- regression test `TestHelperCommandDoesNotRewriteBinary` protects against
  helper subcommands mutating the executable

## 2026-04-22

### Runtime launcher boundary clarified

Follow-up launcher work confirmed two separate behaviors:

- plain signed runtime paths work without mutating `./cove` in place
- bundled macgo runtime paths still are not reliable in the current purego
  AppKit runtime

Observed failure on the bundled headless path:

- `./cove -vm ... -headless run` relaunched through `cove.app`
- the child process trapped in `VZVirtualMachineView.SetVirtualMachine`
- the trap reproduced both when the detached view was created after VM start
  and when it was created before VM start

Observed failure on the bundled headed path:

- `./cove -vm ... run` relaunched through `cove.app`
- AppKit raised `NSInternalInconsistencyException`
- reason: setting the main menu on a non-main thread during `FinishLaunching`

Current mitigation in tree:

- macgo remains opt-in only for headed UI experiments
- runtime commands stay on the plain signed binary path by default
- helper commands still avoid macgo entirely

Related follow-up:

- headless AppKit work now uses the repo-local UI-thread executor instead of
  `dispatch_get_main_queue()` assumptions, which restored stable `gui open`
  and screenshot operations on the plain headless runtime path
