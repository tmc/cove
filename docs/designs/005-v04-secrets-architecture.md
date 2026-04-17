# cove v0.4 secrets — architecture options brief (Council consultation)

**Status**: draft v1 (post-second-opinion review)
**Author**: cove team
**Date**: 2026-04-16
**Target**: input for v0.4 roadmap; implementation follows v0.3 ship

---

## Changelog

- **v1 (2026-04-16)**: Hardened Option A to address Council round-2 concern about
  interactive secret-store auth hangs in headless CI. Added: 30s per-secret hard
  deadline (overridable via `# secret-timeout:` directive), TTY probe at build
  start that sets `COVE_BUILD_HEADLESS=1` and emits an advisory, kill-process-group
  on timeout (`syscall.Kill(-pgid, SIGKILL)`) so adapter subprocesses like `op`'s
  browser-launch helpers don't orphan, and mandatory stderr surfacing on adapter
  failure. Added new reliability dimension to the comparison matrix. Added a new
  open question for Council on `cove secret probe` debugging subcommand.
- **v0 (2026-04-16)**: Initial draft for Council vote.

---

## 1. v0.3 baseline (tmpfs, recap)

`cove build` v0.3 ships the `# secret: KEY` directive. During build, the host reads `$KEY` from its environment and mounts the value into the guest at `/tmp/cove-secrets/KEY` via a VirtioFS-backed tmpfs that never touches the disk image or OCI layer. This closed the "secrets baked into layers" hole and is the canonical secret-plumbing primitive going forward. v0.4 does not alter the in-guest contract — every option below still terminates in the same tmpfs mount. The only question is **how the host materializes the value before the mount**.

## 2. Options

### Option A — URI scheme delegating to existing CLI tools

**Design.** A new directive `# secret-from: KEY=<uri>` alongside `# secret:`. A small adapter registry in `internal/secrets` maps URI schemes to exec'd CLI tools: `1password://` → `op read`, `vault://` → `vault kv get`, `sops://path#key` → `sops -d`, `age://path` → `age -d`, with `env://` and `file://` as trivial fallbacks. cove captures stdout, hands it to the existing tmpfs pipeline, zeroes the buffer.

**Headless-CI hardening (v1).** Real adapters (`op`, `vault`, etc.) can prompt
for MFA/SSO/biometric on STDERR or block waiting on a browser callback even when
`cmd.Stdin = nil`. The naive "fail fast on stdin" posture is insufficient. Option
A therefore specifies three additional behaviors:

1. **Hard deadline per adapter invocation.** Default 30 seconds per secret
   resolution, overridable per-secret via a `# secret-timeout: 60s` directive in
   the manifest. The adapter is launched in its own process group
   (`SysProcAttr{Setpgid: true}`); on timeout, cove kills the **entire group**
   — `syscall.Kill(-pgid, syscall.SIGKILL)` — because tools like `op` commonly
   spawn helpers (browser launchers, session daemons) that would otherwise
   survive a bare `cmd.Process.Kill()` and orphan.

2. **TTY probe at build start.** If `cove build --headless` is passed, OR any of
   stdin/stdout/stderr is not a TTY (detected via `isatty` on each fd), cove
   sets the env var `COVE_BUILD_HEADLESS=1` in the adapter environment and
   emits an advisory warning once at build start:

   > `cove build: running headless; interactive secret-store auth (MFA, SSO,`
   > `biometric) will fail. Pre-authenticate before build (e.g. 'op signin' or`
   > `'vault login') or use service-account tokens.`

   Well-behaved adapters (including recent `op` and `vault`) respect
   `COVE_BUILD_HEADLESS` / `CI` style env signals and fail fast instead of
   opening a browser.

