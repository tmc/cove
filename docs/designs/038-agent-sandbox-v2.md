# Design 034: Agent Sandbox v2

**Status:** shipped as provider abstraction, docs, doctor, examples, and benchmark harness.
Full live provider-matrix evidence still depends on local provider credentials.

## Provider Abstraction

The internal provider registry normalizes OpenAI, Anthropic, Gemini, and Vertex
behind provider metadata and a common `Run` shape. OpenAI uses the local Python
Agents SDK adapter; Anthropic is implemented by the Go runtime path; Gemini and
Vertex use Python bridge scripts.

Provider switching is a one-flag change:

```bash
cove agent-sandbox run --provider anthropic --image agentkit/macos-base:latest --task "..."
cove agent-sandbox run --provider gemini --image agentkit/macos-base:latest --task "..."
```

## Fork-Isolation Invariants

An agent-sandbox run never mutates the parent image directly. The CLI starts a
fresh fork from a local image, waits for the guest agent, runs the provider
loop, writes replay artifacts, and stops the child. This keeps provider choice
separate from VM isolation.

## Env-Var Contract

| Provider | Required env |
| --- | --- |
| OpenAI | `OPENAI_API_KEY` |
| Anthropic | `ANTHROPIC_API_KEY` |
| Gemini | `GEMINI_API_KEY` |
| Vertex | `GOOGLE_CLOUD_PROJECT` or `COVE_VERTEX_PROJECT`; optional `COVE_VERTEX_REGION` |

`cove agent-sandbox doctor --provider all` checks those variables, provider API
TCP reachability, and the selected model id across the full matrix without
making a provider API call in tests.

## Benchmark Methodology

Two scripts live under `bench/agent-sandbox-providers/`:

- `run.sh`: runs the same mechanical task ten times per provider and records
  median latency plus error rate.
- `cold-fork-first-action.sh`: measures from fork start to the first replayed
  control event.

The scripts capture cove version, host info, image ref, provider, model id,
run count, median latency, and error rate. Live provider calls are gated by
`RUN_LIVE=1` and local credentials.

Checked-in evidence distinguishes protocol dry-runs from live provider runs:

- `results-20260505.md`: full-matrix protocol capture; no provider credentials
  were available for live calls in that commit.
- `cold-fork-results-20260505.md`: cold-fork protocol capture; no live calls.
- `results-openai-live-20260507.md`: one successful OpenAI live latency
  run on `m4x-129`. It is not full-matrix evidence.
- `cold-fork-results-openai-live-20260507.md`: one successful OpenAI live
  cold-fork-to-first-action run on `m4x-129`. It is not full-matrix evidence.
- `results-gemini-live-20260507.md`: one successful Gemini live latency run
  on `m4x-129`. It is not full-matrix evidence.
- `cold-fork-results-gemini-live-20260507.md`: one successful Gemini live
  cold-fork-to-first-action run.
- `results-vertex-live-20260507.md`: one Vertex live latency attempt with
  passing auth/ADC that failed with provider HTTP 404 before first action.

## Ship Artifacts

- Provider matrix: `docs/agent-sandbox/provider-matrix.md`
- Five-line example: `examples/agent-loop-5-lines/`
- Cookbook: `docs/agent-sandbox/cookbook.md`
- Quickstart: `docs/agent-sandbox/quickstart.md`
- Benchmarks: `bench/agent-sandbox-providers/`

## Verified 2026-05-10

- `cove agent-sandbox` subcommand registered in `main.go:682`; `doctor`
  subcommand at `agent_sandbox.go:50` accepts `--provider all|openai|anthropic|gemini|vertex` (`agent_sandbox.go:70`).
- Provider abstraction lives in `internal/agentsandbox/provider.go`.
- Env-var contract enforced: `GEMINI_API_KEY`, `COVE_VERTEX_PROJECT`
  exercised in `agent_sandbox_test.go:79-103`. `OPENAI_API_KEY` and
  `ANTHROPIC_API_KEY` referenced in doctor reachability host map
  (`agent_sandbox.go:165-167`).
- Fork-isolation invariant intact: `agent_sandbox.go` composes on top of
  `cove run -fork-from <ref>` (design 013 Phase 3 + design 024 Slice 1).
