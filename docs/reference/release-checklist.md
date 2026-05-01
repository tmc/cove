---
title: Release Checklist
---
# Release Checklist

Use this checklist for production tags. The historical
`docs/v0.1.0-publish-checklist.md` records the first release; this page is the
current runbook.

## Preflight

```bash
git fetch origin main --tags
git status --short --branch
git log --oneline origin/main..HEAD
```

Expect a clean worktree and no unintended commits ahead of `origin/main`.

Required local tools:

```bash
go version
codesign -h >/dev/null
goreleaser --version
```

Install missing release tooling with:

```bash
brew install goreleaser
```

## Quality Gates

```bash
go test ./...
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
codesign -dv --entitlements - ./cove
make release-check
```

The signed local binary must include `com.apple.security.virtualization`.
`make release-check` runs short tests, `go vet`, and a GoReleaser snapshot.

For `cove build`, verify the production boundary before tagging:

```bash
./cove help build
./cove build --base ghcr.io/acme/base@sha256:base --script missing.vzscript vm
./cove build --base ghcr.io/acme/base@sha256:base --script missing.vzscript --tag ghcr.io/acme/vm:test --push vm
./cove build --base ghcr.io/acme/base@sha256:base --script missing.vzscript --push vm
./cove build vm --base ghcr.io/acme/base@sha256:base --script missing.vzscript --cache-from ghcr.io/acme/cache:build --dry-run
```

The second command must fail before script loading with:

```text
cove build: non-dry-run requires local VM base directory
```

The third command must fail before script loading with:

```text
cove build: non-dry-run requires local VM base directory
```

The fourth command must fail before script loading with:

```text
cove build: --push requires at least one --tag
```

The fifth command must fail before script loading with:

```text
cove build: --cache-from registry cache is not implemented yet
```

Then verify local-base execution with a disposable VM directory:

- Run one tiny recipe against a local VM base with `--compact fast`.
- Repeat the same command and confirm `cache hits: 1/1`.
- Run one tiny recipe with `# cache-ttl: 1s`, wait at least two seconds, repeat
  it, and confirm the expired entry is treated as a cache miss.
- Run the same tiny recipe with `--compact targeted` and `--compact thorough`
  on disposable copies of the base, confirming the build reaches `Build
  complete`.
- Run a recipe declaring `# secret: COVE_RELEASE_MISSING_SECRET` with that
  environment variable unset, and confirm it fails before guest start and writes
  no cache entry.
- Run a Linux guest recipe declaring a present `# secret:` while guest swap is
  active, and confirm the build fails before the script body runs.
- Run `cove push <reported-final-vm-dir> <ref> --dry-run` and confirm it plans
  from the build output directory.
- Run a local-base build with `--tag <ref> --push` against a disposable registry
  target and confirm the pushed tag matches the reported final VM directory.

## Live build smoke (P0)

These checks were exercised during the v0.3 RC live-build smoke. Each
sub-section gives the literal command, the expected outcome, and (where
relevant) the verification recipe. Run all of them against a real local VM
base before tagging.

### a. Local-base non-dry-run build (cold cache)

```bash
./cove build <name> --base ~/.vz/vms/<existing-base> --script <tiny.vzscript>
```

Expected:

- exit 0
- wall time roughly 1-2 minutes against a 64 GB base
- final summary contains `cache hits: 0/1`
- result VM dir at `~/.vz/build-scratch/<id>/` containing `disk.img`,
  `aux.img`, `hw.model`, `machine.id` (plus `config.json`, `mac.address`,
  `build.json`)

### b. Second-run cache hit

```bash
./cove build <name> --base ~/.vz/vms/<existing-base> --script <tiny.vzscript>
```

Repeat (a) unchanged. Expected:

- exit 0
- dramatic speedup vs cold (observed ~30-60s vs ~1m25s)
- final summary contains `cache hits: 1/1`
- NO `> guest-exec` lines in the output (script body is not executed)
- NO `Starting virtual machine...` line (no guest boot)

### c. Failed-script cleanup does not poison the cache

Author a vzscript with a guaranteed-failing step, e.g.:

```text
# rc-fail
# compact: targeted

guest-exec sh -lc 'false'
```

Snapshot the cache, run the build, snapshot again:

```bash
find ~/.vz/store -type f | sort > /tmp/cache.before
./cove build <name> --base ~/.vz/vms/<existing-base> --script <fail.vzscript>
find ~/.vz/store -type f | sort > /tmp/cache.after
diff /tmp/cache.before /tmp/cache.after
```

Expected:

- nonzero exit, error mentions the failing step name and the offending
  vzscript line
- `diff` is empty (zero new files in `~/.vz/store/`)
- `find ~/.vz/store -type f | wc -l` before vs after must match
- scratch dir cleaned up by default

### d. `--keep-intermediate=true` on failure preserves scratch

```bash
./cove build <name> --base ~/.vz/vms/<existing-base> \
    --script <fail.vzscript> --keep-intermediate=true
```

Expected:

- nonzero exit
- error message contains the literal scratch path, e.g.
  `scratch kept at /Users/<you>/.vz/build-scratch/<id>`
- the named scratch directory still exists and contains `aux.img`,
  `build.json`, `build.pid`, `config.json`, `disk.img`, `hw.model`,
  `mac.address`, `machine.id`
- still zero new entries in `~/.vz/store/`

