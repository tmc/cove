> Source: T68 round of conductor 0AA1EC69, captured 2026-05-05 around the Cirrus shutdown announcement. Durable summary: memory/project_cirrus_shutdown_2026_06_01.md.

# T68 competitive matrix v2: cove vs Lume/Cua vs Cirrus/Tart

Date: 2026-05-05
Original scope: design/research only. The 2026-05-11 refresh below is an
in-repo docs update.

## 2026-05-11 refresh: current gap analysis

This refresh reconciles the T68 matrix with the current `origin/main` state at
`6393ec1` and a May 11 public-doc pass over the comparison set. It does not
change the strategic center of gravity: cove is strongest where an operator
wants private, fork-per-task VM execution on Apple Silicon. The gap has moved
from "missing basics" to "public packaging, resource observability, and hosted
workflow ergonomics."

### Current competitor deltas

- **Lume/Cua:** Lume's public docs now present a single-binary CLI plus
  localhost HTTP API for VM management, macOS/Linux support on Apple Silicon,
  GHCR/GCS registry image support, unattended golden-image automation, and a
  managed cloud macOS sandbox pilot. Cua still has the clearer agent-facing
  package: Computer SDK, driver, benchmarks, and a story that starts from
  computer-use automation rather than VM plumbing.
- **Tart/Orchard:** Tart remains the public Apple-Silicon VM benchmark:
  registry images, Packer-friendly workflows, broad operator recognition, and
  Tart/Orchard licensing that is free below the published organizational free
  tiers (100 Tart CPU cores, 4 Orchard hosts) but commercial beyond that.
- **Cirrus CI:** Cirrus Labs still states that Cirrus CI shuts down effective
  2026-06-01. That removes the hosted-service long-term path but does not erase
  the operational expectations Cirrus users have: queueing, annotations,
  artifacts, cache semantics, and mature task ergonomics.
- **Daytona / hosted sandboxes:** The managed-sandbox category is getting more
  legible for agent users: APIs, SDKs, snapshot/restore, and computer-use
  entrypoints are the product, not an implementation detail. Cove should not
  try to become a generic hosted queue, but it must be honest that managed
  sandbox providers will beat it on "create a sandbox by API and forget the
  host" UX.

Refresh sources checked on 2026-05-11:

- Lume introduction: <https://cua.ai/docs/lume/guide/getting-started/introduction>
- Lume HTTP server: <https://cua.ai/docs/lume/guide/advanced/http-server>
- Lume VM management: <https://cua.ai/docs/lume/guide/fundamentals/vm-management>
- Tart licensing: <https://tart.run/licensing/>
- Cirrus Labs announcement: <https://cirruslabs.org/>
- Daytona sandboxes: <https://www.daytona.io/docs/en/sandboxes/>

### Gap table after R122-R139

