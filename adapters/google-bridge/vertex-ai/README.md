# google-bridge / vertex-ai

Vertex AI variant of the Gemini computer-use bridge -- same wire shape, GCP
service-account / ADC auth instead of an API key.

This is a substrate example, not a full SDK. It mirrors the sibling
[../computer_use.py](../computer_use.py) (direct Gemini API) and the
[anthropic-bridge](../../anthropic-bridge/). Use this variant when your team
needs to bill against a Google Cloud project rather than ship a
`GEMINI_API_KEY` to dev machines.

## Requirements

- Python 3.11+
- `httpx` and `google-auth` (`pip install httpx google-auth`)
- A Google Cloud project with the Vertex AI API enabled
- Application Default Credentials configured:
  - `gcloud auth application-default login`, **or**
  - `GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json`
- A running cove macOS VM in GUI mode with the control socket exposed
  (`cove run -gui`)

If `google-auth` is unavailable, the helper falls back to
`gcloud auth print-access-token` via subprocess.

## Quickstart

```bash
gcloud auth application-default login
python3 adapters/google-bridge/vertex-ai/computer_use.py \
  --vm macos-eval \
  --project my-gcp-project \
  --region us-central1 \
  --task "Open Safari and read the visible page title."
```

## Limitations

- Substrate-only; no Agents SDK abstraction.
- One-shot loop, capped at `--max-iterations` (default 5).
- Coordinate translation is naive (linear normalize 0-999 to pixels).
- **Billed per request.** Vertex AI computer-use is in preview and has no
  free tier; eval loops can rack up real cost. See
  https://cloud.google.com/vertex-ai/generative-ai/pricing.
- `ENVIRONMENT_BROWSER` is currently the only Gemini computer-use environment;
  results on a macOS desktop guest are best-effort.
- Some actions (`hover_at`, `drag_and_drop`, `navigate`) have no direct
  equivalent on cove's control socket and are returned as no-ops with a
  refreshed screenshot.

See [docs/examples/vertex-ai-computer-use.md](../../../docs/examples/vertex-ai-computer-use.md)
for an end-to-end walkthrough, and the sibling
[../README.md](../README.md) for the direct Gemini API variant.
