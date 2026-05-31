---
title: Agent Sandbox CLI
---
# Agent Sandbox CLI

`cove agent-sandbox run` starts a fresh fork from a local image, runs one
computer-use provider loop, writes a self-describing replay bundle, and stops
the fork.

```bash
cove agent-sandbox run \
  --provider anthropic \
  --image macos-agent:latest \
  --task "Open Safari and summarize the visible page."
```

The parent image is not used directly. Each run creates a disposable fork,
waits for the guest agent, runs the provider bridge, records artifacts, and
stops the fork on exit.

Before creating the fork, `run` checks the provider credential environment and
fails with a direct `doctor` hint when a required value is missing. For example,
OpenAI requires `OPENAI_API_KEY`, Anthropic requires `ANTHROPIC_API_KEY`, Gemini
requires `GEMINI_API_KEY`, and Vertex requires either `GOOGLE_CLOUD_PROJECT` or
`COVE_VERTEX_PROJECT`.

On success the command prints the run bundle, replay bundle, and Markdown
summary paths:

```text
agent-sandbox run: /Users/me/.vz/runs/<run-id>
agent-sandbox replay: /Users/me/.vz/runs/<run-id>/replay
agent-sandbox summary: /Users/me/.vz/runs/<run-id>/replay/summary.md
```

Use `--json` when a CI step or SDK wrapper needs a stable handle instead of
human text. In JSON mode, cove keeps child-run and provider logs on stderr and
writes one result object to stdout:

```bash
cove agent-sandbox run \
  --provider openai \
  --image macos-agent:latest \
  --task "Describe the desktop." \
  --json
```

```json
{
  "ok": true,
  "status": "ok",
  "run_id": "20260531-120000-abcd1234",
  "vm_name": "agent-sandbox-20260531-120000-abcd1234",
  "provider": "openai",
  "image": "macos-agent:latest",
  "run_dir": "/Users/me/.vz/runs/20260531-120000-abcd1234",
  "replay_dir": "/Users/me/.vz/runs/20260531-120000-abcd1234/replay",
  "summary_path": "/Users/me/.vz/runs/20260531-120000-abcd1234/replay/summary.md",
  "metrics_path": "/Users/me/.vz/runs/20260531-120000-abcd1234/metrics.jsonl"
}
```

## Options

| Flag | Default | Description |
| --- | --- | --- |
| `--provider` | required | `openai`, `anthropic`, `gemini`, or `vertex`. |
| `--image` | required | Local cove image ref to fork. |
| `--task` | required | Prompt for the provider loop. |
| `--screenshot-dir` | `~/.vz/runs/<run-id>/screenshots` | Provider screenshot output directory. |
| `--max-steps` | `25` | Maximum provider tool-call rounds. |
| `--vm` | generated | Ephemeral fork name. |
| `--json` | `false` | Print the final run result as JSON on stdout. |

## Providers

Anthropic:

```bash
export ANTHROPIC_API_KEY=...
cove agent-sandbox run \
  --provider anthropic \
  --image macos-agent:latest \
  --task "Use the desktop and report what is visible."
```

Gemini:

```bash
export GEMINI_API_KEY=...
cove agent-sandbox run \
  --provider gemini \
  --image macos-agent:latest \
  --task "Open Safari and read the page title."
```

Vertex AI:

```bash
gcloud auth application-default login
export GOOGLE_CLOUD_PROJECT=my-project
cove agent-sandbox run \
  --provider vertex \
  --image macos-agent:latest \
  --task "Inspect the desktop and summarize it."
```

OpenAI:

```bash
export OPENAI_API_KEY=...
python3.12 -m venv /tmp/cove-openai-agents
/tmp/cove-openai-agents/bin/python -m pip install -e 'adapters/openai-agents-python[agents]'
COVE_AGENT_SANDBOX_PYTHON=/tmp/cove-openai-agents/bin/python \
cove agent-sandbox run \
  --provider openai \
  --image macos-agent:latest \
  --task "Describe the desktop."
```

The OpenAI provider uses the local OpenAI Agents SDK adapter under
`adapters/openai-agents-python`. Python 3.10 or newer is required.

## Doctor And Benchmarks

Check the full provider matrix before live runs:

```bash
cove agent-sandbox doctor --provider all
```

Run dry benchmark protocols without provider API calls:

```bash
cove agent-sandbox bench --provider all
cove agent-sandbox bench --provider all --cold
```

After `doctor --provider all` passes, add `--live` to collect provider latency
and cold-fork-to-first-action evidence.

## Replay Bundle

Each run writes:

```text
~/.vz/runs/<run-id>/replay/
  summary.md
  screenshots/step-NNN.png
  ocr-text.txt
  control-events.jsonl
  final-answer.md
  metrics.jsonl -> ../metrics.jsonl
```

Provider screenshots are written to disk before the provider receives them.
The replay directory then gets a numbered copy plus OCR text extracted from
each screenshot. `control-events.jsonl` records the cove control-socket actions
performed by the provider bridge. `summary.md` records the run id, VM, provider,
image, final status, replay path, metrics path, screenshot count, control-event
count, task, artifact list, and final answer.

## Export

The printed run path includes the run id. Export the complete replay bundle and
metrics as a tarball with `cove runs export`:

```bash
run_id=<run-id>
cove runs export "$run_id" --format tar > "agent-sandbox-$run_id.tar.gz"
```

For GitHub Actions summaries, append the run summary to
`GITHUB_STEP_SUMMARY` and upload the tarball with your scheduler's artifact
step:

```bash
cove runs export "$run_id" --format gha-summary >> "$GITHUB_STEP_SUMMARY"
```

## Background Safety

`agent-sandbox run` isolates the provider loop in a fresh VM fork, not in the
host desktop. Screenshots, keyboard, text, and mouse actions are sent through
cove's guest control surface. Keep agent runs in a dedicated throwaway guest
session and do not treat the current macOS capture path as a Cua Driver-style
focus-safe background automation guarantee.

Use a local or approved artifact store for replay bundles. Screenshots and OCR
text may contain secrets visible in the guest.
