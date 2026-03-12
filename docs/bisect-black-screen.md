# Bisecting the Black-Screen Regression

Use this workflow when `vz-macos` launches a VM window but the guest display stays black.

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
cp ./tools/bisect-black-screen.sh /tmp/vz-macos-bisect-black-screen.sh
chmod +x /tmp/vz-macos-bisect-black-screen.sh
```

## Commands

Validate a good endpoint first:

```bash
git checkout 934670c
/tmp/vz-macos-bisect-black-screen.sh -vm codex-e2e
```

If `934670c` is already bad, test `bb64936` the same way and use that as the good endpoint instead.

Then run the bisect:

```bash
git bisect start
git bisect bad b42b100
git bisect good 934670c
git bisect run /tmp/vz-macos-bisect-black-screen.sh -vm codex-e2e
```

If you need to exercise the selector path instead of the direct run path:

```bash
git bisect run /tmp/vz-macos-bisect-black-screen.sh -vm codex-e2e -mode selector
```

When finished:

```bash
git bisect reset
```
