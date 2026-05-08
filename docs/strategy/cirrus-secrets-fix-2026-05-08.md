---
title: Cirrus secrets → guest env propagation (sized fix)
date: 2026-05-08
audience: implementor picking up the only pure-engineering Cirrus L blocker
size: M
parent: docs/strategy/cirrus-migration-readiness-2026-05-08.md (item 2)
---

# Cirrus secrets → guest env (M)

**Slice 1 shipped (2026-05-08)** — `metrics: redact secret values in run
logs` at `29ff983`, `shell: add --env and --secret-env flags` at
`13ce8c0` (summary: `fe99629`). Host-side `cove shell` flag + run-log
redactor land; proto/ and internal/agent/ untouched.

**Slice 2 shipped (2026-05-08)** — `cove-action: parse secrets: input,
plumb to --secret-env` at `ab7f159`. The GHA composite `secrets:` input
now accepts a multi-line `KEY=value|env://VAR|file:///path` block,
forwarded as repeated `--secret-env` flags to `cove shell`. Same
redaction guarantees as Slice 1.

Cirrus shuts down 2026-06-01. The migration audit
([cirrus-migration-readiness-2026-05-08.md](cirrus-migration-readiness-2026-05-08.md))
identified **one** purely-engineering L blocker: lifting Cirrus
`ENCRYPTED[…]` secrets into the guest environment without leaking them into
workflow logs.

## Today (traced)

Two host→guest secret paths exist; neither covers the runtime case.

1. **Build-time (`cove image build`)** — `build_secrets.go:20-106` mounts a
   tmpfs (Linux) / ramdisk (macOS) at `/tmp/cove-secrets/<NAME>` and writes
   each resolved secret as mode-0600 file. Triggered by vzscript
   `# secret: NAME` (env://) and `# secret-from: NAME=<uri>` directives via
   `internal/secrets/{env,file}.go`. Files are scoped to the build step and
   torn down on cleanup. Not visible in run logs.
2. **Runtime (`cove shell`, `cove-action`)** — no secret path. The
   cove-action composite explicitly rejects its own `secrets:` input
   (`.github/actions/cove-action/action.yml:48-49,95-99`). The escape hatch
   is plain `env:` lines, which land in the guest verbatim via a shell
   `env K=V K=V /bin/sh -lc <cmd>` prefix (`cmd/cove-action/main.go:430-440`)
   — fine for non-sensitive values, unsafe for tokens.

The wire is ready: `proto/agent.proto:87` ExecRequest already has a
`map<string,string> env`, and `agent-exec-attach`'s JSON envelope accepts
an `env` field (`agent_control_attach.go:59,135`). The gap is end-to-end
plumbing on the host side plus a redaction discipline.

## What "fixing this" actually requires

Not just an env-merge — five concerns, in order:

1. **Source surface.** `cove shell` and `cove-action` accept
   `--secret-env NAME=URI` (resolved via `internal/secrets`). URI schemes
   stay locked to `env://` and `file://`. Already-resolved plain-text
   `--env NAME=VALUE` continues to work for non-sensitive values.
2. **Transport.** Populate the existing `env` slot on `agent-exec-attach`.
   No proto change. `cove shell` already builds the attach JSON; add an
   env map and a `-e` / `--secret-env` parser. Materialize secret bytes
   only after dial succeeds; zero the host-side buffer on session end.
3. **Redaction.** Every secret value resolved on the host gets registered
   with the run-log writer
   (`runs_cli.go` / `internal/metrics/`) before any guest output is
   captured. The writer rewrites byte sequences matching any registered
   secret to `***`. Mirrors GitHub Actions' add-mask. Bypass-proof: secrets
   are added to the masker before the first attach frame is sent.
4. **Scope/lease.** Secrets injected via attach env live for the exec
   session only; the agent does not persist them. No keychain, no daemon
   memoization. Lease == process lifetime of the exec.
5. **Naming + collisions.** `--secret-env` overrides `--env` of the same
   name with a stderr warning. Empty resolved value is an error
   (no silent skip).

Privacy gate is irrelevant here — this is local host→local guest, no
public registry surface.

## Sizing

| Surface | Files | Est LOC |
|---|---|---|
| `cove shell` flag + attach env wiring | `shell.go` | ~80 |
| `cove-action` `secrets:` input + parser | `cmd/cove-action/main.go`, `.github/actions/cove-action/action.yml` | ~70 |
| Run-log redactor | `internal/metrics/redact.go` (new), wire from `cove shell` + `runs export` | ~120 |
| Tests | `shell_secret_test.go`, `cmd/cove-action/main_test.go`, `internal/metrics/redact_test.go` | ~150 |
| Migration doc | `docs/migrations/from-cirrus.md` step 7 update | ~30 |

**Total: ~450 LOC, M.** No proto/agentpb change, no `internal/agent/`
change beyond the existing env field. Touches the facade-stable surface
read-only via the already-shipped env slot — no new RPCs.

## Out of scope

- New URI schemes (s3://, vault://, aws-sm://). `env://` + `file://` is
  enough to lift `ENCRYPTED[…]` (operator decrypts to env, points at it).
  Adding more is a follow-up, not part of this slice.
- Build-time secret refactor. `build_secrets.go` already does the right
  thing; leave it.
- TCC / sandbox profile work. Runtime exec env is non-privileged.
- Public Marketplace exposure. Composite stays private; privacy gate.

## Acceptance

- `cove shell <vm> --secret-env GH_TOKEN=env://GH_TOKEN -- printenv GH_TOKEN`
  prints the value once, then any reprint via `cove runs show <id>` is
  redacted to `***`.
- `cove-action` workflow with `secrets:` input no longer errors; same
  redaction discipline in `runs export`.
- `cove shell` exit code, signal, and TTY behavior unchanged.
- Migration audit item 2 demoted L→D with shipped SHA.

## Suggested commit shape

1. `secrets: redactor for run logs` (`internal/metrics/redact.go` + test).
2. `shell: --secret-env NAME=URI on cove shell` (shell.go + test, wires
   redactor).
3. `cove-action: accept secrets: input, plumb to --secret-env` (action.yml
   + main.go + main_test.go).
4. `docs/migrations/from-cirrus: step 7 secrets walkthrough`.

Single ff-merge to main per cove-private convention. No PR.
