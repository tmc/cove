# Native iOS probe and disposable smoke harness

Observed September 12, 2026 UTC on Mac16,8, arm64, 48 GiB RAM,
macOS 27.0 build 26A5425a. No iOS VM was created or started. No DFU device or
guest boot was verified. No simulator, host security changes, firmware download,
or modification of existing VM state was used.

## Observed prerequisites

The public-entitlement executable passed `codesign --verify --strict`. Running
`cove ios probe` found all ten checked selectors with the expected ABI encodings.
The PV3 descriptor/model constructor returned an object, but its `IsSupported`
result was false. The command exited 1 with:

```text
ios research hardware model unsupported on this host
```

A separate copy, signed with `internal/autosign/vz-research.entitlements`, also
passed static signature verification. Executing the same probe terminated with
SIGKILL before it emitted JSON. The host's `amfid` log recorded:

```text
The file is adhoc signed but contains restricted entitlements
```

Thus static codesign verification does not establish permission to execute the
research-signed process. The research-entitled framework support check could not
be evaluated. These results do not determine whether enabling research guests
alone would suffice, and no security policy was changed to test that possibility.
The sanitized [probe evidence](ios-native-smoke-2026-09-12.json) records binary
hashes, signature profiles, selectors and exit outcomes.

Read-only inspection covered `~/.vz`, `~/Downloads`, and the local vphone-cli
checkout. No prepared iOS bundle, SEP state, or boot ROM set was found there.
The upstream default `~/.vphone` directory did not exist, and no `VPHONE_ROOT`
or `VPHONE_LIBRARY_ROOT` override was set in this session. The cached
`~/.vz/cache/RestoreImage.ipsw` is macOS 26.6.2 build 25G83 (Mac product types),
not a prepared iOS research image. The pinned vphone source checkout exists at
revision `87f796c62a7cb385cd37afce121f6e222d83e5b5`, but source alone is not firmware.

Live verification therefore needs an executable permitted to use the research
APIs and a complete prepared consistency set: config, PV3 model, machine identity,
auxiliary storage, SEP storage, disk, boot ROM, and optional SEP ROM. DFU acceptance
also needs mode-aware discovery targeted at that identity's ECID. Neither a VZ
running state nor USB snapshot alone establishes DFU or guest boot.

## Reproduce inspection

From the repository root:

```sh
go build -o /tmp/cove-ios-probe ./cmd/cove
codesign -s - -f --entitlements cmd/cove/vz.entitlements /tmp/cove-ios-probe
python3 scripts/ios-smoke.py --binary /tmp/cove-ios-probe --output /tmp/ios-probe-evidence
```

The output directory must be new. The harness records executable SHA-256,
signature verification, entitlements, OS/hardware, native probe output, USB
snapshot, and specific blockers in `report.json`. Inspection without `--bundle`
records the missing prepared bundle and exits 1. It starts no VM.

To repeat the separate research-signature observation without altering the public
binary or host settings:

```sh
cp /tmp/cove-ios-probe /tmp/cove-ios-research-probe
codesign -s - -f --entitlements internal/autosign/vz-research.entitlements /tmp/cove-ios-research-probe
python3 scripts/ios-smoke.py --binary /tmp/cove-ios-research-probe --output /tmp/ios-research-evidence
```

## Opt in to a disposable run

Once the host probe and prepared-bundle validation pass:

```sh
python3 scripts/ios-smoke.py --binary /path/to/signed/cove --bundle /path/to/stopped.covevm --output /tmp/ios-dfu-evidence --run --mode dfu
```

Use `--mode normal` for a separate normal-start attempt. The harness checks an
existing source `run.lock` when present and verifies source hashes across copying
and execution. Keep the source stopped throughout; the harness does not create a
lock in a source that has none. It copies only required artifacts using macOS
`cp -c`, verifies independent inodes and matching hashes, and launches only the
fresh `disposable.covevm` under the evidence directory. The source is never passed
to the runtime as its target. Output inside the source bundle is rejected.

After the observation interval (default 20 seconds), the harness sends SIGTERM,
waits up to 45 seconds, then kills and reaps an unresponsive process. It retains
the copied state and runtime logs for inspection. A forced kill or failed runtime
is reported as a blocker. `nativeRunningObserved` requires the runtime's observed
VZ-running report; `dfuObserved` remains unknown and `guestBootVerified` remains
false. USB snapshots are supplementary evidence only.

## Reproduced runtime defects and regression checks

The old iOS startup path installed signal handling but did not consume signals
until the generic startup waiter returned. It could swallow SIGTERM for the full
start timeout. A nil start callback also caused a running report without checking
VM state, and synchronous shutdown waiting prevented run-loop pumping while
waiting for a callback.

The iOS lifecycle now handles cancellation on the run-loop thread, requires an
observed running state, and waits asynchronously for confirmed stopped state.
Tests exercise a one-hour pending startup cancelled in under one second, cleanup
exactly once, callback success without running state, and stopped state despite a
missing stop callback. Individual native state probes still have the shared
five-second wait bound; the outer deadline cannot preempt a probe in progress.

```sh
go test ./cmd/cove -run 'TestIOS' -count=1
python3 -m unittest discover -s scripts -p ios_smoke_test.py
```

The harness tests use fake processes and synthetic files. They verify copy
independence, source-path rejection, disposable dispatch, and process reaping;
they are not native boot evidence.
