# Agent sandbox provider latency

- Date: 2026-05-07T03:29:50Z
- Cove: cove 301972d8ad52 (commit 301972d8ad52, built 2026-05-07T03:26:42Z)
- Host: Darwin m4x-129.local 25.4.0 Darwin Kernel Version 25.4.0: Thu Mar 19 19:33:25 PDT 2026; root:xnu-12377.101.15~1/RELEASE_ARM64_T6041 arm64
- Image: `agentkit/macos-base:latest`
- Task: `take a screenshot, click button at coords, type hello, take another screenshot`
- Runs per provider: 1
- Live API run: 1

| Provider | Model | Runs | Median latency s | Error rate | Notes |
| --- | --- | ---: | ---: | ---: | --- |
| openai | default | 1 | 57.144 | 1.00 | mechanical latency only; not quality |
