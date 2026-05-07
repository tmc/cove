# Agent sandbox provider latency

- Date: 2026-05-07T03:38:02Z
- Cove: cove 54b8c1772dc5 (commit 54b8c1772dc5, built 2026-05-07T03:36:30Z)
- Host: Darwin m4x-129.local 25.4.0 Darwin Kernel Version 25.4.0: Thu Mar 19 19:33:25 PDT 2026; root:xnu-12377.101.15~1/RELEASE_ARM64_T6041 arm64
- Image: `agentkit/macos-base:latest`
- Task: `take a screenshot, click button at coords, type hello, take another screenshot`
- Runs per provider: 1
- Live API run: 1
- Run artifacts: `/tmp/cove-agent-sandbox-openai-installed-live-20260506203802-artifacts`

| Provider | Model | Runs | Median latency s | Error rate | Notes |
| --- | --- | ---: | ---: | ---: | --- |
| openai | default | 1 | 40.477 | 0.00 | mechanical latency only; artifacts: /tmp/cove-agent-sandbox-openai-installed-live-20260506203802-artifacts/openai |

## Verification Notes

This run used a temporary Python 3.12 virtual environment with the local adapter
installed:

```bash
python3.12 -m venv /tmp/cove-r41-openai-agents-venv
/tmp/cove-r41-openai-agents-venv/bin/python -m pip install -e 'adapters/openai-agents-python[agents]'
COVE_AGENT_SANDBOX_PYTHON=/tmp/cove-r41-openai-agents-venv/bin/python RUN_LIVE=1 PROVIDERS=openai RUNS=1 IMAGE=agentkit/macos-base:latest ./bench/agent-sandbox-providers/run.sh
```
