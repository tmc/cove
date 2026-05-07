# Agent sandbox provider latency

- Date: 2026-05-07T05:56:20Z
- Cove: cove 0a52f368a563 (commit 0a52f368a563, built 2026-05-07T05:56:20Z)
- Host: Darwin m4x-129.local 25.4.0 Darwin Kernel Version 25.4.0: Thu Mar 19 19:33:25 PDT 2026; root:xnu-12377.101.15~1/RELEASE_ARM64_T6041 arm64
- Image: `agentkit/macos-base:latest`
- Task: `take a screenshot, click button at coords, type hello, take another screenshot`
- Runs per provider: 1
- Live API run: 1
- Run artifacts: `/tmp/cove-agent-sandbox-vertex-live-modelwired-20260506225620-artifacts`

| Provider | Model | Runs | Median latency s | Error rate | Notes |
| --- | --- | ---: | ---: | ---: | --- |
| vertex | default | 1 | 57.312 | 1.00 | mechanical latency only; artifacts: /tmp/cove-agent-sandbox-vertex-live-modelwired-20260506225620-artifacts/vertex |

## Verification Notes

This run used `GOOGLE_CLOUD_PROJECT=tmcdev` with local Application Default
Credentials. Vertex doctor passed before the run. The provider call failed with
HTTP 404 Not Found for the configured Vertex computer-use model/region, so this
is model or regional availability evidence rather than a cove fork/runtime
failure.