3. **Stderr surfacing on failure.** On adapter timeout or non-zero exit, the
   stderr buffer (bounded to 8 KiB, redacted of any bytes that overlap stdout
   to avoid leaking a partial secret) is surfaced verbatim to the user:

   > `cove build: secret adapter 'op' failed (timeout=30s, status=1).`
   > `Stderr: <captured>. Likely cause: MFA/SSO prompt; pre-authenticate or`
   > `use service-account token. See docs/build-secrets-ci.md.`

   v0.3's behavior of swallowing adapter stderr was the single biggest footgun
   raised in Council round 2; this closes it.

**Pros.** Delegates the hard problems — auth, MFA, session renewal, org policy — to tooling users already have configured. Minimal net-new attack surface in cove itself. Transparent: `cove build -v` prints the exact command; users can reproduce it at the shell. Works offline when the underlying tool does. With the v1 hardening, headless CI failure modes are deterministic (bounded latency, actionable error, no zombie subprocesses).

**Cons.** Users must install the CLIs they want to use; cove has to fail gracefully when `op` is missing. No cryptographic provenance of which secret a given build consumed. Bugs in adapter tools become cove's support burden.

**Complexity.** ~400–600 LOC, ~5 engineer-days including tests and docs. One new internal package. No new deps. The v1 hardening (pgid, TTY probe, stderr capture) adds ~80 LOC and is already budgeted inside the range.

### Option B — Age-encrypted sidecar OCI layer

**Design.** At `cove push` time, a new `cove secrets seal` step encrypts declared secrets to one or more age recipients and attaches them as a dedicated OCI layer with mediatype `org.tmc.cove.secrets.v1` and annotation listing key IDs. `cove build` resolves `# secret-from: oci://...` by pulling the sidecar, decrypting with the user's private key from the macOS keychain (or `~/.config/cove/age.key`), then mounting to tmpfs.

**Pros.** Secrets travel with the image — one pull gets everything. Enables signed provenance: the sealed layer is covered by the image signature, so a verifier can prove *which* encrypted payload was used. Strong story for air-gapped and CI-less environments.

**Cons.** Substantial crypto surface: `filippo.io/age`, recipient key rotation, revocation semantics, keychain integration on macOS/Linux. Recipient management is the historical UX killer for age-based tooling. Encrypted-but-leaked payloads still expand the blast radius if a recipient key is compromised. Inverts our "secrets never touch the registry" posture from v0.3 — defensible but requires clear messaging.

**Complexity.** ~1800–2500 LOC, ~15 engineer-days. New crypto code path, key-management CLI (`cove secrets keygen`, `recipients add`), registry integration.

### Option C — Go plugin API for secret providers

**Design.** Public interface `type SecretProvider interface { Name() string; Fetch(ctx, key) (string, error) }`. Third parties register providers; cove dispatches `# secret-from: provider://key`. Plugin loading via Go's `plugin` package or a build-tag-gated static registry bundled at compile time.

**Pros.** Maximum flexibility. Opens a clean ecosystem surface.

**Cons.** Go's `plugin` package is effectively abandoned (no Windows support, cgo required, version-skew landmines). The static-registry alternative couples every adapter's release cadence to cove's, which is the opposite of the flexibility we'd be pitching. Security review surface grows unboundedly as third parties ship code that runs in-process with build-host credentials.

**Complexity.** ~1000 LOC for the shim, but the real cost is a permanent maintenance tax on the plugin contract plus a security-review obligation per provider. ~8 engineer-days for v1; indefinite drag thereafter.

## 3. Comparison matrix

| Criterion                         | A: URI + CLI          | B: Age sidecar layer  | C: Plugin API         |
| --------------------------------- | --------------------- | --------------------- | --------------------- |
| Developer effort (v0.4 ship)      | Low (~5d)             | High (~15d)           | Medium (~8d + tail)   |
| User ergonomics                   | High (reuse existing) | Medium (key mgmt UX)  | Low (plugin install)  |
| Security properties               | Good (no new crypto)  | Strong (signed prov.) | Risky (in-proc code)  |
| OCI portability                   | None (host-only)      | Strong (travels)      | None                  |
| Maintenance cost                  | Low                   | Medium                | High (perpetual)      |
| Differentiation vs lume           | Modest                | Strong                | Modest                |
| Attack surface added              | Minimal               | Moderate (crypto)     | Large (plugin code)   |
| Offline story                     | Depends on tool       | Strong                | Depends on plugin     |
| Reliability under headless CI     | Strong (v1 hardening) | Strong (no prompts)   | Unknown (per-plugin)  |

