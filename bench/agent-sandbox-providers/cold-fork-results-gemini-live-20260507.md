# Cold fork to first observable action

- Date: 2026-05-07T05:27:22Z
- Cove: cove 262f05ab095e (commit 262f05ab095e, built 2026-05-07T03:45:31Z)
- Host: Darwin m4x-129.local 25.4.0 Darwin Kernel Version 25.4.0: Thu Mar 19 19:33:25 PDT 2026; root:xnu-12377.101.15~1/RELEASE_ARM64_T6041 arm64
- Image: `agentkit/macos-base:latest`
- First action proxy: first screenshot/control event in replay bundle
- Live API run: 1
- Run artifacts: `/tmp/cove-agent-sandbox-gemini-cold-live-20260506222722-artifacts`

| Provider | Runs | Median fork-to-first-action s | Error rate | Notes |
| --- | ---: | ---: | ---: | --- |
| gemini | 1 | n/a | 1.00 | instant provisioning metric; artifacts: /tmp/cove-agent-sandbox-gemini-cold-live-20260506222722-artifacts/gemini |

## Verification Notes

This run used a live Gemini API key supplied in the process environment only.
The key was not written to the result file or repository. The cold-fork run
failed before first provider action with HTTP 429 Too Many Requests, so it is
rate-limit/quota evidence rather than a successful cold metric.