| Gap | Status in cove now | Competitive pressure | Next action |
| --- | --- | --- | --- |
| Public install and public trust path | Still gated. The repo remains private; public Homebrew/Marketplace/image-catalog language must stay out of operator docs until the privacy/release decision is made. | Cua and Tart are much easier to evaluate from public docs. | User-gated release/distribution decision; do not auto-ship from conductor. |
| Public image catalog and signed provenance | Private OCI and tar transport work; public catalog, cosign/SLSA, and curated base images remain deferred. | Tart's GHCR image ecosystem is still the benchmark; Lume documents registry push/pull. | Decide the public registry/signing posture before marketing cove as drop-in image infrastructure. |
| Hosted queue semantics | Intentionally absent. Cove owns VM/image/fork execution and expects GitHub Actions, Buildkite, or an operator scheduler to schedule hosts. | Cirrus users expect queue semantics; Daytona-style products sell the API-hosted sandbox. | Keep as non-goal, but make scheduler handoff examples boring. |
| Resource observability | Run JSONL, OTLP, daemon/fleet metrics, and runs UX exist, but there is no periodic guest/host RAM/CPU sample in run metrics. `vz-agent info` exposes guest memory total/available as a point-in-time raw payload. | Cirrus users expect task resource visibility; hosted sandbox products expose operational state through APIs. | Add a small resource-sample metrics event: start/end first, periodic later if needed. |
| Guest artifact copy-out | Run bundles and `cove runs export` exist; guest-to-host artifact copy still requires explicit `ctl cp` or script cooperation. | Cirrus artifacts are first-class. | Add a cove-action artifact copy-out convention before public CI positioning. |
| GitHub annotations | Guest output is logs; `::error`/file-line annotation UX is not first-class. | CI users expect structured failures. | Parse/forward explicit annotation records or document the supported escape hatch. |
| Agent-facing UX | The canonical local path is `cove agent-sandbox run`: fork a local image, wait for the guest agent, run one provider loop, and write replay artifacts. The remaining gap is operator polish around default artifact summaries and background-safety expectations. | Cua leads with a clearer computer-use product. Daytona leads with API-first managed sandbox framing. | Keep `cove agent-sandbox run` as the one operator-facing path; make replay, metrics, and artifact summary defaults boring before adding new agent entrypoints. |
| Network policy depth | Baseline policies and audit/log surfaces shipped. | Tart Softnet-style allow/block policy remains deeper. | Keep current surface for v0.6; consider DNS/egress allowlist policy only if a customer workload demands it. |
| macOS capture backend | SCKit Slice 3 shipped; Slice 4 default flip and CGWindowList removal are intentionally deferred to v0.7. | Cua keeps improving background/focus-safe macOS control. | Do not rush pre-v0.7; finish perf/TCC evidence first. |
| Config/lifecycle code hygiene | Go-team review follow-ups are recorded: `applyUpConfig` global state and `macos.go` lifecycle cleanup. | Internal maintainability gap, not a market gap. | Handle only after protected dirty `up.go`/`macos.go` lanes land or are explicitly assigned. |

### Current ranking

1. **Resource samples in run metrics.** This is the smallest new product gap
   exposed by the May 11 audit. Cove already has the metrics pipe and guest
   memory fields; it needs a stable `resource_sample` event before users debug
   CI flakes by guessing.
2. **Guest artifact copy-out.** This is the most practical Cirrus migration UX
   gap left after secrets, action preflight, and runs export work.
3. **GitHub annotation forwarding.** Useful for CI parity, smaller than public
   distribution, and not blocked by privacy gates.
4. **Canonical agent command polish.** `cove agent-sandbox run` is the one
   operator-facing path; polish replay, metrics, artifact summaries, and
   background-safety wording before adding any parallel agent entrypoint.
5. **Public packaging/signing decision.** This remains high leverage but
   user-gated. Do not let conductor loops make this decision implicitly.

### Bottom line

Cove is production-plausible for private, operator-owned Apple-Silicon VM
runners after the R122-R139 hardening. It is not done as a public product. The
next non-gated gap to close is resource observability in `metrics.jsonl`,
followed by artifact copy-out and CI annotation polish. The larger public
catalog/signing/Marketplace story should remain gated until the repo and brand
posture are deliberately chosen.

## Executive read

The Round 27-31 work changed cove's position materially. The five T49
recommendations are no longer "next bets"; they are shipped surface:

- run metrics with JSONL plus optional OTLP export (`8318fa7`, `3f6c144`,
  `c390eb9`)
- private GitHub Actions executor and metrics wiring (`0985377`, `90493c3`,
  `19804c7`, `8bd473e`, `82a0ac5`, `300abe6`)
- minimal network policy modes and port-forward controls (`671754a`)
- agent-sandbox quickstart (`c50e165`)
- OCI image push/pull/load polish and ExecAttach hardening (`cf6a506`,
  `3891ef1`, `dc67cd5`, `dcb8977`, `18b7d8c`)
- plus OEM first-boot polish for Ubuntu Desktop (`4f71eb3`)

