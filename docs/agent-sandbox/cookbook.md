# Agent Sandbox Cookbook

These examples assume a local image named `agentkit/macos-base:latest`. It is a
local cove image ref, not a public registry reference.

## Run Claude Against a Forked macOS VM

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cove agent-sandbox run \
  --provider anthropic \
  --image agentkit/macos-base:latest \
  --task "Open Safari, visit example.com, and summarize the page." \
  --max-steps 12
```

Expected output:

```text
agent-sandbox replay: /Users/me/.vz/runs/<run-id>/replay
```

The replay bundle contains screenshots, OCR text, control events, and the
final answer.

## OpenAI With Network Access Only to api.openai.com

Create or select a base image configured for the strict network policy:

```bash
cove run -fork-from agentkit/macos-base:latest \
  -fork-name openai-net-setup \
  -network strict:api.openai.com \
  -ephemeral
```

Run the agent loop:

```bash
export OPENAI_API_KEY=sk-...
cove agent-sandbox run \
  --provider openai \
  --image agentkit/macos-base:latest \
  --task "Take one screenshot and report the visible app."
```

Expected output:

```text
agent-sandbox replay: /Users/me/.vz/runs/<run-id>/replay
```

Install the local OpenAI adapter first:

```bash
python -m pip install -e adapters/openai-agents-python[agents]
```

## Four Parallel Forks, Four Providers

```bash
export ANTHROPIC_API_KEY=sk-ant-...
export GEMINI_API_KEY=...
export GOOGLE_CLOUD_PROJECT=my-project
export OPENAI_API_KEY=sk-...

for provider in openai anthropic gemini vertex; do
  cove agent-sandbox run \
    --provider "$provider" \
    --image agentkit/macos-base:latest \
    --task "Take a screenshot and describe the desktop." \
    --vm "agent-$provider-$(date +%s)" &
done
wait
```

Expected output:

```text
agent-sandbox replay: /Users/me/.vz/runs/<run-id>/replay
```

All providers require valid credentials and a local `agentkit/macos-base:latest`
image.

## Snapshot a Long-Running Agent and Resume Tomorrow

Start a named fork and keep it:

```bash
cove run -fork-from agentkit/macos-base:latest \
  -fork-name research-agent-001 \
  -keep \
  -gui
cove ctl -vm research-agent-001 -wait 120s agent-ping
```

Run a provider against the kept VM through the provider bridge or control
socket, then stop and snapshot the VM state:

```bash
cove ctl -vm research-agent-001 stop
cove image build -from research-agent-001 -tag research-agent:day1
```

Resume tomorrow:

```bash
cove run -fork-from research-agent:day1 \
  -fork-name research-agent-day2 \
  -keep \
  -gui
cove ctl -vm research-agent-day2 -wait 120s agent-ping
```

Expected output:

```text
agent pong
```
