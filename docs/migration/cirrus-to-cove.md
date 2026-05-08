# Cirrus to cove

Cirrus CI shuts down 2026-06-01. This walkthrough maps a `.cirrus.yml`
workflow onto cove on a trusted Apple Silicon host. For the readiness gap
report see [cirrus-migration-readiness-2026-05-08.md](../strategy/cirrus-migration-readiness-2026-05-08.md)
(`fe99629`). For competitive context see
[competitive-2026-05.md](../strategy/competitive-2026-05.md) (`ffd7cc6`).
A longer task-by-task reference lives at
[../migrations/from-cirrus.md](../migrations/from-cirrus.md).

## Quickstart

```bash
cove up -user runner -vzscripts xcode,homebrew    # one-time host setup
cove image build -from default -tag acme/runner:latest
cove image verify --strict --newer-than 168h acme/runner:latest
cove run -fork-from acme/runner:latest -ephemeral -headless -- ./ci/test.sh
```

## Side-by-side

| Cirrus | cove |
|---|---|
| `container: image: golang:1.23` | `cove image build` (private OCI) + `-fork-from <ref>` |
| `macos_instance: image: ghcr.io/.../macos:14` | local image built from a maintained parent VM |
| `task:` block | GitHub Actions step using `./.github/actions/cove-action` |
| `script:` lines | `script:` input to `cove-action`; runs via guest agent ExecAttach |
| `env: KEY: value` | `--env KEY=value` on `cove run`, or `env:` step input |
| `env: KEY: ENCRYPTED[…]` | `--secret-env KEY=env://VAR` (see Secrets) |
| `cache: folder/key` | `cache-key:` + `cache-paths:` action inputs |
| `host_network: true` | `--net host-only` or named policy `host-services` |
| `persistent_worker: labels: …` | GitHub `runs-on: [self-hosted, macOS, ARM64, cove]` + `cove fleet` |
| Per-task isolation | APFS fork-per-job + `-ephemeral` teardown |
| Task logs | `cove runs list/show/export` over `~/.vz/runs/<run-id>/` |
| `only_if:` filters | GitHub `if:` expressions |
| Auto-cancel on push | `concurrency: cancel-in-progress: true` |

## Secrets

Cirrus `ENCRYPTED[…]` URIs and OIDC-issued tokens do not survive a
straight lift. Map them to GitHub Actions secrets, then plumb through
cove's redacted secret env path. Slice 1+2 shipped 2026-05-08; see
[cirrus-secrets-fix-2026-05-08.md](../strategy/cirrus-secrets-fix-2026-05-08.md)
(`c9df361`).

```yaml
- uses: ./.github/actions/cove-action
  with:
    image: acme/runner:latest
    script: ./ci/release.sh
    secrets: |
      NPM_TOKEN=${{ secrets.NPM_TOKEN }}
      AWS_ROLE_ARN=${{ secrets.AWS_ROLE_ARN }}
```

The action forwards each entry as `--secret-env NAME=env://VAR`.
`cove shell` and `cove run` redact the values from run logs. Do not pass
secrets through plain `env:` — those values are echoed verbatim.

For `file://` material (private keys, kubeconfigs):

```bash
cove shell <vm> --secret-env KUBECONFIG=file:///run/secrets/kubeconfig
```

## Caching

cove does not run a Cirrus-style HTTP cache server. Two patterns cover
most `cache:` blocks:

1. **Bake into the image.** Rebuild `acme/runner:latest` weekly via
   `cove image build`. Verify with
   `cove image verify --strict --newer-than 168h <ref>` in workflow
   preflight; provenance fields live in the image manifest.
2. **Per-key path cache.** `cove-action`'s `cache-key:` + `cache-paths:`
   restore/save cached directories on the host before/after the fork.
   Compute keys with `hashFiles()` instead of Cirrus's
   `fingerprint_script:`.

There is no content-addressed blob server. Sensitive caches stay on the
trusted host.

## Networking

cove network policy v2 covers the common Cirrus needs. Pick the closest
named policy on `cove run` (or `cove-action`'s `network:` input):

| Cirrus | cove `--net` |
|---|---|
| default outbound | `nat` |
| `host_network: true` | `host-only` (LAN to host) or `host-services` |
| custom container w/ no egress | `offline` |
| package install only | `packages` |
| LAN scanning / discovery | `lan` |
| anything goes | `open` |

`bridged:<iface>`, `vmnet`, and `filehandle` modes cover the rest. pcap
capture is available on `filehandle`. There is no
`cove network audit <run-id>` yet; tail with `cove network logs tail`.

## Known gaps

Items below ship for private-repo customers self-hosting today; public
users wait on registry/marketplace publication. Detail and SHAs in the
readiness audit.

| Gap | Status | Privacy-gated |
|---|---|---|
| Public Marketplace action | composite at `.github/actions/cove-action`, copy-paste per repo | yes |
| Public macOS/Linux image catalog | build locally with `cove image build` | yes |
| Cosign-signed images / SLSA public channel | local provenance only | yes |
| Hosted queue (Cirrus picks a Mac) | operator owns the host or `cove fleet` | no — scope decision |
| Multi-OS hosted CI (Linux x86_64 / Windows) | Apple Silicon only | no — out of scope |
| GitHub Actions annotations from-guest | print plain text; renders as logs | no — UX polish, sized M |
| Guest → host artifact copy-out | `cove ctl cp` or upload `~/.vz/runs/<run-id>/` | no — sized M |

## Questions / report bugs

- File issues at `github.com/tmc/cove` (private; ask a maintainer for
  access).
- Audit your own surface with `cove action doctor`.
- Reproduce with `VZ_DEBUG=1 cove run …` and attach `~/.vz/runs/<run-id>/`.
- Migration questions: cite this doc's SHA and your `.cirrus.yml` task class.
- Keep `.cirrus.yml` checked in until 2026-06-01 — it's your rollback.