Cove now has a sharper wedge than in T49: **fork-per-task VM execution with
agent-native interactive control, private image transport, metrics, and
operator-owned CI packaging**. That closes the old "no CI wrapper", "no run
metrics", "weak network surface", and "weak computer-use packaging" gaps enough
that the next race is not basic feature parity. It is reliability, public
distribution readiness, and a cohesive operator product.

Lume/Cua still leads in public packaging and computer-use product clarity.
Cirrus/Tart still leads in mature public image ecosystem, Packer/templates,
networking depth, and installed CI mindshare. But Cirrus CI now has a strategic
discontinuity: Cirrus Labs announced it is joining OpenAI, stopped taking new
Cirrus Runners customers, and says Cirrus CI shuts down on June 1, 2026.

## Source key

Local cove:

- T49 baseline: `/tmp/5e2c-T49-cove-vs-lume-cirrus.md`
- `docs/reference/changelog.md`
- `docs/features/metrics.md`
- `docs/features/gha-executor.md`
- `docs/features/networking.md`
- `docs/quickstart-agent-sandbox.md`
- `docs/designs/023-cove-shell-exec-ux.md`
- `docs/designs/024-cove-runner-images.md`
- `image_registry.go`, `image_push_load.go`, `agent_control_attach.go`,
  `shell.go`, `proto/agent.proto`

Public web / upstream:

- Cua README: https://raw.githubusercontent.com/trycua/cua/main/README.md
- Lume CLI reference:
  https://raw.githubusercontent.com/trycua/cua/main/docs/content/docs/lume/reference/cli-reference.mdx
- Cua releases: https://github.com/trycua/cua/releases
- Cua/Lume changelog: https://cua.ai/docs/lume/reference/changelog
- Cua Driver README:
  https://raw.githubusercontent.com/trycua/cua/main/libs/cua-driver/README.md
- Tart README: https://raw.githubusercontent.com/cirruslabs/tart/main/README.md
- Tart quick start:
  https://raw.githubusercontent.com/cirruslabs/tart/main/docs/quick-start.md
- Tart releases: https://github.com/cirruslabs/tart/releases
- Tart 2.32.1 GitHub API release metadata, fetched 2026-05-05
- Cirrus CLI README:
  https://raw.githubusercontent.com/cirruslabs/cirrus-cli/main/README.md
- Cirrus persistent workers:
  https://cirrus-ci.org/guide/persistent-workers/
- Cirrus Labs announcement: https://cirruslabs.org/

## Last-30-day competitor deltas

### Lume / Cua

Lume VM surface did not show a major last-30-day lifecycle jump in the public
docs I fetched. The current generated CLI reference is versioned `0.3.0` and
still centers on create/clone/run/stop/delete/get/set/list, GHCR-style image
pull/push, multiple storage locations, cache config, `lume serve`, and preview
unattended macOS setup.

The meaningful Cua motion is around **background computer-use packaging**:

- `cua-driver-v0.1.0` on 2026-05-01 consolidated type/text and window-state
  tools.
- `cua-driver-v0.1.1` added cursor animation and AX/CG fallback behavior.
- `cua-driver-v0.1.3` and `v0.1.4` on 2026-05-04 fixed background hotkeys,
  focus-visible-window behavior, NSMenu shortcuts, overlay z-order, and added
  integration tests that assert clean UX.
- The Cua README now foregrounds "Cua Driver - Background computer-use on
  macOS", "Cua - Agent-Ready Sandboxes for Any OS", `cuabot`, `cua-bench`, and
  Lume as one package family.

Net: Lume itself remains a clean public VM CLI/API. Cua is pulling ahead on
agent-facing product packaging and background computer-use details, not on VM
fork/CI isolation semantics.

### Cirrus / Tart

Tart's public VM story remains strong: public macOS/Linux OCI images, Packer
templates, OCI registry push/pull, directory sharing, SSH/script guidance, and
CI positioning. The last Tart release visible in the last 30 days was `2.32.1`
on 2026-04-12:

