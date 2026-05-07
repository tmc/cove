# Agent sandbox provider latency

- Date: 2026-05-07T05:26:34Z
- Cove: cove 262f05ab095e (commit 262f05ab095e, built 2026-05-07T03:45:31Z)
- Host: Darwin m4x-129.local 25.4.0 Darwin Kernel Version 25.4.0: Thu Mar 19 19:33:25 PDT 2026; root:xnu-12377.101.15~1/RELEASE_ARM64_T6041 arm64
- Image: `agentkit/macos-base:latest`
- Task: `take a screenshot, click button at coords, type hello, take another screenshot`
- Runs per provider: 1
- Live API run: 1
- Run artifacts: `/tmp/cove-agent-sandbox-gemini-live-20260506222634-artifacts`

| Provider | Model | Runs | Median latency s | Error rate | Notes |
| --- | --- | ---: | ---: | ---: | --- |
| gemini | default | 1 | 37.801 | 0.00 | mechanical latency only; artifacts: /tmp/cove-agent-sandbox-gemini-live-20260506222634-artifacts/gemini |

## Verification Notes

This run used a live Gemini API key supplied in the process environment only.
The key was not written to the result file or repository. The one-run provider
latency benchmark reached the forked macOS VM, ran the Gemini bridge, and exited
with error rate 0.00.
