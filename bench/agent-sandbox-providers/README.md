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

Checked-in evidence:

- `results-20260505.md`: full-matrix protocol capture without live provider
  credentials.
- `cold-fork-results-20260505.md`: cold-fork protocol capture without live
  provider credentials.
- `results-openai-live-20260507.md`: one OpenAI live run on `m4x-129`; not
  full-matrix evidence.
