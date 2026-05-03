# cove-action security architecture

**Status**: draft.
**Source**: conductor dispatch 2026-05-02; user decision (cove repo
stays private through this design; cove-action implementation deferred
to v0.4; image push/pull is private-only until the public flip).
**Roadmap**: v0.4 — must land before [021](021-v04-ci-executors-tracks.md)
Slice 1 (cove-action GHA wrapper, ~600 LOC) implementation begins.
**Branch**: planning.

## Goal

Specify the threat model, token lifecycle, and isolation invariants
for the cove-action wrapper that ships as
[021](021-v04-ci-executors-tracks.md) Slice 1. cove-action orchestrates
ephemeral cove forks per CI job; this doc nails down what trust
boundary each token crosses, what residue we are required to keep out
of the parent image, and which knobs the operator is forbidden from
turning off.

The load-bearing input is [015](015-soft-reset-empirical.md): warm
soft reset failed isolation 0 / 3 / 3 against System Keychain,
GlobalPreferences, and orphaned LaunchDaemons. fork/restore is the
only supported isolation primitive. Everything in this doc follows
from that result.

## Non-goals

- Implementation code. cove-action lives in a sibling design
  (021 Slice 1); 025 specifies the security shape it must satisfy.
- Cross-host load balancing. One host, one cove-action installation.
- Multi-tenant runner sharing. One operator owns the cove host; CI
  jobs are mutually distrustful but the operator is trusted.
- GitHub Enterprise on-prem. Out of scope until a customer asks.
- Public-registry signature requirements. Deferred to
  [024](024-cove-runner-images.md) Slice 3 (public-flip dependent).
- A parallel security model for GitLab. The shape is identical;
  token names differ. Differences called out inline.

## 1. Threat model

### Actors

| Actor | Trust | Notes |
|---|---|---|
| GitHub Actions controller | trusted | Mints tokens, dispatches workflow runs. cove-action obeys. |
| GitLab CI controller | trusted | Same role; mints `CI_JOB_TOKEN`, registration token. |
| cove-action runner shim (host) | trusted | The host-side Go binary. Holds operator credentials. |
| Inside-VM workflow code | **untrusted** | May be third-party PR. Assumed adversarial. |
| cove host | trusted | The macOS machine running cove. |
| Operator | trusted | The human who installed cove and registered the runner. |

### Assets

