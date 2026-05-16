# cove 12-month product roadmap — 2026 → 2027

**Status**: draft v1 (strategy / not yet Council-reviewed; v1 incorporates
adversarial review, source review of cirruslabs/tart+orchard+tart-guest-agent,
and an honesty pass — see "Honesty notes" near end and `/tmp/cirruslabs-review.md`
for receipts)
**Author**: cove team (drafted by external product-thinker session v0, revised
by v2 session, NotebookLM-assisted)
**Date**: 2026-04-25
**Horizon**: cove 0.2 → 1.0 (Q2 2026 → Q1 2027)
**Predecessor**: [011-beat-lume-roadmap.md](011-beat-lume-roadmap.md)

## Why this doc exists

[011-beat-lume-roadmap.md](011-beat-lume-roadmap.md) is the engineering roadmap — what
cove builds, in what order, to beat lume on its turf. It is technically opinionated and
correct.

What 011 doesn't say is *who cove is for in 12 months*, *which competitor is the real
threat* (it isn't lume), and *how cove stays alive* when the macro market and the
maintainer situation both move underneath it. This doc is the strategy layer above 011.
It picks a primary positioning, names the cohort, names the threats, and proposes a
sustainability model.

Read this doc when you need to decide whether a feature *belongs*. Read 011 when you
need to decide *what to build next inside the chosen direction*.

## Executive summary

In April 2026 the macOS-VM-on-Apple-Silicon space is being repriced. OpenAI acquihired
Cirrus Labs (Tart, Vetu, Orchard, Cirrus Runners) on 2026-04-07 and is shutting down
Cirrus CI on 2026-06-01; the team's stated focus at OpenAI is "Agent Infrastructure"
(announcement banner, `cirruslabs/tart` commit `abfbb10`). Tart and Orchard remain
under Fair Source 0.9 with hard use limits (100-CPU and 4-device respectively); Tart
remains actively maintained (release 2.32.1 cut 2026-04-11, four days post-
acquisition). Apple shipped first-party `container` for Linux containers on macOS
26 — explicitly Linux-only. Anthropic shipped Claude Computer Use Agent on 2026-03-23
with an in-host Seatbelt sandbox. The OpenAI Agents SDK launched on 2026-04-15 with
eight sandbox providers — none of them ship a first-class Apple-Silicon-native
macOS-guest sandbox. Daytona raised $24M to build cloud agent sandboxes. CUA (trycua)
is on a hosted-platform trajectory with Lume as substrate.

The opportunity is that **the local Apple-Silicon macOS-guest sandbox tier is
structurally underserved at the exact moment agent demand for it is exploding.** The
risk is that **OpenAI is the most likely entity to fill it**, with the Cirrus team
already in-house and publicly working on agent infra. cove's 12-month strategy is to
own the *local-first, user-owns-the-substrate* slice of that tier as **the local
macOS eval runner**: a substrate optimized for high-throughput clean-state macOS
eval iterations via named-snapshot fork (`cove fork`) and per-eval user-account
isolation. Specific throughput numbers are deferred until the Q2 2026 benchmark and
the Q2 soft-reset isolation test suite produce receipts. The OSS core stays MIT and
self-serve; the *fleet, queue, snapshot pool, dashboard, and hosted control plane*
are paid layers above that core. The Tart/Cirrus CI shutdown opens a window for
license-conscious migrants (Tart is Fair Source 0.9 with a 100-CPU cap), but CI
shops and agent-eval teams are sufficiently different buyers (different throughput
shape, different result shape, different SLA expectations) that cove leads with the
eval-runner cohort and treats Tart/Cirrus migration as best-effort, not as the
primary motion. Sustainability becomes a concrete tiered-product plan, with explicit
labelling of which parts are validated and which are still bets — see Honesty
notes near the end.

## Positioning statement

**cove is the local-first macOS eval runner.**

Long form: *cove is the local-first, MIT-licensed, Apple-Silicon-native macOS-guest
substrate for high-throughput agent and software evals. The wedge is two technical
primitives — named multi-snapshot VM-state save/restore via APFS clonefile + VZ
`.vmstate` exposed as a single atomic `cove fork` API, and per-eval user-account
isolation inside a warm guest as a faster soft-reset alternative to full snapshot
rollback. The OSS core is for individuals and small teams running evals by hand;
fleet orchestration, snapshot pools, and the hosted control plane are paid. cove is
what AI agent labs, macOS app QA teams, and privacy-sensitive operators reach for
when they need isolated, reproducible macOS execution and either cannot or do not
want to send their data to a cloud sandbox — including a future first-party OpenAI
macOS sandbox if and when it ships.*

Five-word version: **"the local macOS eval runner."**

The "local-first" is load-bearing. The OpenAI Agents SDK partner list has no
Apple-Silicon-native macOS-guest provider today; that vacancy is a transient
opportunity (likely to close in 3–6 months as the ex-Cirrus team at OpenAI ships
agent infrastructure). The durable wedge is **user owns the substrate** — local data,
local models, no exfiltration to cloud sandboxes. Privacy-sensitive operators
(legal-tech, healthcare AI, fed/gov) are the cohort that *cannot* defect to a
hosted sandbox even if it ships flawlessly; cove's 12-month plan treats them as
day-1 messaging, not Q4 outreach.

What the throughput claim is not: a measured result. cove's existing `disk-snapshot`
and `.vmstate` primitives plausibly support a sub-3s `cove fork` on M2/M3 with a
4GB-RAM/30GB-disk image, but that ship-gate must be benchmarked in Q2 against a
defined reachability predicate (vsock-agent reachable, not "boot success"). The
soft-reset user-account primitive depends on TCC, System Keychain, AppleID
throttling, GlobalPreferences, FileVault SecureToken propagation, and
orphaned-daemon residue all behaving correctly across a delete/recreate cycle —
each of which is currently unproven. See "What's measured / what's hypothesis"
below.

What this commits cove to:
- Two-tier isolation: **hard reset** (VM-state snapshot rollback) and **soft reset**
  (user-account swap inside a warm guest). Both must be first-class in the API.
- macOS *guest* support stays primary; Linux guests stay first-class but secondary.
- HTTP / MCP / programmatic-control surface is product, not toy. The eval-runner
  buyer expects an API, not a CLI demo.
- Snapshot / fork / reset / per-user-account primitives that an eval orchestrator
  consumes.
- A public, signed image distribution channel for the canonical eval base images.
- "Why local" stays a sentence cove can say without flinching: privacy, hardware-
  fidelity, cost, and offline.

What this commits cove to *not* being:
- Not a cloud sandbox provider. Cove will not race Daytona/E2B/Modal on cloud
  microVM economics.
- Not a vendor-specific agent platform. Cove stays substrate; harness logic stays out.
- Not Apple's `container`. cove will not chase the Linux-container-on-Mac use case.
- Not a Lume reskin. cove imports Lume, doesn't re-export it.
- Not an MDM / endpoint-management product. cove provisions VMs, not employee Macs.

What this gives up by *not* picking:
- *"Hobbyist VM tool"* (the UTM seat). cove is fine for hobbyists, but ergonomics
  decisions optimize for eval throughput, not enthusiasts running 30 emulated CPU
  architectures.
- *"Enterprise CI replacement"* as primary motion. Anka and MacStadium Orka own the
  enterprise-fleet sales motion; cove competes on OSS+self-host, not on procurement.
- *"All-in-one agent platform"* (the CUA seat). cove is *one* layer; a harness still
  lives on top.
- *"Generic macOS dev workstation tool."* `cove up -vzscripts workstation` still
  works and stays maintained, but is no longer the headline.

## The cohort: who actually buys this in the next 12 months

The eval-runner positioning sharpens the buyer. There are four primary cohorts and
one secondary (CI refugees, treated as best-effort migration not a primary motion).
Each cohort shares the operational verb *"I need to run N iterations of X on a
clean Mac, repeatedly,"* but they differ in what they need from cove and what
license posture they tolerate. License arithmetic matters: Tart and Orchard are
Fair Source 0.9 with hard usage limits (100-CPU and 4-device respectively), so the
cohorts that already exceed those caps are pre-qualified for the cove pitch.

1. **Privacy-sensitive agent operators (promoted to day-1).** Legal-tech, healthcare
   AI, federal/gov AI contractors that *cannot* send agent-controlled-environment
   data to E2B / Daytona / cloud sandboxes — and *cannot* defect to a future
   first-party OpenAI macOS sandbox if it ships. Local-first is a hard compliance
   constraint, not a preference. License posture matters: an MIT substrate passes
   FedRAMP/HIPAA/legal review more cleanly than Fair Source 0.9 or FSL-1.1. They
   will pay for **a self-hosted control plane SKU with audit logging and SSO.**
   Day-1 paying targets: AI-tooling contractors with FedRAMP / HIPAA /
   regulated-data exposure. **This is the durable cohort** — the one whose buy-
   decision survives OpenAI shipping a hosted equivalent.

2. **AI agent eval/RL teams targeting macOS computer-use.** Includes: cua-bench-style
   academic and corporate evaluators, internal eval teams at AI labs (Anthropic,
   smaller frontier labs, agent startups). They run hundreds of thousands of agent
   rollouts per week and the macOS-targeted subset is bottlenecked by lack of a
   fast clean-Mac primitive. License calculus: at thousands of CPUs of usage,
   Fair Source 0.9 commercial fees are real money. They will pay for **fleet,
   queue, snapshot pool, structured-results dashboard.** Day-1 paying targets:
   computer-use benchmark publishers (cua-bench, OS-World contributors), companies
   building Codex-/Devin-style macOS-aware coding agents. Risk to this cohort:
   if OpenAI ships a first-party macOS sandbox, the *non-privacy-sensitive*
   subset will defect to it for hosted convenience.

3. **macOS app QA / release-engineering teams.** Install/uninstall test matrices,
   smoke-test against fresh macOS state, OS-update regression testing. Today's
   options are: a closet of physical Macs (slow), Anka (expensive, contract), or
   stale snapshots managed by hand. They will pay for **CI integration + parallel
   per-PR eval matrices + failure-replay.** Day-1 paying targets: midsize indie Mac
   software shops (Things, Bear, Setapp catalog members), release-engineering teams
   at Mac-first SaaS (Linear, Raycast, Arc/Browser Company successors).

4. **Security / malware-research teams.** Detonate suspicious macOS software on a
   clean state every time, capture artifacts, snapshot-restore. License posture
   matters less; technical fit (clean detonation per iteration via `cove fork`)
   matters more. They will pay for **the hosted control plane + private signed
   agentkit images.** Day-1 paying targets: indie Mac AV/threat-research shops
   (Patrick Wardle adjacent), incident-response consultancies that handle
   Mac-targeted malware.

5. **Tart/Cirrus migration cohort (best-effort, not primary motion).** GitHub
   Actions self-hosters and Buildkite shops on Tart who are exceeding the 100-CPU
   Fair Source limit, are uncomfortable with OpenAI now owning the upstream, or
   need a maintained migration path post-Cirrus-CI-shutdown (2026-06-01). cove
   ships a one-shot Tart-image importer in Q2; ongoing CI fleet support is **not
   a primary product motion** in the 12-month window. The CI buyer's iteration
   shape (5–30 min builds, exit-code result, YAML config) and the eval buyer's
   shape (seconds-to-minutes rollouts, structured trace+screenshot result, Python
   eval framework) are sufficiently different that conflating them produces a
   worse product for both. cove leads with eval-runner; CI shops who happen to
   want what cove provides get it as an incidentally-supported workload.

Cohorts cove is *not* targeting in the 12-month window:
- Mass-market hobbyist VM users (UTM owns them).
- Enterprise iOS-build CI shops with existing Anka contracts (different motion).
- Windows-on-Mac users (UTM/Parallels own this).
- Generic dev-workstation users — `cove up -vzscripts workstation` still works,
  but they are not the buyer for paid features.

### A specific recruiting note: ex-Cirrus engineers

With Cirrus Labs joining OpenAI, several engineers know macOS Apple-Silicon
virtualization deeper than anyone outside Apple itself. Some fraction will
eventually want to work on substrate, not on agent product, and may not want to
do that at OpenAI forever. They are the world's most informed potential cove
contributors. The cove maintainer should keep a friendly, public, no-pressure
door open. This is potentially the highest-leverage move in the whole 12-month
plan.

## Competitive landscape

### Matrix

| Player | Tier | Apr 2026 status | macOS guests | Local-first | License | Maintainer risk | Cove threat level |
|---|---|---|---|---|---|---|---|
| **Apple `container`** | Linux containers on Mac | 26.1k★ active, v0.11.0 | No (explicit non-goal) | Local | Apache-2.0 | Apple | **Low** — different layer |
| **Lume (trycua)** | Local VM runtime | Active, MIT, framed as CUA component | Yes | Local | MIT | YC startup pivot | **High** — direct competitor |
| **CUA (trycua)** | Agent platform | YC-backed, "cua.ai cloud" coming, MIT | Yes (via Lume) | Mixed | MIT (today) | Will go commercial | **High** — closes via SaaS |
| **Tart (cirruslabs → OpenAI)** | macOS CI / VM runtime | **Active maintenance** post-acquisition (release 2.32.1 dated 2026-04-11); Cirrus team's stated focus at OpenAI is "Agent Infrastructure" | Yes | Local | **Fair Source 0.9, 100-CPU cap** (`cirruslabs/tart/LICENSE`); commercial license required above cap; not relicensed post-acquisition | Cirrus team integrating into OpenAI agent-infra; long-term cadence unclear | **High** — both as direct comparable and as the wedge that closes if OpenAI ships a successor |
| **Orchard (cirruslabs → OpenAI)** | Tart orchestration tier | Mature multi-year codebase: Controller+Worker, scheduler, REST API, multi-VM-per-host | Via Tart | Local (cluster) | **Fair Source 0.9, 4-device cap** (`cirruslabs/orchard/LICENSE`) | Same as Tart | **High** — this is the prior art for cove's hosted control plane |
| **tart-guest-agent** | Guest-side agent | Active | Mac+Linux | Local | **FSL-1.1-Apache-2.0** (2-year commercial restriction, then Apache) | Same as Tart | Medium — same architecture as cove's vz-agent (vsock RPC, daemon+agent two-process) |
| **Anka (Veertu)** | Enterprise macOS CI | Active, paid | Yes | Local | Commercial | Stable enterprise | Low — different motion |
| **UTM** | Hobbyist GUI | Active, App Store $9.99 | Yes | Local | Apache-2.0 | Stable | Low — different cohort |
| **OrbStack** | Container/Linux Mac UX | Active, $8/mo | No (Linux machines, not macOS) | Local | Closed-source | Stable | Low — adjacent, tone-model |
| **Daytona** | Cloud agent sandbox | $24M Series A, sub-90ms, macOS Early Access | Yes (cloud Mac Mini) | Cloud | Apache-2.0 + SaaS | Funded | **High** — well-funded crossover |
| **E2B / Modal / Runloop / Blaxel / Vercel / Cloudflare** | Cloud Linux microVM | OpenAI Agents SDK partners | No | Cloud | Mixed | Funded | Low — different OS |
| **Anthropic sandbox-runtime** | In-host process sandbox | OSS npm package | Process-level only | Local | OSS | Anthropic | Low — different tier |
| **Claude Code computer use** | In-host on actual desktop | GA | Same desktop | Local | SaaS | Anthropic | Medium — substitute for low-stakes |
| **MacStadium Orka / Mac Mini cloud** | Hosted Apple Silicon | Active, paid | Yes | Cloud | Commercial | Stable | Low — referral path |

**Key sources backing this matrix:**

- [/tmp/cove-research-update-2026-04-25.md][r1] (notebook source, partially
  reconstructed; specific dates flagged for re-verification) — OpenAI ↔ Cirrus deal,
  Apple `container` Linux-only stance, Daytona Series A, OpenAI Agents SDK partner
  list, Anthropic sandbox-runtime semantics, OrbStack pricing.
- [/tmp/cove-competitor-landscape-2026.md][r2] (notebook source) — original
  research seed.
- [/tmp/cirruslabs-review.md][r4] (notebook source) — direct source review of
  `cirruslabs/tart`, `cirruslabs/orchard`, and `cirruslabs/tart-guest-agent` with
  file:line citations for license, snapshot model, OCI surface, and architecture.
  This is the receipts trail for the Tart and Orchard rows in the matrix above.
- [011-beat-lume-roadmap.md][r3] — internal stance on lume and the engineering
  release themes (0.1 → 0.4) cove's marquee features map onto.

[r1]: ../../../tmp/cove-research-update-2026-04-25.md
[r2]: ../../../tmp/cove-competitor-landscape-2026.md
[r3]: 011-beat-lume-roadmap.md
[r4]: ../../../tmp/cirruslabs-review.md

### The four real fights

cove will be *talked about* against everyone in the matrix. cove only actively *fights*
four:

**Fight 1: vs Lume / CUA (today).** Both are MIT, both target Apple Silicon, both have
HTTP+MCP. cove's wedge is named multi-snapshot lineage + per-eval user-account
isolation (planned, contingent on validation) + deterministic automation (vzscript +
disk injection vs cloud-init only) + the explicit "not going commercial" license
posture. CUA's roadmap is to go commercial (cua.ai cloud); cove's roadmap is to *be
the fork-resistant MIT alternative when they do*. Asymmetric interop (cove pulls
Lume images; default push is cove-format) is the bridge. **Note**: APFS clonefile is
not exclusive to cove — Tart's `clone` also uses it (`cirruslabs/tart/Sources/tart/
Commands/Clone.swift:77`); cove's wedge is the *named multi-snapshot model and atomic
`cove fork` API*, not the underlying primitive.

**Fight 2: vs the *transient* absence of a macOS-guest provider in the OpenAI Agents
SDK partner list.** Eight providers, none macOS-native. Daytona has macOS in Early
Access only and not Apple-Silicon-local. cove ships an Agents-SDK adapter and an
Anthropic sandbox-runtime adapter so any harness can target a cove VM in 5 lines of
Python. **The wedge is transient**: OpenAI acquired the Cirrus team specifically to
work on "Agent Infrastructure" (announcement banner, `cirruslabs/tart` commit
`abfbb10`, 2026-04-07). A first-party `openai-agents-sdk[macos-sandbox]` provider
is plausible inside a 3–6 month window. cove's response is *not* to race OpenAI to
ship a hosted sandbox; cove's response is to position adapters as the *local*
escape hatch — for the cohort that needs the Agents SDK ergonomics but cannot or
will not send their data to OpenAI's hosted offering. See T0 below.

**Fight 3: vs Tart and Orchard on the *license* axis (not on maintenance).** The
prior draft described Tart as a "zombie." Source review (`/tmp/cirruslabs-review.md`)
shows Tart is actively maintained — release 2.32.1 cut 2026-04-11, four days post-
acquisition. The real wedge is license arithmetic: Tart is **Fair Source 0.9 with a
100-CPU cap** (`cirruslabs/tart/LICENSE:9-10`); Orchard is **Fair Source 0.9 with a
4-device cap** (`cirruslabs/orchard/LICENSE:1-9`); tart-guest-agent is
**FSL-1.1-Apache-2.0** (commercial-restricted for 2 years, Apache afterward,
`cirruslabs/tart-guest-agent/LICENSE:1-7`). cove is MIT. For users exceeding the
caps, requiring OSI-license compliance, or running in regulated industries with
strict redistribution rules, cove is the only option in this comparison without a
commercial-license trigger. cove publishes a "Tart migration" guide and a
best-effort one-shot Tart-format OCI image importer in Q2; ongoing CI fleet support
is *not* a primary product motion. Migration window claims are removed from the
prior draft — Tart maintenance has not lapsed and may not lapse for the foreseeable
future.

**Fight 4: vs Orchard on the *orchestration tier* (the prior art for cove's hosted
control plane).** Orchard already does what 012 originally framed `coveconnect.dev`
as doing in greenfield: Controller+Worker, scheduler with profiles
(`cirruslabs/orchard/internal/controller/api_cluster_settings.go:48`), multi-runtime
(Tart+Vetu), REST API at `/vms`, `/vms/{name}/exec`, `/vms/{name}/port-forward`,
`/workers`, `/service-accounts`, multi-VM-per-host scheduling, mature Ansible
playbooks. cove's hosted control plane competes on three specific axes: (a) MIT
license vs Orchard's Fair Source 4-device cap, (b) short-lifecycle eval scheduling
(Orchard's scheduler is built for long-running VMs with restart policies, not
"thousands of clean-state iterations per hour"), and (c) per-eval user-account
isolation as first-class API. **cove's hosted-control-plane pitch must articulate
those three wedges, not assume the orchestration tier is uncontested.**

**Fight 5: vs Apple `container` — *only at the boundary*.** Apple owns the Linux
container UX on Mac. cove does not contest that. Where they touch is: a developer who
wants both a containerized Linux service *and* an isolated macOS VM running it should
have one obvious tool that hands off cleanly. cove's `cove run -linux` stays first
class, but does not chase feature-parity with `apple/container` on Linux ergonomics.

### Lessons from OrbStack

OrbStack is the structural inspiration, not a competitor. What it proves:
- A single-Mac-only commercial product can sustainably charge $8/mo.
- Speed + native UX + transparency-of-pricing beats feature breadth.
- "Less than 0.1% background CPU" is a marketing line that works.
- A solo-feel team can ship and be profitable.

cove should adopt OrbStack's tone (quiet competence, native feel, no marketing speak)
and pricing instinct (single tier, transparent). cove should not adopt OrbStack's
closed-source choice — too much of cove's wedge depends on being the OSS, fork-
resistant, audit-yourself substrate.

## The 12-month roadmap

011 sets release themes (0.1 → 0.4). 012 keeps those themes, places them on a calendar,
and adds the marquee features that wire the strategy to the substrate.

### Calendar overview

| Quarter | Theme (from 011) | Marquee feature for *this* doc | Distribution moment |
|---|---|---|---|
| Q2 2026 (May-Jul) | Linux dev moat (0.2) | **F3 — Agents-SDK adapters + MCP polish** | "cove for Tart users exceeding the 100-CPU cap" announce; HN + Lobsters posts |
| Q3 2026 (Aug-Oct) | Build & caching moat (0.3) | **F2 — `cove build` + content-addressed cache + secrets** | "docker build for macOS VMs" announce; published agentkit images |
| Q4 2026 (Nov-Jan) | Shared-host & CI hardening (0.4) | **F1 — agent-grade snapshot/fork API**; *hosted control plane MVP deferred* | "cove fork: named multi-snapshot lineage" demo |
| Q1 2027 (Feb-Apr) | 1.0 stabilization | **F5 — agentkit registry GA + signed images**; **hosted control plane MVP** (deferred from Q4) | 1.0 release; tier-1 partner integrations |

The 011 release themes stand. What 012 layers on is *which features are non-negotiable
because they encode the agent-substrate positioning*. Features that don't appear here
can ship if there's room (e.g. linux nested KVM, vsock improvements, browser display
bridge), but they don't get sacrificed for *to make the marquees ship*.

### Marquee features (4 in 12-month scope, F4 deferred)

These are the features that, taken together, no other current player ships in the
same configuration. F4 (`cove record / replay`) is described below for
completeness but is deferred to 1.0+ for solo-maintainer scope reasons. Each
in-scope marquee is scored against the agent-substrate thesis (does it only make
sense if cove is an agent substrate?), the moat-widening claim, and
solo-shippability. Scoring borrowed from notebook pressure-test, condensed.

#### F1 — Agent-grade snapshot/fork API (`cove fork`)

`cove fork --from <snapshot|running-vm>` returns a brand-new VM by combining APFS
clonefile (instant disk fork) and the existing `.vmstate` save/restore in a single
atomic API call. Exposed via the `cove serve` HTTP gateway as
`POST /v1/vms/:name/fork` and over MCP as `fork_vm`. The forked VM gets its own
control socket, network namespace (vmnet isolation), and identity files. Named
multi-snapshot lineage: snapshots are addressable by name and can be referenced
across forks (e.g. `cove fork --from base@clean`).

**Why this is differentiated:** the underlying primitives are not exclusive to cove
— Tart's `clone` uses APFS copy-on-write (`cirruslabs/tart/Sources/tart/Commands/
Clone.swift:77`), and Tart has VZ `.vmstate` save/restore on macOS 14+
(`cirruslabs/tart/Sources/tart/Commands/Run.swift:464-471, 556-565`). What cove
does that Tart does not is wrap them in (a) a *named multi-snapshot* model
(Tart's CLI has only single-state suspend/resume per VM; no `tart snapshot save
NAME`), (b) a *single atomic `cove fork` API* (Tart requires `clone` then start —
two steps, no live state), and (c) HTTP + MCP exposure for agent harnesses to
drive forks programmatically. Lume doesn't have any of the three. Apple `container`
is Linux-only. Cloud sandboxes can fork in cloud, but not on the user's local
Apple Silicon.

**Why this matters for agents:** the canonical agent loop is "snapshot a clean Mac,
have the agent do work, snapshot the result, fork from the clean snapshot for the
next trial." Without low-latency fork, the eval/replay loop is too slow to be
useful.

**Scope:** ~2 engineering weeks for solo. Builds on existing `disk-snapshot` and
`snapshots` code. The API shape is the work; the primitives exist.

**Ship gate (revised, falsifiable):** on M2/M3 host, with a 4 GB-RAM, 30 GB-disk
macOS-15 image, `cove fork --from <snapshot>` produces a forked VM that is
**vsock-agent-reachable in <3s wall-clock**, with the fork's writes isolated from
the parent's disk, and the parent able to remain running. Sub-3s is the honest
ship-gate — the prior <2s number was aspirational and is downgraded pending
benchmark. (See "What's measured / what's hypothesis" below.)

#### F2 — `cove build` (the docker-build-for-macOS pitch)

011 already contains the engineering plan (`003-cove-build-oci-caching.md`).
This roadmap commits to it as the **single deepest moat** cove ships in 2026.

**Why this matters for agents:** every agent harness needs reproducible base
environments. "I run Codex on macOS 15 + Xcode 16 + my custom dev tools" should be
one `cove pull <user>/macos-codex:v3` away. `cove build` is how those images get
produced.

**Ship gate:** `003-cove-build-oci-caching.md` ship gate.

**Risk this roadmap accepts:** cross-machine cache stability is a benchmark gate
([004-churn-benchmark-harness.md](../004-churn-benchmark-harness.md)); `cove build` ships
even if cross-machine `--cache-from` is documented as deferred. Local-only cache hits
are still a category-defining feature.

#### F3 — Agents-SDK + sandbox-runtime adapters (the discoverability layer)

A 5-line Python integration that lets a developer write:

```python
from openai_agents import Agent
from cove_sandbox import CoveSandbox

agent = Agent(model="gpt-5", sandbox=CoveSandbox(image="ghcr.io/tmc/macos-codex:latest"))
agent.run("...")
```

…and have the agent execute in a real, local, Apple-Silicon macOS VM, with full
filesystem and network isolation, programmatic snapshot/fork, and screen access.

What ships:
- A `cove-sandbox` Python package (PyPI) implementing the OpenAI Agents SDK's
  `Sandbox` interface.
- An `@cove/sandbox-runtime` shim that *additionally* satisfies the Anthropic
  `sandbox-runtime` interface, so Claude Code users can swap in cove for `bash` tool
  isolation when they want full-VM isolation rather than Seatbelt.
- An MCP server that already exists (`cove serve --mcp`); 012 promotes it from
  experimental to stable.
- Ten worked examples, including: agent eval, code-runner sandbox, Computer-Use
  benchmark replay, CI-as-agent.
- A "cove for the OpenAI Agents SDK" landing page with a 60-second demo.

**Why this is differentiated *today*:** none of the eight Agents-SDK partners
offer an Apple-Silicon-native local-first macOS-guest substrate. Daytona is
closest and gated. This adapter is how cove becomes the answer to *"how do I
sandbox an agent that needs to use a Mac?"* — for as long as the partner-list
vacancy lasts. See T0 for why this wedge is transient and what cove's durable
position is in the (likely) world where OpenAI ships a first-party macOS
sandbox.

**Ship gate:** the 5-line example actually works against current `gpt-5` /
`claude-sonnet-4.6` Agents-SDK releases on a clean install.

#### F4 — `cove record` and `cove replay` (deferred to 1.0+)

The original Phase-3 candidate was "deterministic full-system replay." Even a
narrowly-scoped action-level version is no longer a 12-month commitment. **012
defers F4 to 1.0+.** Rationale: a solo maintainer's Q4 already contains F1
ship-gate work plus the Q2 soft-reset isolation validation results to act on;
the hosted control plane MVP itself was deferred from Q4 to Q1 2027 (see
Sustainability) for the same scope-vs-solo reason. Adding a record/replay
shipgate would compound the overload.

If F4 reappears in scope, the action-level scope is the right one: `cove record`
captures every control-socket event (mouse, keyboard, exec, file I/O, screenshot,
vzscript step) plus periodic snapshots, and `cove replay` re-runs those events
against a forked clean VM, surfacing divergence. The eval-debugging value is real;
the scope-vs-solo-maintainer math is not. Q4 effort goes to F1 hardening and
acting on the Q2 soft-reset validation findings instead.

**Note on the "agent debug" problem this addresses:** the #1 unsolved problem in
computer-use evals is "my agent passed yesterday, fails today; what changed?" In
the absence of F4, cove's interim answer is the existing control-socket logging
(every event is timestamped) plus `cove disk-snapshot save` of the failed run's
disk state — together sufficient to bisect agent behavior changes manually. F4
turns that into a one-command operation; the manual workflow is acceptable for
1.0.

#### F5 — Public agentkit image registry (`registry.cove.run` or similar)

A small, opinionated registry of signed, content-addressed cove-format images for the
common agent base environments:
- `cove/macos-15-base` (clean macOS 15 + vz-agent + provisioned `dev` user)
- `cove/macos-15-xcode` (+ Xcode CLT + Homebrew)
- `cove/macos-15-codex` (+ Node + Python + common AI agent libraries)
- `cove/macos-15-cua-bench` (the cua-bench environment, for benchmark replay)
- `cove/ubuntu-2404-base` (Linux equivalent for non-mac agent workloads)

Images are produced by `cove build` (eat-our-own-dogfood) on a public CI runner. They
are signed (sigstore / cosign) and pinned to `cove-build` digests. The `cove
agentkit` subcommand pulls and forks one as a one-liner:

```bash
cove agentkit run macos-15-codex --user dev
```

**Why this matters for distribution:** every other agent-sandbox provider has a
catalog: E2B has templates, Daytona has sandboxes, Modal has images. cove's catalog
is *signed reproducible Apple-Silicon-native macOS images*. That's the discoverability
moment — someone hits the catalog page and tries cove in 30 seconds.

**Why this is asymmetric vs Lume:** Lume has GHCR push/pull but no curated catalog
and no signed-image story. cove uses Lume's public IO path (interop) and adds the
opinionated layer Lume doesn't have.

**Ship gate:** at least 5 published images, all reproducibly built on CI; `cove
agentkit run macos-15-codex` reaches a logged-in desktop in <60s on first run.

### Quarterly milestones

#### Q2 2026 (May–Jul) — *theme: "the maintained alternative"*

The Cirrus *CI* shutdown (not Tart maintenance — Tart is still maintained) opens a
distribution window for license-conscious migrants. Use it without
overcommitting.

**Must ship by end of Q2:**
- 0.2 release (closes blockers in `docs/blockers-next.md` — `cove up` fresh install,
  Lume-format pull, `serve` discovery scope).
- F3 v1: PyPI `cove-sandbox` package implementing the OpenAI Agents SDK `Sandbox`
  interface, with three end-to-end examples in CI.
- "cove for Tart users" docs + migration guide + best-effort one-shot Tart-image
  OCI importer. Honest framing: cove is for Tart users who exceed the 100-CPU
  Fair Source cap, want OSI-license compliance, or are uncomfortable with
  OpenAI now owning the upstream — *not* a refugee migration from a zombie
  project.
- A first set of agentkit images (F5 v1: 2 images, manual sign).
- HN + Lobsters launch posts. Submission to GoodCommit, AppleSilicon newsletter,
  Console.dev. Demo video <2 minutes.
- **Validation milestones (non-negotiable for the eval-runner positioning):**
  - F1 benchmark on M2/M3: measure `cove fork` time-to-vsock-agent-reachable
    under a defined workload (4 GB RAM, 30 GB disk, macOS 15). Number goes in
    public docs, replacing the "thousands per hour" claim.
  - Soft-reset isolation test suite: TCC residue, System Keychain residue,
    AppleID throttling, GlobalPreferences leakage, FileVault SecureToken
    propagation across N user lifecycles, orphaned-daemon residue. Each is
    pass/fail/documented-limit. Results determine whether per-eval
    user-account isolation is a *real* primitive or a documented limit. See
    "What's measured / what's hypothesis" below.
- USPTO trademark search on "cove" — must clear before signed images go to
  any public registry under that name.

**Distribution actions:**
- Open `cove` Discord (small, founder-led for now).
- Reply to ongoing Tart/Cirrus migration discussions on GitHub issues and
  Reddit threads with concrete migration guides — not marketing. Don't
  trash-talk Tart; framing is "here's what's different and when it matters
  for you."
- Submit `cove-sandbox` to OpenAI Agents SDK examples directory.
- Open public, no-pressure recruiting door for ex-Cirrus engineers (see
  cohort-section recruiting note).

**Ship gate for Q2:**
- A user exceeding Tart's 100-CPU Fair Source cap can switch to cove on a
  GitHub Actions self-hosted macOS runner in <30 minutes following published
  docs.
- A user can run an Agents-SDK-compatible agent against a cove VM in 5 lines
  of Python.
- The F1 benchmark and soft-reset isolation results are *published*, even if
  some are negative — the eval-runner positioning's load-bearing claims have
  receipts after Q2, not just marketing.

#### Q3 2026 (Aug–Oct) — *theme: "build the moat"*

**Must ship by end of Q3:**
- 0.3 release with `cove build` (per [003-cove-build-oci-caching.md][cb]).
- F2 ship-gate met: cache-hits skip guest exec; secrets don't leak into pushed
  layers; cross-machine cache stability is documented (passing or deferred per the
  churn-benchmark harness).
- F5 v2: 5 published agentkit images, all reproducibly built on CI, all signed.
- F3 v2: Anthropic `sandbox-runtime` adapter ships; ten worked examples in
  `examples/agents/`.

[cb]: 003-cove-build-oci-caching.md

**Distribution actions:**
- "docker build for macOS VMs" launch post — long-form write-up with benchmarks.
- Conference talks / lightning talks at: AppleDev meetups, GopherCon (cove is pure
  Go), AI engineer events.
- Sponsor adoption: try to land cove in *one* well-known agent harness (Codex
  alternative, evaluation lab, Computer-Use benchmark group). Evidence-of-traction
  for sustainability conversation.

**Ship gate for Q3:**
- `cove build` produces a published image that reproduces on a fresh machine bit-
  for-bit (or with a documented divergence boundary).
- An external project links cove as a documented sandbox option.

#### Q4 2026 (Nov–Jan) — *theme: "the agent primitives"*

**Must ship by end of Q4:**
- 0.4 release with shared-host hardening (per 011).
- F1 ship-gate met: `cove fork` returns a vsock-agent-reachable forked VM in <3s
  on M2/M3 with a 4 GB-RAM/30 GB-disk image (revised from <2s, see F1 above).
- The Q2 soft-reset-isolation validation (TCC, FileVault SecureToken, AppleID
  throttling, residue, Keychain) has either passed or has been documented as a
  hard limit and the eval-runner positioning has been revised accordingly. This
  is a non-negotiable Q4 gate.

**Deferred from Q4 to 1.0+:**
- F4 `cove record / replay` — out of scope for 12-month plan.
- Hosted control plane MVP (`coveconnect.dev`) — moved to Q1 2027 to give the
  solo maintainer breathing room. See Sustainability section for the revised
  hosted-tier path.

**Distribution actions:**
- "Named multi-snapshot lineage on macOS" launch post.
- Day-1 messaging refresh for the privacy-sensitive cohort (legal-tech,
  healthcare AI, gov-fed AI contractors) — they are the durable buyer in the
  face of OpenAI shipping a hosted macOS sandbox.
- Public benchmark write-up: cove vs Tart vs Lume on the same workload, same
  hardware, same OCI image. Numbers, not vibes.

**Ship gate for Q4:**
- F1 ship-gate met (above).
- An external partner uses `cove fork` in a published eval pipeline.
- The Q2 soft-reset-isolation validation has either passed or led to documented
  positioning revision.

#### Q1 2027 (Feb–Apr) — *theme: "1.0"*

**Must ship by end of Q1 2027:**
- cove 1.0 release. Public commitment to a stability window for the HTTP/MCP API
  and the OCI image format.
- F5 GA: agentkit registry has signed image production, an SLA, and a content
  policy. (GHCR-backed for v1; self-hosted `registry.cove.run` only if pulls
  exceed GHCR's free tier.)
- A second deeply-integrated partner (an agent harness or eval framework that
  ships cove as their default macOS sandbox).
- **Hosted control plane MVP** (deferred from Q4): a managed gateway that lets a
  remote agent harness reach a fleet of cove VMs running on a customer's own
  Macs. OSS gateway, paid hosting tier. Pricing TBD after first 5 customers
  (see Sustainability for why the prior OrbStack-style $8/seat anchor was
  removed).
- Sustainability funded: at least one of (hosted control plane has 3 paying
  customers; GitHub Sponsors floor at $1.5k/mo; one consulting contract).
  Conservative numbers; the prior $2k/mo and 5–10 customers were overshoot.

**Ship gate for Q1 2027:**
- 1.0 cuts cleanly from main without a deferred-blockers list longer than 011's.
- The product can be honestly described as "the local macOS AI agent sandbox" and
  that description survives a hostile reading — including the readings the doc
  fails today (see Honesty notes).
- USPTO trademark search has cleared "cove" (or rebrand executed before 1.0
  signed images go to a public registry under the cove name).

## Non-goals (12-month)

cove **will not** in this 12-month window:

1. **Build a hosted cloud sandbox business.** Daytona / E2B / Modal own that
   economics. cove's hosted offering is *control-plane*, not *compute*. Customers
   bring their own Macs.
2. **Race apple/container on Linux container ergonomics.** Linux guests stay first-
   class but not the headline.
3. **Implement deterministic-replay-of-the-OS.** Action-level replay only (F4).
4. **Ship a closed-source tier.** All current OSS surface stays MIT. New revenue
   surfaces (control plane, support) are layered above.
5. **Build vendor-specific LLM loops in core.** Per 011 — cove stays model-neutral.
6. **Pursue Anka-style enterprise procurement sales motion.** No outbound sales
   team in 12 months. Self-serve and developer-led only.
7. **Compete with UTM on hobbyist breadth.** No QEMU back-end. No 30 emulated
   architectures. cove stays Apple Silicon native.
8. **Replace `cove run -gui` with browser VNC.** Per 011 — native AppKit GUI stays
   primary; browser display is optional only.
9. **Ship Windows guest support beyond the existing experimental stub.** Out of
   scope for the agent-substrate thesis.
10. **Take VC funding that requires a non-OSS pivot.** See Sustainability.

## Threats and responses

### T0 — OpenAI ships a first-party macOS sandbox in the Agents SDK partner list

**12-month likelihood: high (~50–70%).** The Cirrus Labs team — the world's most
informed Apple-Silicon-virtualization team outside Apple — joined OpenAI on
2026-04-07 with the explicit stated focus of "Agent Infrastructure"
(`cirruslabs/tart` commit `abfbb10`). The Cirrus CI shutdown 2026-06-01 (six weeks
notice for a paying CI service) is bandwidth being aggressively reallocated. The
OpenAI Agents SDK partner list has eight providers but no Apple-Silicon-native
macOS-guest provider — exactly the wedge cove identifies. The path of least
resistance for OpenAI is first-party, not partnership.

This is a **bigger, faster threat than the original T1** (Apple shipping macOS
guests in `container`).

**Response:**
- Treat the partner-list-vacancy wedge as **transient**. Adapter shipgate (F3)
  matters as a wedge while it lasts, but the durable position is "local-first /
  user-owns-substrate."
- Lead messaging with the privacy-sensitive cohort that *cannot* defect to a
  hosted OpenAI macOS sandbox even if it ships flawlessly (legal-tech,
  healthcare AI, fed/gov contractors).
- Document the *isolation hierarchy*: when OpenAI's hosted sandbox is the right
  answer, when local cove is the right answer, when in-host Anthropic
  sandbox-runtime is enough. cove's messaging benefits from telling buyers when
  *not* to use cove.
- Keep cove model-neutral. cove's adapter for the Agents SDK should also work
  for any future open Agents-SDK-compatible harness. Don't tie the wedge to one
  vendor's continued non-shipping.
- Maintain the Cirrus alumni recruiting posture (see "Recruiting note" above).
  If even one of those engineers wants to keep working on macOS-AS substrate
  rather than OpenAI agent product, they are the highest-leverage contributor
  cove will ever get.

**Trigger to revisit positioning:** if a `pip install
openai-agents[macos-sandbox]` works against a real Apple-Silicon backend before
Q4 2026, cove pivots its messaging to local-first / privacy-only and accepts
that cohort #2 (general AI eval) partially defects.

### T1 — Apple ships macOS-guest support in `apple/container`

**12-month likelihood: low (~20%).** Apple `container` is currently Linux-only by
explicit design. macOS guest support requires Apple to publish private
`_VZ*` APIs (e.g. `_VZPCIDeviceConfiguration` for GPU) which they have signaled they
*may* publish on their own timeline, but doing it inside `container` would muddy
that product's "Linux containers replacing Docker Desktop" story.

**Response:**
- Stay up-stack of Apple's Linux-container UX. cove's wedge is macOS guests, agent
  control, GUI automation, OCR, vzscript. None of those go away if `container`
  expands to macOS guests.
- Make sure cove can *complement* `container`: `cove run --linux-via-container` as
  a supported runtime if it's faster than VZ direct in a future release.
- If Apple does ship macOS-guest support inside `container`, cove pivots its
  positioning toward the *agent substrate* (UI automation, snapshots, MCP, Agents-
  SDK adapters) rather than the *VM runtime*, and supports Apple's runtime
  underneath.

### T1.5 — Apple-platform release-cadence exposure

**12-month likelihood: medium-low (~25%).** Distinct from T1's specific scenario.
cove rides on Apple's `Virtualization.framework`, Apple's release cadence, Apple's
bug fixes, Apple's API stability. Two scenarios that this section is designed to
flag rather than predict precisely:

- macOS 27 ships with a `Virtualization.framework` breaking change. cove spends a
  quarter on compatibility work; the roadmap slips by that quarter.
- Apple ships a private API (e.g., `_VZPCIDeviceConfiguration` for GPU) that one
  competitor gets early through MFi/developer relations. cove cannot reach
  feature parity until Apple publicizes.

**Response:**
- Budget one calendar quarter of slack across the 12-month plan for unforeseen
  Apple-platform compat work. (This is honest planning, not contingency.)
- Build the agent-substrate features (F1, F3, F5) on top of stable VZ APIs cove
  already uses. Avoid taking dependencies on private `_VZ*` APIs that could
  vanish.
- Subscribe to Apple developer release notes, WWDC sessions, beta seeds — fast
  detection saves weeks.

### T2 — CUA goes commercial and pulls Lume into a closed/SaaS-only future

**12-month likelihood: high (~70%).** CUA is YC-backed, has no commercial pricing
yet, and is preparing "cua.ai cloud." Their public framing already positions Lume as
"one component of CUA" rather than the product. The most natural commercial move is
to keep Lume MIT but route the platform value to the cloud.

**Response:**
- Stay rigorously OSS and MIT. cove must be the obvious destination for a Lume user
  who feels squeezed.
- Keep asymmetric interop: cove pulls Lume images, cove's default push is cove-
  format, and a `--lume-compat` flag exists for explicit interop.
- Run a tasteful "cove for Lume users" migration page when CUA's commercial pivot
  becomes public. Don't preempt; don't trash-talk; *be the obvious next step*.
- Publish a public benchmark: cove vs Lume on the same workload, same hardware,
  same OCI image. cove's snapshot/fork/local primitives should win on key metrics.
- If CUA contributors get squeezed, *invite them*. Be the OSS-first home.

### T3 — Daytona / Runloop / E2B ship well-funded Apple-Silicon macOS-guest sandboxes

**12-month likelihood: high (~60%).** Daytona has macOS Computer Use in Early
Access today; with $24M they can absolutely fund Apple-Silicon-native local
sandboxes if the demand from agent customers materializes. E2B and Runloop are
similarly resourced.

**Response:**
- Stop trying to compete on cloud sandbox economics. The cohort cove serves is
  exactly the cohort that *cannot* use a cloud sandbox: local data, local models,
  privacy-sensitive workloads, offline-capable.
- Lean into local-first as a feature. Privacy-sensitive verticals (healthcare,
  legal-tech, fed-gov AI contractors) cannot exfiltrate to cloud sandboxes. cove
  is the only Apple-Silicon-native option in those segments.
- Be the BYO-substrate option that sits underneath any of the cloud players. If
  Daytona ships an Apple-Silicon-local "agent" SKU, cove offers to run as the
  local runtime under their orchestration.
- Compete on developer ergonomics: cove's `cove run` and `cove up` UX is faster
  to onboard than `daytona up` for a single-Mac use case.

### Bonus threat — T4: Anthropic's in-host sandbox normalizes "no-VM is enough"

**12-month likelihood: medium (~40%).** Anthropic's `sandbox-runtime` is open
source, fast, and free. For *most* coding-agent use cases, in-host Seatbelt
isolation is sufficient and the in-process latency is unbeatable.

**Response:**
- Don't compete with Anthropic at the in-host tier. cove's tier (full VM) only
  matters when in-host is insufficient: untrusted code, full reset semantics,
  hardware-fidelity testing, GUI workflows, long-running stateful environments.
- Document the *isolation hierarchy* on cove's marketing page. Show developers
  when Seatbelt is the right answer (and use cove only when it isn't).
- Ship the `@cove/sandbox-runtime` adapter so a Claude Code user can swap to cove
  when they want VM isolation, without migrating their config.

## Sustainability and funding

cove today is solo-maintained (Travis Cline). 011 is silent on how that changes; 012
isn't allowed to be.

### What "sustainable" means here

A 12-month plan that survives the maintainer being half-time, sick for two weeks,
or wanting to take a vacation without `~/.vz/vms` becoming abandonware. *Not* a plan
to grow a team to 30 people.

### Three-track plan

**Track A — OSS funding floor.**
- GitHub Sponsors, prominently linked on README.
- Goal Q3 2026: $500/mo recurring (covers infra, CI, domain, registry hosting).
- Goal Q1 2027: $1.5k/mo recurring (matches the Q1 2027 sustainability ship-gate
  in the milestones; this replaces the v0 $2k/mo aspiration with a more honest
  conservative number — see Honesty notes).
- Tactic: corporate sponsors who use cove in CI. If they're saving on Cirrus
  Runners or Tart commercial fees by using cove + their own Mac Mini, ask for
  $50/mo back. No "Built with cove" attribution requirement.

**Track B — Hosted control plane (`coveconnect.dev` or similar).**
- OSS gateway code stays in the main repo (per 011's `cove serve` design).
- *Hosted* tier: a managed gateway with team auth, audit logging, and SSO.
  Customer brings their own Macs (Mac Minis in their office, MacStadium rentals,
  whatever); cove hosts the control plane that orchestrates them.
- **Prior art**: Orchard (cirruslabs) already does Controller+Worker, scheduler,
  multi-VM-per-host, REST API, multi-runtime. cove's hosted control plane
  competes with Orchard on three specific axes: (a) MIT vs Orchard's Fair Source
  4-device cap, (b) short-lifecycle eval scheduling (Orchard's scheduler is
  optimized for long-running VMs with restart policies, not for short-loop
  snapshot-restore at high concurrency), (c) per-eval user-account isolation as
  first-class API (Orchard does not expose user-account lifecycle as an API
  resource). The pitch is *not* "no one has built this layer" — Orchard exists,
  has years of polish, and works. The pitch is "Orchard's license forces
  commercial fees above 4 Macs, and Orchard's scheduler is not optimized for
  short-lifecycle eval workloads — assuming cove's Q2 benchmark and soft-reset
  validation bear out."
- **Pricing**: TBD after first 5 customers. The original draft anchored on
  OrbStack ($8/seat or $25/team), but OrbStack sells to a developer running it
  on their own laptop (one seat = one Mac); cove's hosted control plane sells
  to a team whose compute is N Mac Minis in a closet. The unit economics
  differ, and per-fleet pricing may be more honest than per-seat. Defer the
  number until the first 5 conversations are had.
- This is *not* a cloud sandbox. cove never runs the customer's VMs. The hosted
  service holds tokens, routes API calls, and maintains a fleet view.
- **Schedule**: MVP deferred from Q4 2026 to Q1 2027 to give the solo
  maintainer breathing room.
- Risk: doesn't scale enterprise without sales motion. Acceptable for 12 months;
  revisit at 1.0.

**Track C — Selective consulting / commercial support.**
- A clear "support" page for orgs that want priority bug-fix turnaround, custom
  vzscripts, or migration help.
- Capped at *one* contract per quarter to protect maintainer focus.
- Keeps cove honest about what large users actually need.

### What cove will *not* do

- **No dual-license rug-pull.** cove stays MIT. Forever. If a sustainability
  failure happens, the answer is reduced scope or maintainer transition, not a
  re-license to BSL/SSPL/Elastic.
- **No closed-source plugins.** Adapters and the agentkit registry stay OSS.
  Hosted-only features (audit logs, team SSO) live server-side, not as code that
  shipped to customers and got disabled.
- **No VC at terms that require closed-source pivot.** A small angel or "rolling
  fund" round is acceptable if the runway doesn't push toward enterprise sales
  motion. A traditional SaaS Series A is not — it forces cove into Daytona's
  game, which cove loses.
- **No "Built with cove" attribution requirement.** That kills adoption.

### Honest unknowns

- Will Track B (hosted control plane) actually convert? Unclear. The first 5
  customers are the experiment. If they don't convert, drop Track B and lean
  harder on Track A and consulting.
- Is the maintainer interested in growing into a CEO role? If no, the plan
  caps at "sustainable solo with one or two part-time contributors." That is
  fine. cove doesn't need to become Daytona to be successful.
- Does a co-founder show up? If yes, the calculus changes; if no, default to
  the sustainable-solo plan.

## What's measured vs what's a hypothesis

The original draft (v0) of this doc made several load-bearing claims with the
confidence of measured results. They are not measured results yet. v1 (this
revision) labels each one and commits Q2 2026 to producing receipts.

**Measured (existing primitives in the cove repo today):**
- APFS clonefile-based disk snapshots and restores work
  (`disk_bench.go`, `snapshots.go`).
- VZ `.vmstate` save and restore on macOS 14+ work (existing snapshot code path).
- vsock guest agent + control socket work end-to-end (memory_minimize_sudo
  feedback memory, recent commits).
- vzscript engine (rsc.io/script + txtar + guest-exec + OCR) ships and runs
  recipes today.
- 60-second cold-boot to logged-in desktop is the documented expectation in
  CLAUDE.md, has been observed in Setup Assistant automation runs.

**Hypothesis — must be validated in Q2 2026 to keep the eval-runner positioning:**
- *"Sub-3s `cove fork` time-to-vsock-agent-reachable on M2/M3, 4 GB-RAM,
  30 GB-disk macOS 15 image."* This is the plausible-but-unproven F1 ship-gate.
  The previous "<2s" number was aspirational; sub-3s is the honest revised
  target. Real number replaces the placeholder once benchmarked.
- *"Per-eval user-account isolation correctness across N=500 lifecycles."*
  Specifically: TCC residue across user delete/recreate (test: User A grants
  Screen Recording, gets deleted; User B inherits without prompt = fail);
  System Keychain residue (User A installs root CA, gets deleted; User B
  inherits trust = fail); AppleID rate limiting (50 successive AppleID logins
  trigger HTTP 429 or "max free accounts" lock = documented limit);
  GlobalPreferences leakage (User A modifies system audio volume, User B
  inherits = fail); FileVault SecureToken propagation across N user
  lifecycles (sysadminctl LaunchDaemon eventually fails to propagate = hard
  limit at N); orphaned-daemon residue (User A drops a `/Library/LaunchDaemons`
  plist, gets deleted; daemon survives = hard limit). Each is pass / fail /
  documented limit. Per-eval user-account isolation is reframed accordingly
  after Q2.
- *"Thousands of macOS eval iterations per hour"* — replaced in v1 with
  "high-throughput agent and software evals." Specific throughput numbers
  appear only after Q2 benchmarks ship. The doc does not promise a number
  it has not measured.
- *"Day-1 paying targets for cohorts."* The named targets (cua-bench, Buildkite
  ecosystem shops, frontier-lab eval team via warm intro, Raycast/Linear
  release-engineering, regulated-AI contractor) are **categories with one
  candidate name each**, not interview-validated buyers. v1 treats them as
  outreach hypotheses; v2 of this doc (post-Q2) replaces them with named
  customers who have run cove on a real workload.

**Hypothesis — pricing / revenue assumptions:**
- *"$8/seat or $25/team for the hosted control plane"* — anchored to OrbStack
  in the v0 draft. Removed in v1: OrbStack's per-developer-laptop unit
  economics don't match cove's per-fleet hosted-control-plane economics.
  Pricing is TBD after first 5 customer conversations.
- *"$500/mo Q3 / $2k/mo Q1 2027 GitHub Sponsors floor"* — aspirational, not
  underwritten by current sponsorship trajectory. v1 keeps the targets but
  labels them as goals, not ship-gates. The Q1 2027 sustainability ship-gate
  is "at least one of (3 paying control-plane customers; $1.5k/mo Sponsors;
  one consulting contract)" — meaningfully more conservative than v0.

**Hypothesis — competitive timing:**
- *"OpenAI ships first-party macOS sandbox in 3-6 months"* (T0 likelihood
  50–70%). This is a forecast, not a fact. cove's response plan is robust to
  it being wrong in either direction (faster: privacy-sensitive cohort
  positioning is already day-1 in v1; slower: F3 adapter-shipgate captures
  the wedge while it lasts).

**Verification gap from the reconstructed research file:**
The notebook source `/tmp/cove-research-update-2026-04-25.md` was reconstructed
after the original was lost; its honesty notes flag specific dates as
"approximate, verify before publish." v1 cross-verifies the load-bearing dates
where source-of-truth is locally readable: the OpenAI announcement banner is
verified against `cirruslabs/tart` commit `abfbb10` dated 2026-04-07. Other
dates (Cirrus CI shutdown 2026-06-01, Daytona Series A timing, OpenAI Agents
SDK launch 2026-04-15, Anthropic Computer Use launch 2026-03-23) remain
notebook-cited and have not been re-verified against primary sources within
this revision. A pre-publish honesty pass should re-fetch each via WebFetch
and downgrade any that don't verify cleanly.

## Open questions for the maintainer

1. **Q2 priority trade.** Q2 must ship 0.2 *and* the F1 benchmark *and* the
   soft-reset isolation test suite *and* the F3 v1 adapter *and* the
   Tart-import best-effort path. That is more than fits in a calendar quarter.
   What gets sacrificed if (when) Q2 slips? My read: keep F1 benchmark and
   soft-reset validation as non-negotiable; push the Tart-import to Q3 best-
   effort if needed; never sacrifice the validation milestones because they
   are the eval-runner positioning's correctness check.
2. **Does the `coveconnect.dev` hosted control plane align with how you want
   to spend time?** It's the only credible non-consulting recurring revenue
   path I see in 12 months, *and* Orchard already exists as the prior art it
   competes with. If you don't want to build/operate a SaaS, Track B drops
   out and the funding plan leans on sponsors + consulting only — viable but
   thinner.
3. **Discord vs forum vs neither.** I recommend a small Discord opened
   2026-Q2. Lower-friction than the alternatives, but it's *yours* to moderate.
   If that's not appealing, GitHub Discussions + Lobsters threads are
   acceptable — slower-burn community.
4. **Trademark / branding (now Q2 mandatory hygiene, not a 1.0 footnote).**
   cove (the name) collides with several other dev-tool projects. USPTO search
   must clear *before* signed images go to a public registry under that name,
   else rebrand-on-cease compounds the migration cost. Memory note
   `project_rename_tarn.md` mentions a prior naming exercise — surface that
   now, not at 1.0.
5. **The "agentkit" registry costs money to host.** GHCR is free for public
   images (recommended start). A self-hosted registry (`registry.cove.run`)
   adds bandwidth costs once images are big and downloads are common. My
   recommendation: GHCR through 1.0; revisit only if GHCR free-tier limits
   bite.
6. **Deeper integrated partner ask.** Q3 calls for landing cove in *one*
   well-known external project. Do you have an existing relationship with
   anyone in: AI agent eval labs, Computer-Use benchmark teams, AI labs
   building macOS-aware coding agents? A warm intro saves 6 months.
7. **License of the adapters (cove-sandbox PyPI, etc.).** MIT to match cove,
   or Apache-2.0 to match the OpenAI Agents SDK ecosystem? Apache is friendlier
   for partner inclusion. My recommendation: Apache-2.0 for adapters only.
8. **The "thousands per hour" claim.** The v1 doc replaces it with
   "high-throughput agent and software evals" pending Q2 benchmark. If the Q2
   number comes back at, say, 200/hour rather than 2000/hour, does the
   eval-runner positioning still survive? My read: yes if soft-reset works
   (200/hour with hard-reset only, but soft-reset opens 2000+/hour), no if
   soft-reset hits hard limits. This is the single biggest pivot trigger in
   the 12-month plan.
9. **Privacy-sensitive cohort messaging is now day-1.** Cohort #1 in v1 is
   legal-tech / healthcare AI / fed-gov AI contractors. That is the cohort
   most resistant to OpenAI's hosted alternative. Their messaging differs
   from "AI agent sandbox" — closer to "compliance-grade local macOS
   sandbox." Consider a separate landing page in Q3.
10. **Counter-positioning.** If Q2 validation reveals the soft-reset
    primitive doesn't work at scale (multiple TCC/Keychain/SecureToken
    failures stop being one-off), the eval-runner thesis softens. The
    fallback is "macOS dev workstation as code" + "macOS CI fleet runner OSS"
    — same engineering features, different headline. v1 does not commit to
    this counter-positioning; Pass 4 (honesty audit) will note it as a
    hypothesis-pivot trigger.

## Honesty notes

This section exists because v1 of this doc was repaired after a Pass-2 critique
revealed several load-bearing claims that the v0 draft made with the confidence
of measured results when they were aspirations. The maintainer should know
exactly which claims still need validation before the strategy is committed
beyond Q2 2026.

**Hypotheses the maintainer should validate before further commitment:**

1. **Per-eval user-account isolation works at scale.** Status: unproven. v0
   treated this as a primitive cove already had; v1 treats it as a Q2 test
   suite that produces pass/fail results across TCC, System Keychain, AppleID
   throttling, GlobalPreferences leakage, FileVault SecureToken propagation,
   and orphaned-daemon residue. **If multiple of these hit hard limits, the
   eval-runner positioning's "soft-reset in milliseconds" headline does not
   survive — and neither does the throughput claim.**
2. **Sub-3s `cove fork` to vsock-agent-reachable on M2/M3.** Status: plausible
   but unbenchmarked. v1 commits to publishing the actual number from a Q2
   benchmark on a defined workload. The honest answer might be 2s, 4s, or
   8s — each of which has different implications for whether "high-throughput
   evals" is true marketing or rhetoric.
3. **OpenAI ships a first-party macOS sandbox in 3-6 months.** Status: forecast,
   not fact. v1's response plan is robust to it being wrong in either
   direction. But if OpenAI ships *and* the user-account isolation primitive
   *also* hits hard limits, two of the three legs of the eval-runner
   positioning are gone, and the doc must counter-position to "macOS dev
   workstation as code" / "macOS CI fleet runner OSS."
4. **Hosted control plane converts paying customers.** Status: unproven. Track
   B has been deferred from Q4 to Q1 2027 to give the maintainer breathing
   room. The first 5 customer conversations in Q1 2027 are the experiment.
   Pricing TBD until then.
5. **Solo-maintainer scope.** Status: ambitious. The 12-month plan still
   contains 5 marquee features, 4 release cuts, a hosted control plane MVP,
   GTM activity per quarter, Discord moderation, and partner outreach. v1
   scaled back from v0 (F4 deferred to 1.0+, hosted control plane deferred
   to Q1 2027), but the surface area is still large. If two consecutive
   releases slip, the right move is descope further, not push harder.

**Date claims that need re-verification before publish:**
- Cirrus CI shutdown 2026-06-01 — re-fetch from Cirrus blog/announcement.
- Daytona Series A timing (notebook says February 2026) — verify against
  Crunchbase or Daytona blog.
- OpenAI Agents SDK launch 2026-04-15 — verify against OpenAI blog.
- Anthropic Computer Use launch 2026-03-23 — verify against Anthropic blog.
- The OpenAI ↔ Cirrus announcement date 2026-04-07 *is* verified against
  `cirruslabs/tart` commit `abfbb10` and stands.

**Claims removed from v0 that v1 declines to make:**
- "Thousands of macOS eval iterations per hour" (replaced with "high-throughput
  agent and software evals" until benchmarked).
- "Sub-2s `cove fork`" (revised to sub-3s as the honest target).
- "Tart has no maintainers, zombie codebase" (false — Tart 2.32.1 cut
  2026-04-11; license is still Fair Source 0.9 unchanged from 2023).
- "APFS clonefile is a cove-exclusive primitive" (false — Tart's `clone` uses
  it; cove's exclusive thing is the named multi-snapshot model and atomic
  fork API).
- "OrbStack-style $8/seat hosted-tier pricing" (replaced with "TBD after
  first 5 customers" because the unit-economics analogy doesn't hold).
- "We are the only player without a commercial-license trigger" (sharpened
  with the actual license-arithmetic table — Tart Fair Source 100-CPU,
  Orchard Fair Source 4-device, tart-guest-agent FSL-1.1; cove MIT is the
  only OSS-by-OSI option).
- "First 100 customers" cohort framing replaced with five cohorts plus
  named outreach candidates clearly labeled as outreach hypotheses.

**The single biggest open question.** If forced to pick one validation step
to do first, the maintainer should run the **soft-reset isolation test
suite** in Q2 2026. If that suite reveals the user-account primitive works
across at least 4 of the 6 concerns (TCC, Keychain, AppleID, GlobalPrefs,
FileVault, residue), the eval-runner positioning is sound and the rest of the
plan is engineering. If 3 or more hit hard limits, the positioning needs
revision *before* committing further engineering quarters to it.

## A note on the methodology

This roadmap was drafted by an external product-thinker session (v0) and revised
by a follow-on session (v1) after a five-pass review (comprehension → adversarial
critique → repair → honesty audit → hand-off). Both sessions used a seeded
NotebookLM notebook (`79a32e96-8e1c-4e89-9385-20193e3a8209`) as a sparring
partner. v1 added a direct source review of `cirruslabs/tart`,
`cirruslabs/orchard`, and `cirruslabs/tart-guest-agent`
(`/tmp/cirruslabs-review.md`) which corrected several v0 claims about Tart's
license, maintenance status, and snapshot model. Where v1's reasoning diverged
from v0's or from the notebook's, the divergence is called out explicitly in
the Honesty notes section.

External web research (April 2026) underpinned the OpenAI ↔ Cirrus Labs deal,
Apple `container` Linux-only stance, Daytona Series A, OpenAI Agents SDK partner
list, Anthropic Computer Use Agent launch, and OrbStack pricing. The
research-update notebook source was reconstructed mid-flight after the original
was lost; specific date claims are flagged for re-verification before publish
(see Honesty notes).

## Success test

This roadmap is working if, by 2027-Q1, both of the following are true:

> When an AI agent harness team or privacy-sensitive macOS-eval operator needs to
> run, snapshot, fork, and reset a real macOS environment on Apple Silicon
> *locally*, *cove* is the answer they reach for, the answer their docs link to,
> and the only MIT-licensed Apple-Silicon-native option without a commercial-license
> trigger.

> The Q2 2026 soft-reset isolation test suite either passed (per-eval user-account
> isolation works at scale) or led to a documented limit and a positioning revision
> that the maintainer published openly rather than buried.

If by Q1 2027 cove is still positioned as "another macOS VM CLI" or "a Lume
alternative for purists," the strategy has drifted. If the throughput claim
shipped without a published benchmark, the doc has lied. If the Tart row in the
matrix was published with the v0 "zombie" framing, the doc has been sloppy. The
1.0 cut is the moment to decide whether to course-correct or accept that.