- docs announcement about joining OpenAI
- Docker-related fixes
- Homebrew completion packaging change

Tart `2.32.0` was just outside the 30-day window but relevant: docs switched
to Tahoe language, disk v1 support was removed, and local network prompt docs
were updated.

Cirrus CLI's repository did not show commits from the last 30 days via the
GitHub commits API. Its docs still lead with reproducible `.cirrus.yml` tasks,
Docker/Podman execution, rsync-clean task roots, multi-CI integrations, and HTTP
cache server support. Persistent workers remain the mature operational model.

The strategic change is corporate/product lifecycle, not a feature commit:
Cirrus Labs says it is joining OpenAI, will relicense Tart/Vetu/Orchard under a
more permissive license, has stopped charging licensing fees, is no longer
accepting new Cirrus Runners customers, and Cirrus CI shuts down on Monday,
June 1, 2026.

## Updated feature matrix

| Area | cove now | Lume / Cua now | Cirrus / Tart now | T68 call |
| --- | --- | --- | --- | --- |
| VM lifecycle | macOS/Linux install, run, up, ctl, fork, image-backed `-fork-from`, ephemeral teardown. OEM desktop first-boot fix shipped. | Lume has clean create/clone/run/stop/delete/get/set/list and local API server. | Tart has mature create/run/stop/delete/list/get/set/rename/suspend plus public images. | Parity-ish on basics; cove leads on fork semantics, trails on public polish. |
| Image lifecycle | Local image store, build/list/rm/inspect/gc, tar push/load, OCI push/pull via `oras-go`, Docker auth reuse, public-push refusal still active. | Lume has GHCR-style pull/push, chunked upload, reassemble, cache/storage config. | Tart has mature OCI push/pull to any registry, public image catalog, Packer templates. | Old gap partly closed. Remaining gap is public signed/curated channel and templates. |
| Fork/ephemeral isolation | Strong lead: APFS clonefile fork, image-backed ephemeral forks, soft-reset rejected as isolation primitive, GHA action uses fresh fork per job. | Clone exists; Cua Sandbox exposes ephemeral API, but Lume docs do not give hard VM fork isolation contract. | Clone/standby/VM isolation exist, but task isolation often CI/persistent-worker-shaped. | Cove leads on explicit hard-isolation story. |
| Interactive shell / guest exec | ExecAttach v3 gives bidi stdin, resize, signal, concurrent attach sessions, fallback for older agents; no SSH required. | Cua Sandbox API has `shell.run`; Lume CLI public docs do not show equivalent interactive guest shell. | Tart has `tart exec` and SSH guidance; Cirrus tasks run scripts. | Cove now leads for Docker-shaped local interactive debug. |
| CI executor | Private GHA action wrapper shipped, direct wrapper verification done, metrics path exposed, fork-per-job model documented. | Public GHA-like integration not evident in fetched Lume docs; Cua is agent/sandbox/bench oriented. | Cirrus CLI has mature CI integrations and `.cirrus.yml`; Cirrus CI/Runners are entering shutdown/new-customer freeze. | Cove moved from behind to credible private wedge; public packaging still behind. |
| Metrics / observability | Run metrics JSONL and optional OTLP; action emits wrapper/command lifecycle and metric path outputs. | Cua has replayable trajectories and driver integration tests; Lume has logs. | Cirrus persistent workers have mature operational metrics and cache/task logs; Tart has OpenTelemetry work. | Gap narrowed. Cove needs dashboards, resource metrics, and artifact UX. |
| Networking | `--net nat|bridged:<iface>|host-only|none|vmnet|filehandle`, strict/minimal sandbox interaction, startup/runtime port forwards. | Lume docs show shared dirs, mounts, VNC, registry/storage; not Tart-level networking controls. | Tart has broader Softnet allow/block, bridged/host-only style controls; Cirrus has localnetworkhelper. | Cove closed minimal policy gap but still trails Tart Softnet depth. |
| Shared folders | Boot/runtime shared folders, Linux mount path fixes, pending/live honesty. | Lume `--shared-dir` and Linux read-only `--mount`. | Tart `--dir` with macOS automount and Linux manual mount/fstab docs. | Comparable; cove's UX honesty is improving but true hot-add remains future work. |
| Computer-use GUI | cove has screenshots, OCR/detect, mouse/keyboard/text, OpenAI/Anthropic/Gemini adapters, agent sandbox quickstart. | Cua leads: background macOS driver, MCP/CLI, replay trajectories, cuabot, benchmark suite, no focus stealing. | Tart provides UI/VNC/screenshot primitives; Cirrus is less computer-use-productized. | Cua still leads. Cove has substrate; needs a first-class agent UX. |
| Linux guest breadth | Ubuntu/Debian/Fedora/Alpine work, Rosetta, nested KVM, OEM Desktop fix. | Lume supports macOS/Linux VMs, but docs are less distro-specific. | Tart offers public Ubuntu/Debian/Fedora images and Vetu for Linux hosts. | Cove leads on installer/control depth, Tart leads on public image availability. |
| Public ecosystem | Private repo posture remains; public registry/signed channel deferred. | Cua is public MIT, active, broad docs/community. | Tart is public and widely used; Cirrus CI service is sunsetting but tools are moving permissive. | Cove still behind until public channel decision. |
| Security posture | Strong: fork isolation, secret tmpfs/RAM disk, public-push refusal, network `none`/strict modes. | Cua sandbox story is broad; Lume-specific secret lifecycle not evident. | CI secrets and registry auth are mature; VM-level secret lifecycle less explicit. | Cove leads in explicit private/local threat model. |

