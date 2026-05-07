# Agent sandbox provider latency

- Date: 2026-05-07T03:35:44Z
- Cove: cove 81588c7a5619 (commit 81588c7a5619, built 2026-05-07T03:34:27Z)
- Host: Darwin m4x-129.local 25.4.0 Darwin Kernel Version 25.4.0: Thu Mar 19 19:33:25 PDT 2026; root:xnu-12377.101.15~1/RELEASE_ARM64_T6041 arm64
- Image: `agentkit/macos-base:latest`
- Task: `take a screenshot, click button at coords, type hello, take another screenshot`
- Runs per provider: 1
- Live API run: 1
- Run artifacts: `/tmp/cove-agent-sandbox-openai-artifact-live-20260506203544-artifacts`

| Provider | Model | Runs | Median latency s | Error rate | Notes |
| --- | --- | ---: | ---: | ---: | --- |
| openai | default | 1 | 36.628 | 1.00 | mechanical latency only; artifacts: /tmp/cove-agent-sandbox-openai-artifact-live-20260506203544-artifacts/openai |

## Failure Artifact Summary

The run reached the forked macOS VM, connected to the guest agent, and then
failed before the provider task because the local Python environment did not
have the OpenAI Agents SDK adapter installed:

```text
install OpenAI Agents SDK: pip install -e adapters/openai-agents-python[agents]
error: agent-sandbox: openai provider: exit status 1
```

The artifact-preserving harness records stdout/stderr under the result artifact
directory for live runs.