## 4. Recommendation

**The cove team recommends Option A for v0.4.** Rationale:

1. **Ships on the v0.4 window.** Every other item on the v0.4 roadmap (disk snapshots GA, vzscript-as-Dockerfile parity, Council-mandated audit log) is non-trivial. Option A fits in the remaining budget; B does not.
2. **Matches user mental models.** Teams that already use 1Password, Vault, or SOPS have invested in those flows. Option A honors that investment; B asks them to re-key their world.
3. **Security posture is defensive by default.** cove gains no new crypto primitives, no new keychain integrations, no new registry-resident payloads. The blast radius of a cove bug stays small — delegated trust is still trust, but it is trust in tools already audited and trusted by the user's org.
4. **Headless CI is now a first-class concern.** The v1 hardening (30s hard deadline, TTY probe with `COVE_BUILD_HEADLESS=1` advisory, kill-process-group on timeout, mandatory stderr surfacing) directly addresses Council round-2's concern that naive stdin-closing leaves `op`/`vault`/`sops` free to hang on MFA/SSO/browser callbacks. Failure modes are now bounded in latency and actionable in their error messages. This is the maturity signal that was missing from v0.
5. **Non-exclusive.** Option A does not foreclose B. If v0.5 demands OCI-portable secrets (e.g., for air-gapped customers), the sidecar layer can land later as an additional scheme (`oci://`) on top of the same adapter registry. Option C, by contrast, is genuinely rejected — Go plugin loading and in-process third-party code are not compatible with our security posture.

**Decision requested:** Council confirms Option A for v0.4, defers B to v0.5 pending air-gap signal, formally rejects C.

## 5. Open questions for Council

1. **Multiple schemes at once?** Should a single manifest mix `# secret:`, `# secret-from: 1password://`, and `# secret-from: env://` freely, or do we require one style per build for clarity?
2. **Missing-tool behavior.** If a user references `1password://` without `op` installed, do we hard-fail at manifest parse, or fall back to prompting the user interactively? (Recommendation: hard-fail; prompting is a phishing risk.)
3. **Caching.** Should cove cache fetched secrets for the duration of a build to avoid re-prompting MFA on every `# secret-from` line? If yes, where — process memory only, or an encrypted on-disk cache keyed to the build?
4. **Audit log scope.** v0.4 audit log should record *which URIs were resolved* (not values). Does Council want the adapter command line captured verbatim, or normalized to scheme+key to avoid leaking paths?
5. **v0.5 OCI sidecar preview.** Should we ship a feature-flagged `oci://` scheme in v0.4 as a proof-of-concept, or hold the line and keep v0.4 strictly host-side?
6. **`cove secret probe` debugging subcommand (new in v1).** Should we ship a small `cove secret probe <uri>` that resolves a single secret URI with full stderr attached and no tmpfs plumbing, purely for debugging adapter hangs in CI? ~30 LOC on top of the existing adapter registry. Would help CI users diagnose MFA/SSO hangs without having to run a full `cove build` loop. Team leans yes but defers to Council on whether this belongs in v0.4 or slips to v0.4.1.

---

*References: v0.3 secrets design doc (`docs/design/secrets-v0.3.md`), Council round-1 notes (`docs/council/2026-03-secrets-round1.md`), Council round-2 notes (`docs/council/2026-04-secrets-round2.md`), vzscript directive reference (`docs/vzscript.md`), headless-CI adapter runbook (`docs/build-secrets-ci.md`).*
