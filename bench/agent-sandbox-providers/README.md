# Agent Sandbox Provider Benchmarks

This directory contains reproducible protocols for measuring provider-loop
latency and cold-fork-to-first-action time.

Run a dry protocol capture without provider API calls:

```bash
cove agent-sandbox bench --provider all
cove agent-sandbox bench --provider all --cold
```

Run live measurements only after the provider matrix doctor passes:

```bash
cove agent-sandbox doctor --provider all
cove agent-sandbox bench --provider all --live
cove agent-sandbox bench --provider all --live --cold
```

The benchmark scripts capture cove version, host info, image ref, provider,
model id, run count, median latency, and error rate. They measure mechanical
latency and reliability only, not task quality.

Live runs preserve per-provider stdout/stderr under
`<result-name>-artifacts/<provider>/`. Set `ARTIFACTS_DIR=/tmp/...` to keep
large or exploratory runs outside the repository.

Checked-in evidence:

- `results-20260505.md`: full-matrix protocol capture without live provider
  credentials.
- `cold-fork-results-20260505.md`: cold-fork protocol capture without live
  provider credentials.
- `results-openai-live-20260507.md`: one successful OpenAI live latency run
  on `m4x-129`; not full-matrix evidence.
- `cold-fork-results-openai-live-20260507.md`: one successful OpenAI live
  cold-fork-to-first-action run on `m4x-129`; not full-matrix evidence.
- `results-gemini-live-20260507.md`: one successful Gemini live latency run on `m4x-129`; not full-matrix evidence.
- `cold-fork-results-gemini-live-20260507.md`: one successful Gemini live cold-fork-to-first-action run.

- `results-vertex-live-20260507.md`: one Vertex live latency attempt with passing auth/ADC that failed with provider HTTP 404 before first action.

- `results-vertex-model-probe-20260511.md`: direct Vertex API probe showing `tmcdev/us-central1` can access `gemini-2.5-flash`, but that model rejects `computer_use`; the preview computer-use model is not accessible there.
