# Cove usability snags observed while provisioning an MLX-LM VM

Observed on 2026-05-04 while running:

```bash
./cove up -vm mlx-lm -user cove -password cove -vzscripts mlx-lm \
  -no-shutdown -headless -cpu 8 -memory 16 -disk-size 128
```

Goal: deploy a fresh macOS VM with the built-in `mlx-lm` vzscript and leave it
running for model inference.

## Fresh `up` shows as an orphan during IPSW download

While `cove up` is still downloading `~/.vz/cache/RestoreImage.ipsw`, the target
VM directory already exists but has no disk:

```text
No VMs found. Run 'cove install' to create one.

Orphans (missing disk image):
  default    (orphan: missing disk)
  mlx-lm     (orphan: missing disk)
```

That output is technically accurate but misleading during an active install. It
looks like the VM is broken even though `cove up` is simply still in the
download phase.

Potential improvement: mark in-progress installs with a sentinel file or
operation record, and have `cove list` render `mlx-lm (installing: downloading
IPSW)` instead of grouping it with stale orphans.

## Long IPSW downloads lack a reconnectable operation view

The fresh macOS path immediately blocks on a 15.7 GB Apple restore image
download. The terminal progress bar is useful, and the download is resumable,
but once the command is moved into another terminal session there is no first
class way to ask cove:

- which operation is active,
- what phase it is in,
- how much of the IPSW has downloaded,
- what command should be resumed if the process dies.

In this run, the only durable progress check was inspecting
`~/.vz/cache/RestoreImage.ipsw` with `du`/`ls` and finding the `curl` child with
`pgrep`.

Potential improvement: expose fresh installs through the existing operation
surface, or add a lightweight `cove operations list/status` path for CLI-driven
installs. At minimum, print the resumable command and cache path prominently
before starting the download.

## Moving a running operation into iTerm2 is awkward

The requested workflow was to run the long operation in an iTerm2 horizontal
split. A process already started from a non-PTY tool session could not be moved
into iTerm2, so the workflow required:

1. stop the parent `cove up` process,
2. stop the child `curl` process,
3. rely on the partial IPSW for resume,
4. create an iTerm2 split with AppleScript,
5. restart the same `cove up` command in that split.

The first iTerm2 AppleScript attempt created or targeted a session but did not
leave a running cove process. The reliable pattern was to create the horizontal
split first and then write the command into the returned session.

Potential improvement: document an `it2` helper recipe or small local helper
for opening long-running cove operations in an iTerm2 split. More importantly,
if cove install operations are resumable and observable, moving the UI surface
becomes less risky.

## Disk-size failures arrive after the IPSW wait

The first attempt used `-disk-size 128` on a host with about 66.8 GB free. Cove
did not discover the disk-size problem until after the 15.7 GB IPSW download
had completed and the installer began configuring the VM:

```text
error: install: setup configuration: block device config:
insufficient disk space: need 128.0 GB, have 66.8 GB available
```

The retry with `-disk-size 48` proceeded to actual installation.

Potential improvement: check requested disk size and available space before
starting the IPSW download. If the IPSW is not already cached, account for both
the IPSW cache requirement and the requested VM disk so the user gets one early
capacity error instead of a late failure.

## Headless `up` can still block on host GUI authorization

The 48 GB retry completed macOS installation and then stopped at offline disk
provisioning:

```text
Waiting for macOS admin-password dialog approval...
```

This came from the host-side SecurityAgent authorization prompt while the
command itself was running in a headless VM workflow inside an iTerm2 split.
Until the GUI prompt is approved, the CLI appears stalled unless the user knows
to look for the macOS password dialog.

Potential improvement: print a more explicit line before invoking
Authorization Services, for example: "Approve the macOS administrator password
dialog on the host to continue provisioning <vm>." For non-interactive or
headless runs, consider an earlier preflight warning that provisioning will
still require host GUI authorization unless an alternate privileged helper path
is selected.

