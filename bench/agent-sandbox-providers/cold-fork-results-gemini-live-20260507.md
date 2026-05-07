# Cold fork to first observable action

- Date: 2026-05-07T05:30:07Z
- Cove: cove a2a159fcc922 (commit a2a159fcc922, built 2026-05-07T05:28:44Z)
- Host: Darwin m4x-129.local 25.4.0 Darwin Kernel Version 25.4.0: Thu Mar 19 19:33:25 PDT 2026; root:xnu-12377.101.15~1/RELEASE_ARM64_T6041 arm64
- Image: `agentkit/macos-base:latest`
- First action proxy: first screenshot/control event in replay bundle
- Live API run: 1
- Run artifacts: `/tmp/cove-agent-sandbox-gemini-cold-retry-live-20260506223007-artifacts`

| Provider | Runs | Median fork-to-first-action s | Error rate | Notes |
| --- | ---: | ---: | ---: | --- |
| gemini | 1 | 43.734 | 0.00 | instant provisioning metric; artifacts: /tmp/cove-agent-sandbox-gemini-cold-retry-live-20260506223007-artifacts/gemini |

## Verification Notes

This run used a live Gemini API key supplied in the process environment only.
The key was not written to the result file or repository. The bridge was run
with `COVE_GOOGLE_BRIDGE_HTTP_RETRIES=4`; the run reached the forked macOS VM,
recorded a replay event, and produced a cold-fork-to-first-action metric with
error rate 0.00.