## What T49 gaps closed

1. **CI executor packaging: partially closed.** Cove now has a private GHA
   action wrapper, direct wrapper tests, metrics wiring, and fork-per-job
   security docs. Remaining gap: public Marketplace packaging and signed image
   channel are still deferred.
2. **Observability/metrics: materially closed.** Run JSONL and optional OTLP
   exist. Remaining gap: resource utilization, dashboard examples, artifact
   upload defaults, and persistent-worker host metrics.
3. **Networking controls: baseline closed.** NAT, bridged interface, host-only,
   none, and port-forward controls exist. Remaining gap: Tart Softnet-style
   allow/block policy, DNS/egress policy, and per-job network audit logs.
4. **Computer-use packaging: partially closed.** Agent Sandbox Quickstart
   exists across OpenAI, Anthropic, Gemini. Remaining gap: Cua Driver's
   focus-safe background control, native agent-facing CLI polish, and replay
   trajectory product.
5. **Public/private image transport: partially closed.** OCI push/pull now
   exists for private registries; public push remains intentionally gated.

## New gaps opened or became more visible

1. **Public product packaging now matters more.** Cove's private posture is
   coherent, but Cua and Tart are very legible to outsiders. After the GHA and
   metrics work, the missing piece is less capability and more installable,
   supportable surface.
2. **Agent UX needs one canonical command path.** Cove has adapters, ctl, shell,
   screenshots, OCR, and fork semantics. Cua has an agent-facing story that
   starts with `cua-driver`/MCP/CLI and feels packaged.
3. **Metrics are event-rich but not operator-rich yet.** JSONL/OTLP is the
   right base. Operators still need `cove runs list/show`, durations summaries,
   failure attribution, resource samples, and upload recipes.
4. **Public image freshness is now an operational requirement.** T63 had to
   refresh `agentkit/linux-base:latest` because a stale agent blocked cove
   shell readiness. Once CI depends on images, image freshness/signing/provenance
   becomes a first-class product risk.
5. **Cirrus shutdown changes the opportunity window.** Their CI service lead is
   weakening commercially, but Tart's tool ecosystem remains strong. Cove can
   win displaced local-runner users only if the private GHA path becomes boring.

## Where cove now leads vs both

1. **Hard local fork-per-task isolation.** Cove is more explicit than both
   competitors that soft reset is not isolation; the GHA wrapper and image
   runner flow now embody that stance.
