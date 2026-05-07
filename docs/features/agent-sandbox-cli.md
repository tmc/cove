---
title: Agent Sandbox CLI
---
# Agent Sandbox CLI

`cove agent-sandbox run` starts a fresh fork from a local image, runs one
computer-use provider loop, writes a replay bundle, and stops the fork.

```bash
cove agent-sandbox run \
  --provider anthropic \
  --image macos-agent:latest \
  --task "Open Safari and summarize the visible page."
```

The parent image is not used directly. Each run creates a disposable fork,
waits for the guest agent, runs the provider bridge, records artifacts, and
stops the fork on exit.

## Options

| Flag | Default | Description |
| --- | --- | --- |
| `--provider` | required | `openai`, `anthropic`, `gemini`, or `vertex`. |
| `--image` | required | Local cove image ref to fork. |
| `--task` | required | Prompt for the provider loop. |
| `--screenshot-dir` | `~/.vz/runs/<run-id>/screenshots` | Provider screenshot output directory. |
| `--max-steps` | `25` | Maximum provider tool-call rounds. |
| `--vm` | generated | Ephemeral fork name. |

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
  screenshots/step-NNN.png
  ocr-text.txt
  control-events.jsonl
  final-answer.md
  metrics.jsonl -> ../metrics.jsonl
```

Provider screenshots are written to disk before the provider receives them.
The replay directory then gets a numbered copy plus OCR text extracted from
each screenshot. `control-events.jsonl` records the cove control-socket actions
performed by the provider bridge.

Use a local or approved artifact store for replay bundles. Screenshots and OCR
text may contain secrets visible in the guest.
