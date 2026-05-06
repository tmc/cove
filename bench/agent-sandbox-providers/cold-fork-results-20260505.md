# Cold fork to first observable action

- Date: 2026-05-05
- Cove: source tree benchmark harness added in R41
- Host: Apple Silicon macOS local runner
- Image: `agentkit/macos-base:latest`
- First action proxy: first screenshot/control event in replay bundle
- Live API run: not executed in this commit because provider credentials are not available in tests

| Provider | Runs | Median fork-to-first-action s | Error rate | Notes |
| --- | ---: | ---: | ---: | --- |
| openai | 10 | n/a | 1.00 | set RUN_LIVE=1 and OPENAI_API_KEY to collect |
| anthropic | 10 | n/a | 1.00 | set RUN_LIVE=1 and ANTHROPIC_API_KEY to collect |
| gemini | 10 | n/a | 1.00 | set RUN_LIVE=1 and GEMINI_API_KEY to collect |
| vertex | 10 | n/a | 1.00 | set RUN_LIVE=1 and GOOGLE_CLOUD_PROJECT plus ADC to collect |