## Built-in `mlx-lm` recipe depends on a preboot mount

The built-in `mlx-lm` recipe declares:

```text
# mount: ~/.cache/huggingface ro
```

That is the right behavior for reusing local model cache, but live hot-add is
not supported. If a user runs `cove vzscript run mlx-lm` against an already
booted VM that was not started with the mount, the recipe can still install
`mlx-lm`, but cached models will not be visible at the expected guest mount
path.

Potential improvement: when a vzscript declares mounts and the target VM is
already running, warn that the mount requires a reboot unless the current
runtime configuration already includes it.

## Long or quiet agent execs can leave guest work running after host timeout

After `mlx-lm` installed, direct `agent-exec` and `agent-exec-stream` checks
that touched the Hugging Face cache or loaded a model hit:

```text
error: receive: read unix ->/Users/tmc2/.vz/vms/mlx-lm/control.sock: i/o timeout
```

The timeout was host-side. The guest command kept running after the host-side
read failed, leaving stale shell, `head`, and `mlx_lm.generate` processes that
had to be killed manually through a later agent call.

The workaround was to run long MLX commands file-backed and detached in the
guest (`nohup ... >/tmp/... 2>&1 &`), then poll result files with short
`agent-exec` calls. That worked for both `mlx_lm.generate` and
`mlx_lm.server` endpoint verification.

Potential improvement: give `agent-exec`/`agent-exec-stream` an explicit
timeout flag and define cancellation semantics. If the host read times out, the
CLI should either keep the stream attached, tell the user the guest process is
still running with its PID, or terminate the remote process group.

## VM shared-folder hot-add worked, but the success path is hard to audit

Adding the benchmark source tree while `mlx-lm` was running worked:

```bash
./cove -vm mlx-lm shared-folder add /Users/tmc2/go/src/github.com/tmc tmc rw
```

The output correctly reported that the folder was applied to the running VM and
mounted at `/Volumes/My Shared Files/tmc`. For a benchmark workflow, the next
manual step was still to run a separate guest command to prove the intended
repository was visible at that path.

Potential improvement: after a successful live mount, optionally print a
copy-pastable verification command or include a one-line guest-side stat of the
mount root. That would make "source tree is mounted and ready" easier to trust
before starting a long guest job.

## Benchmark-style guest jobs need a first-class detached mode

The reliable pattern for installing guest tools and running `mlx-go-benchmarks`
was to create guest scripts under `/tmp`, launch them with `nohup`, write output
to a shared file, and poll with short `agent-exec` calls. This avoids the
control-socket timeout issue, but it is awkward for a normal CLI workflow.

Potential improvement: add an `agent-exec --detach --stdout <path> --stderr
<path>` mode that returns a guest PID and a status handle. A matching
`agent-job status/log/wait` command would fit long benchmark and model-download
workflows without requiring users to hand-roll PID files and polling scripts.

## Nested shell quoting can silently corrupt guest scripts

For the full benchmark sweep, a long guest-side script was initially written
through a nested `agent-exec /bin/bash -lc 'cat > ... <<EOF ... EOF'` command.
The command returned successfully, but the resulting guest script was truncated
and had malformed quoting around Python snippets:

```text
"$REPO/benchmarks/python-mlx/.venv/bin/python3" -c "import mlx, mlx_lm; print(python-mlx, mlx.__version__, mlx_lm.__version__)"
```

The safe workaround was to write the script on the host, base64-encode it, and
decode it in the guest before launching it.

Potential improvement: provide a file transfer helper for `agent-exec` workflows
or document a robust `base64 | agent-exec base64 -D` recipe for multi-line guest
scripts. If Cove already has a copy primitive, it should be discoverable near
the agent execution commands.

## Shared mounts make benchmark setup dirty on the host

Installing benchmark dependencies inside the mounted repository created host
worktree changes for guest-only artifacts, especially Python virtualenvs under:

