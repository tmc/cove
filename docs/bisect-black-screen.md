# Bisecting the Black-Screen Regression

Use this workflow when `cove` launches a VM window but the guest display stays black.

## Scope

The most likely history range is:

- bad: `b42b100` (`improve selector state and startup errors`)
- first likely good probe: `934670c` (`update VM core files for apple API renames and dispatch.Queue typing`)
- fallback lower bound if `934670c` is already bad: `bb64936` (`add core VM infrastructure`)

`934670c` is the best first probe because it is the largest committed change to the GUI display path: queue wiring, `VZVirtualMachineView` bridging, and app activation/run-loop behavior.

## Helper

Use `tools/bisect-black-screen.sh`. It:

- launches through `go run .` so macgo stays in the path
- deletes `suspend.vmstate` and `suspend.config.json` before each run
- refuses to run if the VM control socket is already active
- prompts for `good`, `bad`, `skip`, or `quit` after the app exits

Because `git bisect` checks out older commits, copy the helper outside the worktree before you start:

```bash
cp ./tools/bisect-black-screen.sh /tmp/cove-bisect-black-screen.sh
chmod +x /tmp/cove-bisect-black-screen.sh
```

## Commands

Validate a good endpoint first:

```bash
git checkout 934670c
/tmp/cove-bisect-black-screen.sh -vm codex-e2e
```

If `934670c` is already bad, test `bb64936` the same way and use that as the good endpoint instead.

Then run the bisect:

```bash
git bisect start
git bisect bad b42b100
git bisect good 934670c
git bisect run /tmp/cove-bisect-black-screen.sh -vm codex-e2e
```

If you need to exercise the selector path instead of the direct run path:

```bash
git bisect run /tmp/cove-bisect-black-screen.sh -vm codex-e2e -mode selector
```

## Experiment matrix (launch/config)

To compare launch behavior and runtime device profile explicitly, run:

```bash
/tmp/cove-bisect-black-screen.sh -vm codex-e2e -launch-order window-first -runtime-profile full
/tmp/cove-bisect-black-screen.sh -vm codex-e2e -launch-order start-first -runtime-profile full
/tmp/cove-bisect-black-screen.sh -vm codex-e2e -launch-order window-first -runtime-profile minimal
/tmp/cove-bisect-black-screen.sh -vm codex-e2e -launch-order start-first -runtime-profile minimal
```

For Apple unified logs during each run, pass through:

```bash
/tmp/cove-bisect-black-screen.sh -vm codex-e2e -launch-order start-first -runtime-profile minimal -- -apple-log -verbose
```

Interpretation:

- if `start-first` fixes black screen while `window-first` fails, startup ordering is the likely culprit
- if `minimal` fixes black screen while `full` fails, one of the optional runtime devices is the likely culprit
- if only `start-first + minimal` works, both factors interact

When finished:

```bash
git bisect reset
```
