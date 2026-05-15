---
title: Vertex AI Computer Use
---
# Vertex AI Computer Use

Drive a local cove macOS VM from Google
[Vertex AI's computer-use preview](https://cloud.google.com/vertex-ai/generative-ai/docs/computer-use)
for one-shot agent runs, billing against a Google Cloud project. The bridge
is a small Python helper that translates Vertex AI `function_call` parts into
cove control-socket commands and sends the next screenshot back as a
`function_response`. It is the GCP-native sibling to the
[direct Gemini API helper](gemini-computer-use.md) -- same wire shape, same
function-call dispatch, just a different endpoint and Application Default
Credentials instead of an API key.

This variant is for teams that can't ship a `GEMINI_API_KEY` env var to dev
machines (security or compliance) and need usage billed against a GCP
project.

## Prerequisites

- A cove binary built and codesigned with the virtualization entitlement
  (`go build && codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove`).
- A long-lived macOS VM with the control socket exposed in GUI mode
  (`cove -vm vertex-eval run -gui`).
- A Google Cloud project with the Vertex AI API enabled.
- Application Default Credentials configured:
  - `gcloud auth application-default login`, **or**
  - `export GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json`.
- Python 3.11+ and `httpx` plus `google-auth`
  (`pip install httpx google-auth`). If `google-auth` is missing the helper
  falls back to `gcloud auth print-access-token` via subprocess.

## Run it

Start the VM and wait for the guest agent:

```bash
cove -vm vertex-eval run -gui
cove ctl -vm vertex-eval agent-ping -wait 120s
```

In another shell, run the bridge:

```bash
gcloud auth application-default login
python3 adapters/google-bridge/vertex-ai/computer_use.py \
  --vm vertex-eval \
  --project my-gcp-project \
  --region us-central1 \
  --task "Open Safari, search for 'cove vm', and read the first result."
```

The script auto-resolves the VM's local control socket and per-VM auth token.
Use `--token "$COVE_CONTROL_TOKEN"` to override from a secret manager or
redacted environment variable, and `--credentials /path/to/sa.json` to point at
a specific service-account file.

## How it works

Vertex AI's computer-use preview returns the same shape as the direct Gemini
API: `candidates[0].content.parts` containing zero or more `function_call`
parts (`click_at`, `type_text_at`, `key_combination`, `scroll_at`, etc.).
The helper dispatches each call to the matching cove primitive over the local
control socket, then takes a fresh screenshot and sends it back as a
`function_response` with an `inline_data` PNG. The loop runs until the model
emits no further `function_call` parts or `--max-iterations` (default 5) is
hit, then prints the model's final text.

Authentication uses Google's standard Application Default Credentials
resolution. The helper tries `google.auth.default()` first; if `google-auth`
isn't installed (or fails for any reason), it falls back to a
`gcloud auth print-access-token` subprocess. Tokens are short-lived OAuth2
bearer tokens; one is acquired at startup and reused for the whole run.

## Limits

- **Billed per request.** Vertex AI computer-use is in preview and has no
  free tier; eval loops can rack up real cost. See
  https://cloud.google.com/vertex-ai/generative-ai/pricing for pricing.
- Substrate only. There is no Agents SDK abstraction; this is a single
  request/response loop.
- Coordinate translation is naive: Vertex emits `(x, y)` in normalized
  0..999 space, the helper linearly maps to the screenshot's pixel size.
- `ENVIRONMENT_BROWSER` is currently the only defined Gemini computer-use
  environment, which is sub-optimal for a macOS desktop guest. Results
  are best-effort. We will switch when a desktop-shaped environment ships.
- `hover_at`, `drag_and_drop`, and browser actions like `navigate` have no
  direct equivalent on cove's control socket and are returned as no-ops
  with a refreshed screenshot.

## See also

- [Gemini Computer Use](gemini-computer-use.md) -- sibling bridge for the
  direct `generativelanguage.googleapis.com` endpoint with an API key.
- [Anthropic Computer Use](anthropic-computer-use.md) -- sibling bridge for
  the Anthropic Messages API computer-use beta.
- [OpenAI Agents SDK](openai-agents.md) -- full Agents SDK adapter with
  `ComputerTool` and `SandboxRunConfig` integration.