| Asset | Where it lives | Worst-case loss |
|---|---|---|
| GH PAT (operator's, used for `cove-action register`) | host filesystem (or keychain ref) | Operator account compromise; runner registration spam. |
| GH Actions registration token | host memory, briefly | Lets attacker register a fake runner; ~1h lifetime. |
| GH Actions per-job runner token | inside the ephemeral fork | Lets attacker poll job queue for one workflow run. |
| `GITHUB_TOKEN` (per-job, GH-injected) | inside the fork, never seen by cove-action | Repo-scoped write per workflow run. |
| Repo / org secrets (GH-injected) | inside the fork | Whatever the secret grants. |
| Parent VM image | host filesystem | Identity / credentials baked into the image. |
| Other VMs on same host | host filesystem | Cross-VM compromise via host kernel or daemon. |

GitLab analogue: `CI_JOB_TOKEN` plays the `GITHUB_TOKEN` role; CI/CD
variables play the GH-secrets role; runner registration token is
distinct from the per-job token same as GitHub. Token names differ;
trust boundary is identical.

### Attack surfaces

1. **Malicious workflow code** — anything `run:` puts on the guest
   shell. Assumed RCE inside the fork. Containment is the fork
   boundary.
2. **Malicious PR fork** — same, but the workflow is the
   first-time-PR variant. GH already gates `pull_request` events on
   approval; cove-action does not weaken that gate.
3. **Side-channel between concurrent jobs** — two forks running on
   one host at the same time. Containment is the host hypervisor
   boundary plus per-fork unique `machine.id` / MAC / disk
   ([013](013-vm-fork.md) hard constraints).
4. **Residue between sequential jobs on the same parent** — the
   exact failure mode [015](015-soft-reset-empirical.md) measured
   for warm soft reset. Containment is "every job is a fresh fork
   from a never-mutated parent image".
5. **Operator misconfiguration** — operator disables `-ephemeral`
   or shares one parent across runs without forking. Containment
   is a startup-time refusal in cove-action (see Section 3).

## 2. Token lifecycle

For each token, this section pins *who mints*, *where it lives*,
*what it crosses*, and *how it dies*.

### 2.1 Operator GH PAT

- **Mints**: operator, manually, via GitHub UI.
- **Holds**: the host filesystem or the operator's macOS Keychain.
  cove-action reads it at `cove-action register` time only.
- **Crosses**: never crosses the host → guest boundary. PAT exists
  on the host only.
- **Dies**: when the operator rotates or revokes it, manually.
- **Failure mode if leaked**: full operator-account compromise
  scoped to the PAT's permissions. Mitigation: scope the PAT to
  `repo` + `workflow` only; document the minimum scope in
  cove-action's `register` help text.

GitLab analogue: a personal access token with `api` scope for
`cove-action register`. Same host-only constraint.

### 2.2 GH Actions registration token

- **Mints**: GitHub, via `POST /repos/{owner}/{repo}/actions/runners/registration-token`,
  authenticated with the operator's PAT.
- **Holds**: cove-action host process memory, briefly. Never
  written to disk. Never written into a parent image. Never sent
  to a long-lived child VM.
- **Crosses**: host → ephemeral fork once, at fork-boot time, via
  a guest-agent `Exec` call that hands it to the inside-VM runner
  binary (`./config.sh --token <regtok>`). After `config.sh`
  succeeds the inside-VM runner has a per-job token (see 2.3) and
  the registration token is discarded.
- **Dies**: ~1 hour after mint (GitHub-side TTL); cove-action
  treats it as already-dead at fork teardown.
- **Failure mode if leaked**: attacker can register a runner under
  the operator's repo for ~1h. Mitigation: short TTL is enforced
  by GitHub, not by us; we just avoid persisting it.

GitLab analogue: runner registration token from
`POST /api/v4/runners`. Same lifecycle; longer TTL by default but
still rotatable. cove-action MUST refresh per-fork rather than
embedding in the image.

### 2.3 GH Actions per-job runner token

- **Mints**: GitHub, on behalf of the inside-VM runner after it
  successfully runs `config.sh` against the registration token.
- **Holds**: inside the ephemeral fork only, on the fork's disk
  under the runner's `actions-runner/.credentials*` files.
- **Crosses**: never leaves the fork.
- **Dies**: when the fork is destroyed at end-of-job. The
  inside-VM disk is discarded; the credentials files go with it.
  cove-action MUST NOT attempt to recover or reuse this token.
- **Failure mode if leaked**: scoped to one workflow run; expires
  with the run. Containment is the fork boundary; if the fork
  filesystem leaks (e.g. operator copies it out), the token leaks.

### 2.4 `GITHUB_TOKEN` (per-workflow)

- **Mints**: GitHub.
- **Holds**: inside the fork, in the runner's per-job env.
- **Crosses**: cove-action does not see it. The host-side runner
  shim does not forward it; GH injects it directly into the
  runner's env via the runner protocol.
- **Dies**: at end of workflow run.
- **Failure mode if leaked**: repo-scoped write per run. Same as
  any GH-hosted runner. cove-action does not change the surface.

GitLab `CI_JOB_TOKEN` is identical-shape: scoped, GitLab-injected,
not seen by cove-action.

### 2.5 Repo / org secrets

- **Mints**: GitHub, per workflow run.
- **Holds**: inside the fork as runner env vars.
- **Crosses**: cove-action does not see them. Same channel as
  `GITHUB_TOKEN`.
- **Dies**: at end of fork.
- **Failure mode if leaked**: whatever the secret grants. The
  cove-action invariant is: *the fork is destroyed before the next
  job starts*, so secrets cannot leak across jobs. This is
  enforced by Section 3.

## 3. Isolation invariants

This section is non-negotiable. It is the cash value of
[015](015-soft-reset-empirical.md).

1. **Every job runs in a fresh ephemeral fork.** cove-action
   invokes `cove run -fork-from <parent-image-ref> -ephemeral`
   per job ([024](024-cove-runner-images.md) shipped this surface
   at `8a106dc`).
2. **The parent image is never mutated.**
   [024](024-cove-runner-images.md) Slice 1 already enforces this:
   `cove image rm` is gated by the `ParentImage` field on every
   live fork's `config.json`. cove-action MUST consume an image
   built via `cove image build`, not a registered VM that an
   operator might also use interactively.
3. **Soft reset is rejected as a primitive.** cove-action MUST
   refuse to reuse a parent VM as a runner directly. The operator
   trying to point cove-action at a registered VM (rather than an
   image) gets a refusal at `cove-action register`-time with a
   pointer to [015](015-soft-reset-empirical.md) and to
   `cove image build`.
4. **`-ephemeral` cannot be disabled.** There is no
   `cove-action --persist-fork` knob. If a future contributor
   proposes one, the answer is no, and the answer cites
   [015](015-soft-reset-empirical.md). The fork-end-of-job destroy
   is the boundary that keeps Section 2.5 honest.
5. **Lineage gate.** [013](013-vm-fork.md) Phase 4 added
   `vm tree` and `delete --cascade`. cove-action's per-job forks
   are first-class children; orphaned forks (job died mid-run,
   host crashed) are visible via `cove vm tree --orphans` and
   reaped by a periodic `cove-action gc` step. The operator can
   inspect orphans before the GC fires.

The startup-time refusals (3 and 4) are the operator-misconfig
containment. They are not advisory; they are exit-non-zero with
a documented error message that names design 015 and 024.

## 4. Filesystem isolation

What boundary exists between the cove host and the inside-VM
workflow code:

- **No shared folders by default.** cove-action does not mount any
  host directory into the fork unless the operator opts in via
  cove-action config.
- **Opt-in shared folders are read-only and pinned.** If the
  operator opts in (e.g. to share a project workspace), the
  shared folder MUST be read-only and MUST be a subpath of a
  pre-declared root (cove-action config field; not
  per-job-controllable). The inside-VM workflow cannot widen the
  scope.
- **Control socket is host-only.**
  `~/.vz/vms/<fork>/control.sock` and `control.token` live on the
  host filesystem. They are never exposed inside the guest. The
  inside-VM workflow code cannot reach `cove ctl`.
- **Vsock is one-way operationally.** The guest agent is reachable
  from the host via vsock; agent commands originating *inside*
  the VM cannot escalate. The agent only executes what the host
  pushes through `Exec` / `Cp` / `Write` RPCs
  ([006](006-cove-linux-v02.md), `proto/agent.proto`). Inside-VM
  code cannot make the agent execute new commands on its own host;
  vsock dial requires the `VZVirtualMachine` instance reference
  that only the host-side cove process holds
  ([023](023-cove-shell-exec-ux.md) "vsock ownership" finding).
- **Other VMs on the same host are out of reach.** The hypervisor
  boundary; cove does not weaken it.

## 5. Network isolation

What the inside-VM workflow can reach:

- **NAT to internet by default.** Matches GH-hosted runner
  expectation; most workflows expect outbound internet for
  `actions/checkout`, package fetch, etc.
- **Egress allowlist policy is operator-configurable.** cove-action
  config exposes a `network` field with values `nat`, `bridged:<iface>`,
  `none`, or `allowlist:<host-list>`. The default is `nat`.
- **`bridged` puts the runner on a private network if the operator
  wants it.** Same surface as `cove run -network bridged:en0`.
- **`none` is supported** for jobs that should have no network at
  all (rare; useful for build-only jobs that consume pre-fetched
  inputs from a read-only shared folder).
- **No inbound network surface.** The fork does not expose
  listening ports to the host LAN by default. Port forwards are
  per-cove-action-config and out of scope for Slice 1.

GitLab analogue: identical. `CI_JOB_TOKEN`'s reach to GitLab APIs
travels over NAT outbound; same as `GITHUB_TOKEN` for cove-action
on GHA.

## 6. Image trust

Where parent images come from, and how cove-action knows they're
the right ones:

- **Privacy gate honored.** While the cove repo
  ([`tmc/cove`](https://github.com/tmc/cove)) is private, parent
  images come from one of:
  - `cove image build` on the operator's host (locally built); or
  - `cove image pull` from a private registry the operator owns
    (Slice 2 of [024](024-cove-runner-images.md), v0.4).
  Public-registry pulls are refused by [024](024-cove-runner-images.md)
  Slice 1 (`COVE_ALLOW_PUBLIC_PUSH=1` / pull override exists for
  explicit operator opt-in).
- **cosign sign/verify is OFF by default in v0.4.** It becomes
  required for public push in [024](024-cove-runner-images.md)
  Slice 3, which is contingent on the cove repo flipping public.
  When that flip happens, cove-action will REQUIRE signature
  verification by default; until then, image trust is "operator
  built it; operator owns it".
- **`cove image rm` gate already protects the parent.**
  [024](024-cove-runner-images.md) Slice 1 enforces that an image
  with live forks cannot be deleted. cove-action's per-job forks
  are live forks; the parent cannot be swapped under cove-action
  while jobs run.

## 7. Logging and audit

What cove-action records on the host. The principle: log enough to
debug a job failure or detect a security event, never log a token
or secret.

### Recorded per-job

- workflow run ID (GH provides via env; GitLab provides via env)
- fork name (`cove-action-<run-id>-<attempt>`)
- fork lineage (parent image ref + digest, captured from
  `cove run -fork-from`'s output)
- start time, end time
- exit code
- vzscript / commands invoked (the cove-job descriptor; not the
  workflow stdout)

### NOT recorded

- GH PAT, registration token, per-job runner token, `GITHUB_TOKEN`,
  any repo/org secret, `CI_JOB_TOKEN`, or any GitLab CI/CD
  variable value.
- Workflow stdout / stderr. Those go to GitHub's / GitLab's job
  logs via the runner. cove-action MUST NOT tee them on the host.

### Storage

- Path: `~/.vz/cove-action/runs/<run-id>-<attempt>.json`. Mirrors
  the existing `~/.vz/vms/<name>/` layout
  ([024](024-cove-runner-images.md) chose `~/.vz/images/` over an
  early `.cove/`-rooted proposal for the same reason).
- Format: JSON, one file per job. Easy to ship to a SIEM if the
  operator wants to.
- Retention: cove-action does not GC these by default; operator
  rotates.

## 8. Relationship to the T-GHA-RUNNER vzscript

[`vzscripts/github-runner.vzscript`](../../vzscripts/github-runner.vzscript)
(`7c315fc`, T-GHA-RUNNER) is the **manual provisioning path**. It
installs and registers a long-lived self-hosted runner inside one
named VM. The runner persists across jobs.

cove-action is the **automated path**. It spawns one ephemeral fork
per job, registers a fresh runner inside, runs the job, destroys
the fork.

| Path | When to use | Isolation story |
|---|---|---|
| `vzscripts/github-runner` | developer machine convenience; one-off self-hosted runner against a side branch; you accept warm-guest residue | soft-reset shape — see [015](015-soft-reset-empirical.md), not a privacy boundary |
| `cove-action` | production CI; PR-from-fork workflows; multi-job throughput; secrets matter | fork-per-job, parent never mutated; the supported primitive |

Both can coexist. The vzscript path is documented as developer
convenience; the production story is cove-action.

[024](024-cove-runner-images.md) section "Where does
T-GHA-RUNNER live once images ship?" picks the right answer:
T-GHA-RUNNER is the bake-step that goes into `cove image build`,
so the published image already has the runner agent installed
and cove-action just registers it per-fork.

## 9. Open questions and non-goals

### Open

1. **Where does the operator GH PAT live?** Strawman: macOS
   Keychain via `cove-action register` (asks once, stores in
   System.keychain via `security add-generic-password`).
   Alternative: environment variable / config file. The Keychain
   path is the lowest-blast-radius default; document the
   alternatives.
2. **GC cadence for orphaned forks.** Strawman: cove-action runs
   `cove vm tree --orphans` on each `register` and on a periodic
   timer (LaunchAgent), reaping forks older than 24h whose
   workflow runs are completed per the GH API. Tune in Slice 1.
3. **What happens if the operator runs cove-action against an
   image that is mutated mid-run?** [024](024-cove-runner-images.md)'s
   `cove image rm` gate covers delete-while-live, but in-place
   mutation of an image (e.g. operator runs `cove image build` to
   re-tag) is undefined. Strawman: image refs are content-hashed
   at fork-spawn-time; subsequent re-tags do not affect already-
   spawned forks. Verify with [024](024-cove-runner-images.md)
   author.

### Non-goals (deferred)

- Cross-host load-balancing. One host, one cove-action.
- Multi-tenant runner sharing on one host. Operator is trusted;
  jobs are not. We do not solve "untrusted operator".
- GitHub Enterprise on-prem. Wait for a customer.
- Public-registry signature requirements. Deferred to
  [024](024-cove-runner-images.md) Slice 3 (public-flip dependent).
  When that lands, cove-action's image-trust default flips to
  "verify-or-refuse".
- GitLab parity beyond shape. The token lifecycle and isolation
  invariants are identical-shape; the wire-level token names
  differ. cove-action's GitLab adapter (Slice 2 of
  [021](021-v04-ci-executors-tracks.md)) inherits this design
  unchanged except for substituting `CI_JOB_TOKEN` for
  `GITHUB_TOKEN` and `gitlab-runner` registration for
  `actions-runner` registration.

## References

- [013](013-vm-fork.md) — fork-from semantics; Phase 3 RAM-overlay
  (`99b3732`); Phase 4 lineage / `vm tree` (`eacbf5e`).
- [015](015-soft-reset-empirical.md) — load-bearing. Soft reset
  failed isolation 0/3/3; fork/restore is required.
- [021](021-v04-ci-executors-tracks.md) — cove-action GHA wrapper
  (Scope B) and GitLab shim. 025 must land before 021 Slice 1
  implementation.
- [024](024-cove-runner-images.md) — `cove image build/list/rm` +
  `-ephemeral` (Slice 1 shipped at `8a106dc`). cove-action consumes
  this surface.
- [`vzscripts/github-runner.vzscript`](../../vzscripts/github-runner.vzscript)
  (`7c315fc`, T-GHA-RUNNER) — manual provisioning path that
  cove-action improves on.
- [023](023-cove-shell-exec-ux.md) — vsock-ownership invariant
  cited in Section 4.
- [006](006-cove-linux-v02.md) — guest agent gRPC over vsock; the
  `Exec` / `Cp` / `Write` RPCs cove-action uses to seed the
  registration token at fork-boot.
