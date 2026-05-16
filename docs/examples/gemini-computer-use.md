---
title: Gemini Computer Use
---
# Gemini Computer Use

Drive a local cove macOS VM from Google Gemini's
[computer-use tool](https://ai.google.dev/gemini-api/docs/computer-use) for
one-shot agent runs. The bridge is a small Python helper that translates
Gemini `function_call` parts into cove control-socket commands, then sends
the next screenshot back as a `function_response`.

## Prerequisites

- A cove binary built and codesigned with the virtualization entitlement
  (`go build && codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove`).
- A long-lived macOS VM with the control socket exposed in GUI mode
  (`cove -vm macos-eval run -gui`).
- A Gemini API key from [Google AI Studio](https://aistudio.google.com/).
- Python 3.11+ and `httpx` (`pip install httpx`).

## Run it

Start the VM and wait for the guest agent:

```bash
cove -vm macos-eval run -gui
cove ctl -vm macos-eval agent-ping -wait 120s
```

In another shell, run the bridge:

```bash
export GEMINI_API_KEY=...
python3 adapters/google-bridge/computer_use.py \
  --vm macos-eval \
  --task "Open Safari, search for 'cove vm', and read the first result."
```

The script auto-resolves the VM's local control socket and per-VM auth token.
Use `--token "$COVE_CONTROL_TOKEN"` to override from a secret manager or
redacted environment variable.

## How it works

Gemini's computer-use API returns `candidates[0].content.parts` containing
zero or more `function_call` parts (`click_at`, `type_text_at`,
`key_combination`, `scroll_at`, etc.). The helper dispatches each call to
the matching cove primitive over the local control socket, then takes a
fresh screenshot and sends it back as a `function_response` with an
`inline_data` PNG. The loop runs until the model emits no further
`function_call` parts or `--max-iterations` (default 5) is hit, then prints
the model's final text.

## Limits

- Substrate only. There is no Agents SDK abstraction; this is a single
  request/response loop.
- Coordinate translation is naive: Gemini emits `(x, y)` in normalized
  0..999 space, the helper linearly maps to the screenshot's pixel size.
- `ENVIRONMENT_BROWSER` is currently the only defined Gemini computer-use
  environment, which is sub-optimal for a macOS desktop guest. Results
  are best-effort. We will switch when a desktop-shaped environment ships.
- `hover_at`, `drag_and_drop`, and browser actions like `navigate` have no
  direct equivalent on cove's control socket and are returned as no-ops
  with a refreshed screenshot.
- Vertex AI computer-use is deferred to a follow-on slice; this helper
  targets the simpler `generativelanguage.googleapis.com` endpoint.

## See also

- [Anthropic Computer Use](anthropic-computer-use.md) -- sibling bridge for
  the Anthropic Messages API computer-use beta.
- [OpenAI Agents SDK](openai-agents.md) -- full Agents SDK adapter with
  `ComputerTool` and `SandboxRunConfig` integration.
