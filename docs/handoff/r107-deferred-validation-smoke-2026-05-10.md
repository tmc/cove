# R107 deferred validation smoke

Date: 2026-05-10

Base: `b305c86` (`origin/main`)

Dirty worktree note: pre-existing edits in `agent_control.go`,
`agent_control_test.go`, and `ctl.go` were left untouched.

## Lume GHCR pull validation

Built and signed a throwaway binary:

```
go build -o /tmp/cove-r107 .
codesign -s - -f --entitlements internal/autosign/vz.entitlements /tmp/cove-r107
```

Smoke command:

```
/tmp/cove-r107 pull --dry-run --as r107-lume-ghcr-smoke \
  docker://ghcr.io/trycua/ubuntu-noble-vanilla:latest
```

Output:

```
Pull dry run
  ref: ghcr.io/trycua/ubuntu-noble-vanilla:latest
  vm: r107-lume-ghcr-smoke
  target: /Users/tmc/.vz/vms/r107-lume-ghcr-smoke
  manifest digest: sha256:a78ef799209b072757f5762e1288655336b2ac72e1ff699253b50e538d423e9e
  format: lume (tar-split)
  disk parts: 41
  compressed bytes: 20.0 GB
  nvram.bin: 128.0 KB
  config.json: 159 B
```

Result: PASS for live GHCR manifest fetch, `docker://` reference parsing, and
Lume tar-split dispatch. This was intentionally `--dry-run`; it did not fetch
disk blobs, import the VM, or boot it.

Post-check:

```
test ! -e /Users/tmc/.vz/vms/r107-lume-ghcr-smoke
```

The post-check passed, so the dry-run did not create the target VM directory.

## Clean-host install smoke

Not run. The requested fallback requires a clean-host install under
`VZ_DEBUG_INSTALL=1`; this checkout already has active local `~/.vz` state and
the Lume GHCR smoke produced a safe live result without touching it.

Command to run on an isolated host:

```
VZ_DEBUG_INSTALL=1 ./cove up -user smoketest -password smokepass123 \
  -vm r107-cleanhost-smoketest -ipsw ~/.vz/cache/RestoreImage.ipsw \
  -headless -disk-size 48 -no-shutdown
```
