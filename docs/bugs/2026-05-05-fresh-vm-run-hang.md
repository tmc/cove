# Fresh VM run hang after Configuring VM

User report: after `cove -vm mlxgo-fresh-nodev-20260505 provision-agent`
ended with `You must be root for this command`, a fresh
`cove -vm mlxgo-fresh-nodev-20260505 run -headless -memory 8 -cpu 4`
printed `Configuring VM: 4 CPUs, 8 GB RAM` and made no visible progress for
more than two minutes. A parallel `cove list` reported the VM as `stopped`
while that `cove run` process was still alive.

## Code path

The macOS run path is:

1. `RunVMWithConfig` takes `<vmDir>/run.lock` before calling `runMacOSVM`.
2. `runMacOSVM` validates the VM, ensures the disk is detached, then prints
   `Configuring VM: %d CPUs, %d GB RAM`.
3. It calls `buildVMConfiguration`, creates the dispatch queue, creates the
   `VZVirtualMachine`, and delegates to `startVMWithQueue`.
4. `startVMWithQueue` prints `Starting virtual machine...`, calls
   `beginVMStart`, and waits in `waitForVMStart`.
5. `beginVMStart` dispatches the actual `vm.StartWithCompletionHandler` call
   onto the VM queue.

Before this fix, `waitForVMStart` used a fixed 30 second timer and returned
`vm start timed out` without detailed state. It also depended on successful
state polling from the VM dispatch queue, which means a blocked queue could
hide the last useful state from the operator.

## State visibility

`cove list` uses `vmconfig.List(detectVMState)`. `detectVMState` currently
does only:

1. connect to the VM control socket and report `running` if it succeeds;
2. report `suspended` if `suspend.vmstate` exists;
3. otherwise report `stopped`.

No `state`, `runtime.json`, or `last_state` file is written during startup.
That means the interval after `run.lock` is taken but before the control socket
starts is invisible to other processes. A separate `cove list` sees stale
`stopped` even when a live `cove run` process is configuring or starting the VM.

## Reproduction

The specific QA VM was not available in this worktree. The minimal code-level
reproduction is a VM directory with a held `run.lock` and no control socket:
before this fix, `detectVMState` returned `stopped` because it ignored the
lock-backed startup intent. That reproduces Issue B without needing a live VZ
guest.

Issue A is addressed from the user-facing path: add a configurable
`-start-timeout` watchdog and include the last observed VM state and startup
diagnostics in the error. This does not change boot semantics; it prevents an
unbounded operator hang and gives the next debug session useful facts.

The `provision-agent` `You must be root for this command` result is adjacent
only. This work does not change that sudo-required path.