2. **Interactive agent-native VM debug.** ExecAttach v3 gives cove an SSH-free,
   Docker-shaped `cove shell` path with bidi stdin, signals, resize, and
   fallback. Lume CLI does not show this, and Tart's story remains exec/SSH.
3. **Private, operator-owned CI with local forks.** Cirrus leads in mature CI,
   but its hosted service is sunsetting. Cove's private action is a better fit
   for teams that want to own the host, image, fork, metrics, and teardown.
4. **Security-first image transport.** Private OCI push/pull plus hard public
   push refusal is not as convenient as Tart, but it is a stronger default for a
   private repo and unreleased public brand.
5. **VM build/cache substrate.** Cove's build layers, secret directives,
   compaction, fork-time benchmarks, and agent exec make it more Docker-build
   shaped than Lume or Tart Packer alone.

## Where Lume/Cua still leads

1. **Agent-facing product clarity.** Cua README leads with background
   computer-use, sandboxes, cuabot, Cua Bench, and Lume as a coherent product
   family.
2. **Background macOS control UX.** Cua Driver is actively polishing focus
   safety, window targeting, NSMenu shortcuts, cursor animation, screenshots to
   file, MCP compatibility, and integration tests.
3. **Public install/docs/community.** Public MIT repo, generated docs, frequent
   releases, and a single public documentation story still beat cove's private
   repo posture.
4. **Cloud/local/cross-OS framing.** Cua's Sandbox API claims one API across
   local/cloud Linux/macOS/Windows/Android. Cove is deliberately Apple
   Virtualization.framework first.

## Where Cirrus/Tart still leads

1. **Public image ecosystem.** Tart's GHCR images, macOS/Linux image templates,
   and Packer plugin remain the public benchmark.
2. **Networking depth.** Tart Softnet and related allow/block controls are
   still ahead of cove's minimal policy surface.
3. **Operational CI maturity.** Cirrus CLI `.cirrus.yml`, cache servers,
   rsync-clean task roots, and persistent worker patterns are mature even if the
   hosted Cirrus CI service is ending.
4. **Adoption and trust.** Tart is a known Apple Silicon VM tool with public
   users and examples. Cove needs repeated successful workflows to earn that.
5. **Packer/template workflows.** Cove has build and image transport; Tart has
   public Packer templates that operators already understand.

## Ranked next 5 strategic investments for Rounds 32-40

### 1. Cove Runner Kit: private GHA action from "works" to "boring"

Impact: highest. Uniqueness: high, because it combines fork isolation,
ExecAttach, metrics, and private images.

Ship:

- `cove action doctor`: validates signed cove binary, entitlement, image
  freshness, agent version, disk capacity, network mode, and metrics output.
- `cove action prepare-image`: checks an image has current agent, runner deps,
  shell readiness, and no stale forks.
- Workflow examples for success, failure, artifact upload, metrics upload, and
  `--net none` / NAT.
- A live smoke script that uses one refreshed `agentkit/linux-base` image and
  asserts no stale-agent fallback.

Why now: T59/T63 proved this is where stale images and shell readiness surface.
This closes the "it works once for the author" gap and targets displaced Cirrus
CI users without needing public Marketplace release.

### 2. Runs UX: `cove runs list/show/export` over metrics + artifacts

Impact: very high. Uniqueness: medium-high.

Ship:

- `cove runs list` with run id, image, vm, status, durations, exit code.
- `cove runs show <id>` summary: fork time, boot time, agent-ready time,
  command time, teardown time, action exit code, artifact paths.
- `cove runs export <id> --format gha-summary|json|tar`.
- First failure classification: agent timeout, image missing, guest command
  exit, teardown orphan, network denied.

Why now: Metrics landed, but raw JSONL is not a user experience. Cirrus's
operational lead is still real; this turns cove's instrumentation into an
operator tool.

### 3. Image provenance and freshness gates

Impact: high. Uniqueness: high for cove's security posture.