```text
benchmarks/python-mlx/.venv
benchmarks/vllm-mlx/.venv
```

That is convenient for reusing setup across VM runs, but surprising when the
goal is to keep the host checkout clean and only preserve benchmark results.

Potential improvement: benchmark vzscripts or docs should steer dependency
state into a guest-local path by default, with only results written to the
shared mount. A warning before creating large dependency trees under a shared
folder would also help.

## Guest long-running child processes can survive attempted cancellation

When a `vllm-mlx` benchmark hung during server startup, killing the parent
`go test` process did not immediately clear all child `bench.py` processes. New
prompt subprocesses continued briefly under the guest before a broader
`pkill -f benchmarks/vllm-mlx` cleanup.

Potential improvement: if Cove adds detached job support, job cancellation
should terminate the full guest process group, not only the first shell process.
For current `agent-exec` behavior, the CLI should document whether commands are
started in a killable process group and how users should clean up descendants.

## Host and guest benchmark output required manual normalization

The VM benchmark output used guest-specific paths and a different benchmark
suffix:

```text
BenchmarkDecode//Volumes/My_Shared_Files/.../short-8
```

The host output used:

```text
BenchmarkDecode//Users/tmc2/.cache/.../short-14
```

That is expected Go benchmark behavior, but it meant `benchstat` initially
rendered host and VM as separate benchmark sets instead of a comparison. The
workaround was to normalize the model path and strip the GOMAXPROCS suffix
before running `benchstat`.

Potential improvement: provide or document a small benchmark normalization
helper for Cove VM comparison runs, especially when paths intentionally differ
between host and guest.

## Shared-folder source trees can stall Go and Python benchmark commands

Several commands that should have been quick stalled when run directly from the
VirtioFS mount, including `go list ./benchmarks/llamafile`, `go test` for
individual benchmark packages, and Python probes that touched a mounted
`.venv`. The same commands completed quickly after copying the source tree and
model artifacts into guest-local paths with `agent-cp`.

The practical workaround was to keep the host checkout as the edit source, copy
a small code archive into `/Users/cove/work/mlx-go-benchmarks`, copy the GGUF
model into `/Users/cove/models/gguf`, and point Python benchmarks at guest-local
virtualenvs.

Potential improvement: Cove should document when shared folders are suitable
for source execution versus artifact exchange, and expose a "copy tree to
guest, excluding patterns" helper. The existing `agent-cp` is useful, but only
after discovering it in `ctl --help`.

## `agent-cp` should make ownership and overwrite behavior clearer

Copying a host archive into the guest with `agent-cp` worked well and avoided
shared-folder hangs, but the destination file was owned by `root`:

```text
-rw-r--r--  1 root  staff   7.0M ... /Users/cove/work/mlx-go-benchmarks-code.tar.gz
```

That was harmless for extraction because it was world-readable, but surprising
inside the user workspace. I also accidentally launched cleanup and `agent-cp`
in parallel once; the copy command reported success, but the cleanup race
removed the file immediately afterward.

Potential improvement: `agent-cp` could default copied files to the target user
when writing into that user's home directory, and its success message could
include final owner/mode metadata. For common workflows, a non-racy
`agent-cp --mkdirs --replace` mode would reduce shell choreography.

## Hugging Face cache access can block unrelated guest inspection commands

During the `vllm-mlx` rerun, the default Hugging Face cache under
`/Users/cove/.cache/huggingface/hub` became slow enough that simple `find` and
`ls` probes through `agent-exec` hit the Cove control-socket timeout. Retrying
the benchmark with a fresh guest-local `HF_HOME=/Users/cove/hf-vllm` let the
model download and server startup complete.

Potential improvement: long model-download workflows would benefit from
examples that set explicit guest-local cache paths and from Cove-side status
commands that avoid recursively touching busy cache directories.
