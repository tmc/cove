# google-bridge

Thin Python helper that drives a running cove macOS VM through the existing
control-socket primitives (screenshot/mouse/text/key) as a Google Gemini
computer-use target.

This is a substrate example, not a full SDK. It mirrors the sibling
[anthropic-bridge](../anthropic-bridge/) and the more complete
[openai-agents-python](../openai-agents-python/) adapter.

## Requirements

- Python 3.11+
- `httpx` (`pip install httpx`)
- `GEMINI_API_KEY` env var (https://aistudio.google.com/ -> Get API key)
- A running cove macOS VM in GUI mode with the control socket exposed
  (`cove run -gui`)

## Quickstart

```bash
export GEMINI_API_KEY=...
python3 adapters/google-bridge/computer_use.py \
  --vm macos-eval \
  --task "Open Safari and read the visible page title."
```

## Limitations

- Substrate-only; no Agents SDK abstraction.
- One-shot loop, capped at `--max-iterations` (default 5).
- Coordinate translation is naive (linear normalize 0-999 to pixels).
- `ENVIRONMENT_BROWSER` is currently the only Gemini computer-use environment;
  results on a macOS desktop guest are best-effort.
- Vertex AI computer-use is **deferred** to a follow-on slice; this helper
  uses the simpler `generativelanguage.googleapis.com` endpoint with a single
  API key.
- Some Gemini actions (`hover_at`, `drag_and_drop`, `navigate`) have no direct
  equivalent on cove's control socket and are returned as no-ops with a
  refreshed screenshot.

See [docs/examples/gemini-computer-use.md](../../docs/examples/gemini-computer-use.md)
for an end-to-end walkthrough.
