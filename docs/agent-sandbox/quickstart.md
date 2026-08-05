---
title: Agent Sandbox Quickstart
description: Go from zero to a fork-isolated agent sandbox running a provider loop.
icon: robot
---
# Agent Sandbox Quickstart

From zero to a fork-isolated provider loop:

```bash
git clone git@github.com:tmc/cove.git
cd cove
go build -o cove ./cmd/cove
codesign -s - -f --entitlements cmd/cove/vz.entitlements ./cove
export PATH="$PWD:$PATH"

cove run -fork-from agentkit/macos-base:latest -fork-name agent-smoke -ephemeral -gui
cove agent-sandbox run --provider anthropic --image agentkit/macos-base:latest --task "Take a screenshot and describe the desktop."
```

## If you do not have a base image yet

The commands above fork from `agentkit/macos-base:latest`. To build your own
base instead, install a VM, verify the control plane, then stop it so it can
serve as a fork source:

```bash
cove up -vm macos-base -user agent
cove ctl -vm macos-base -wait 120s agent-ping
cove ctl -vm macos-base screenshot -o /tmp/macos-base.png
cove -vm macos-base ctl stop
```

Fork it per task, or build an image once and run disposable children:

```bash
cove fork macos-base task-001
cove image build -from macos-base -tag macos-agent:latest
cove run -fork-from macos-agent:latest -ephemeral -gui
```

A parent must be stopped before `cove fork`, `cove clone --linked`, or
`cove run -fork-from <vm>`. Keep secrets and untrusted state in the child and
discard it after each task. The individual control calls these build on are in
the [Control socket API](../reference/control-api.md).

Provider credentials:

```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=sk-ant-...
export GEMINI_API_KEY=...
export GOOGLE_CLOUD_PROJECT=my-project
```

Check auth and network reachability:

```bash
cove agent-sandbox doctor --provider all
```

`cove agent-sandbox run` also checks the selected provider credential before it
starts a VM fork. If a key or project is missing, it exits immediately with the
matching `doctor --provider ...` command.

Switch provider with one flag:

```bash
cove agent-sandbox run --provider gemini --image agentkit/macos-base:latest --task "Open Safari."
cove agent-sandbox run --provider vertex --image agentkit/macos-base:latest --task "Open Safari."
```

Every successful run prints three paths:

```text
agent-sandbox run: ~/.vz/runs/<run-id>
agent-sandbox replay: ~/.vz/runs/<run-id>/replay
agent-sandbox summary: ~/.vz/runs/<run-id>/replay/summary.md
```

For CI steps and SDK wrappers, add `--json`. Cove keeps child-run and provider
logs on stderr and writes the final run/replay/summary handles to stdout as one
JSON object.

`cove run -fork-from` writes a lazy bundle under `~/.vz/runs/<run-id>/`:

```text
manifest.json
events.jsonl
stdout.log
stderr.log
screenshots/
```

The event log records control-socket activity — screenshots, text, keys, mouse
events, and agent calls. Per-event field shapes are in the
[Runs schema](../observability/runs-schema.md).

The replay bundle includes `summary.md`, screenshots, OCR text, control events,
the final answer, and a metrics symlink. Use a dedicated throwaway guest session
for agent runs; cove isolates the VM fork, but the current macOS capture/control
path is not a Cua Driver-style focus-safe background automation guarantee.

Export the run bundle when you need to hand it to CI or another operator:

```bash
run_id=<run-id>
cove runs export "$run_id" --format tar > "agent-sandbox-$run_id.tar.gz"
cove runs export "$run_id" --format gha-summary >> "$GITHUB_STEP_SUMMARY"
```

Provider benchmark protocols live in `bench/agent-sandbox-providers/`. Use
`cove agent-sandbox bench --provider all` to record the protocol without API
calls, or add `--live` after `doctor --provider all` passes on the host.