Ship:

- image manifest fields: cove commit, agent commit, agent feature set,
  build recipe, source image, network/sandbox defaults.
- `cove image verify <ref>`: current-agent feature check, checksum,
  provenance completeness, public/private registry policy, fork count.
- stale-agent refusal or strong warning when an image lacks ExecAttach v3 for
  shell/action use.
- optional local signature placeholder even before public cosign channel.

Why now: T63 showed image lifecycle is now part of CI correctness. Tart leads on
public catalogs; cove can lead on explicit provenance/freshness.

### 4. Agent computer-use command surface

Impact: high. Uniqueness: medium.

Canonical path:

- `cove agent-sandbox run --provider openai|anthropic|gemini --image ... --task ...`
  is the operator-facing command. It must remain a thin wrapper around existing
  adapters, local-image forks, guest-agent readiness, replay capture, and run
  metrics.
- Next docs/UX work should make the default outputs boring: first-frame and
  screenshot-to-file behavior that avoids base64-in-context bloat, replay bundle
  paths, `cove runs show/export` links, and failure text for missing provider
  credentials or stale agent images.
- Background-safe macOS remains an audit/documentation item: identify whether
  cove can avoid focus theft or must document the difference from Cua Driver.

Why now: Cua is moving fast on background computer-use. Cove should not try to
clone every Cua Driver trick first; it should make its fork-isolated VM agent
story one command deep.

### 5. Network policy v2: egress and audit

Impact: medium-high. Uniqueness: medium.

Ship:

- named policies: `offline`, `packages`, `host-services`, `lan`, `open`.
- egress allow/block CIDR/domain plan, even if v1 implementation is host-side
  packet/logging plus documented limits.
- per-run network policy recorded in metrics and manifest.
- `cove network audit <run-id>` summarizing mode, forwards, pcap path, denied
  attempts where available.

Why now: Minimal networking closed the T49 parity gap. The next strategic step
is not copying Tart Softnet wholesale; it is making agent/CI network posture
auditable per run.

## Investment order

1. Cove Runner Kit: make private GHA action repeatable and self-diagnosing.
2. Runs UX: convert JSONL/artifacts into operator-facing run summaries.
3. Image provenance/freshness gates: prevent stale-agent CI regressions.
4. Agent computer-use command surface: one command over OpenAI/Anthropic/Gemini
   with replay bundles.
5. Network policy v2: named policies and per-run audit.

## Explicit non-recommendations

- Do not drop the public-push refusal just to match Tart. The private posture is
  coherent until the repo/name decision changes.
- Do not chase every Tart networking mode before cove has run-level audit UX.
- Do not build a hosted Cirrus replacement. The near-term wedge is
  operator-owned local runners.
- Do not compete with Cua Driver on background macOS focus tricks before
  packaging cove's existing fork-isolated computer-use flow.

## Completion audit

| Requirement | Evidence |
| --- | --- |
| Read T49 v2 baseline | Read `/tmp/5e2c-T49-cove-vs-lume-cirrus.md`; its original five gaps and lead/lag table are reflected above. |
| WebFetch/WebSearch public Lume/Cirrus docs/blog/repos for last 30 days | Used web search/open plus GitHub API/curl for Cua releases/commits, Lume CLI docs, Cua README/driver README, Tart README/quick-start/releases, Cirrus CLI README, persistent workers docs, and Cirrus Labs announcement. |
| Update cove-leads-on table after five investments shipped | See "What T49 gaps closed", "Where cove now leads vs both", and updated feature matrix. |
| Identify where cove leads and where competitors lead | See three lead sections for cove, Lume/Cua, and Cirrus/Tart. |
| Propose next 5 strategic investments for Rounds 32-40 ranked by user-visible impact and uniqueness | See ranked next five and investment order. |
| Design only, no commits, no code edits | No repo files were changed; this report is under `/tmp`. |
| Output path | `/tmp/629f-T68-cove-vs-lume-cirrus-v2.md`. |