Cleanup hint after inspection:

```bash
rm -rf ~/.vz/build-scratch/<id>
```

### e. SIGINT mid-build preserves cache integrity

Kick off a slow vzscript (`guest-exec sleep 90` or similar), then send
SIGINT after about 10 seconds:

```bash
./cove build <name> --base ~/.vz/vms/<existing-base> \
    --script <slow.vzscript> &
COVE_PID=$!
sleep 10
kill -INT "$COVE_PID"
wait "$COVE_PID" || true
find ~/.vz/store -type f | sort > /tmp/cache.after-sigint
diff /tmp/cache.before /tmp/cache.after-sigint
```

Expected:

- the process exits
- `diff` is empty: zero new cache entries (cache integrity preserved)
- the abandoned scratch dir is GC'd by the next `cove build` invocation
  via `gcBuildScratch`

Note: SIGINT currently exits silently without printing a
context-cancellation-shaped error. Tracked as polish, not an RC blocker.

### f. Secret + compaction combination

Author a vzscript that declares a secret and a compaction mode, e.g.:

```text
# rc-secret
# secret: RC_TEST_SECRET
# compact: targeted

guest-exec sh -lc 'wc -c /tmp/cove-secrets/RC_TEST_SECRET'
```

Run it with the secret set in the environment:

```bash
RC_TEST_SECRET=hunter2-rc-XYZ ./cove build <name> \
    --base ~/.vz/vms/<existing-base> \
    --script <secret+compact.vzscript>
```

Expected:

- exit 0; `Build complete` summary
- `/tmp/cove-secrets/<NAME>` is present in the guest as a 0600 file on a
  RAM-disk-backed APFS volume (macOS) or tmpfs (Linux)
- the RAM disk / tmpfs is detached automatically after the script body

The literal secret value MUST NOT appear in any persistent on-disk
artifact. Verify with:

```bash
grep -r "hunter2-rc-XYZ" ~/.vz/store/                              # 0 hits
grep -r "hunter2-rc-XYZ" ~/.vz/build-scratch/<id>/build.json       # 0 hits
strings ~/.vz/build-scratch/<id>/disk.img | grep "hunter2-rc-XYZ"  # 0 hits
```

All three must report zero matches.

### g. Signed cove binary check

```bash
codesign -d --entitlements - ./cove 2>&1 \
    | grep -E 'com.apple.security.virtualization|com.apple.vm'
```

Expected: at least one match line containing
`com.apple.security.virtualization`.

If no match, re-sign:

```bash
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

### Known limitations gates

These behaviours were observed during RC smoke and must either be fixed
or explicitly gated/documented before tag:

- `--compact thorough` is currently broken on macOS guests:
  `compact.go:121` runs `diskutil secureErase freespace 0 /`, but `/` is
  the read-only System volume on macOS 11+. For RC, either fix the
  command to target `/System/Volumes/Data`, gate with an explicit
  pre-flight error, or document the mode as Linux-guest only.
- SIGINT exits silently rather than printing a context-cancellation
  error. Cache integrity is preserved (see (e) above), and the abandoned
  scratch is GC'd by the next build, but the missing diagnostic is worth
  a release-notes mention. Polish, not an RC blocker.

## Docs Gates

```bash
go test . -run 'TestBuildCLIDocs|TestBuildDumpDocs'
./cove dump-docs -type cli -pretty >/tmp/cove-cli-docs.json
```

Check that:

- `docs/reference/changelog.md` describes the release surface, including the
  canonical "what ships / what's deferred" boundary below.
- `docs/reference/cli.md` matches the command help.
- `docs/designs/ROADMAP.md` does not mark unfinished work as shipped.
- `docs/designs/016-notebooklm-roadmap-refresh-2026-04-30.md` and
  `docs/designs/017-v03-execution-roadmap.md` agree with this checklist on the
  deferred-items list.
- Public docs say non-dry-run `cove build` requires a local VM base directory
  for execution, registry bases remain planning-only, build output can be
  handed to `cove push`, and `--push` requires at least one output tag.

The canonical deferred-items list (must appear consistently across the CLI
reference, changelog, roadmap, 016 refresh, and 017 execution roadmap):

- Registry-base `cove build` execution. Non-dry-run requires a local VM
  directory base.
- Registry cache import/export (`--cache-from`, `--cache-to`). Reserved and
  rejected before planning.
- Public curated `cove` image registry and signed agentkit image channels.
- External secret stores (1Password, Vault, SOPS, age). v0.3 secrets are host
  environment variables mounted through tmpfs only.
- BuildKit-style parallel step execution. v0.3 builds run sequentially.
- Packer plugin shim.
- Product-name resolution before any public registry or signed channel.

## Tag And Publish

```bash
git push origin main
git tag -s vX.Y.Z -m "cove vX.Y.Z"
git push origin vX.Y.Z
goreleaser release --clean
```

After publish:

```bash
gh release view vX.Y.Z
brew update
brew install tmc/tap/cove
cove version
```

## Rollback

If the release is published with a bad artifact, remove the GitHub release
before deleting or moving the tag:

```bash
gh release delete vX.Y.Z --yes
git push origin :refs/tags/vX.Y.Z
git tag -d vX.Y.Z
```

Do not reuse a tag after public artifacts or Homebrew checksums have propagated;
cut a patch release instead.
