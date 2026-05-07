# Cold fork to first observable action

- Date: 2026-05-07T03:42:10Z
- Cove: cove 54b8c1772dc5 (commit 54b8c1772dc5, built 2026-05-07T03:36:30Z)
- Host: Darwin m4x-129.local 25.4.0 Darwin Kernel Version 25.4.0: Thu Mar 19 19:33:25 PDT 2026; root:xnu-12377.101.15~1/RELEASE_ARM64_T6041 arm64
- Image: `agentkit/macos-base:latest`
- First action proxy: first screenshot/control event in replay bundle
- Live API run: 1
- Run artifacts: `/tmp/cove-agent-sandbox-openai-cold-recorded-live-20260506204209-artifacts`

| Provider | Runs | Median fork-to-first-action s | Error rate | Notes |
| --- | ---: | ---: | ---: | --- |
| openai | 1 | 54.785 | 0.00 | instant provisioning metric; artifacts: /tmp/cove-agent-sandbox-openai-cold-recorded-live-20260506204209-artifacts/openai |

## Verification Notes

This run used the same temporary Python 3.12 virtual environment as the OpenAI
latency run. The OpenAI adapter recorded `control-events.jsonl`, and the cold
metric is measured from benchmark process start to the first replayed provider
action timestamp.
