# Agent sandbox provider doctor, 2026-05-12

- Date: 2026-05-12T06:46Z
- Cove: `c22154a9102f95f6547703df4e105b23681aefe9`
- Host: `m4x-129.local`
- Command surface: `cove agent-sandbox doctor --provider <provider>`

| Provider | Env check | Network check | Model check | Status |
| --- | --- | --- | --- | --- |
| openai | pass with `OPENAI_API_KEY` in process env | pass `api.openai.com:443` | pass provider default | preflight pass |
| gemini | pass with `GEMINI_API_KEY` supplied in command env | pass `generativelanguage.googleapis.com:443` | pass `gemini-2.5-computer-use-preview-10-2025` | preflight pass |
| vertex | pass with `GOOGLE_CLOUD_PROJECT=tmcdev` supplied in command env | pass `aiplatform.googleapis.com:443` | pass `gemini-2.5-computer-use-preview-10-2025` | preflight pass |
| anthropic | fail: `ANTHROPIC_API_KEY` unavailable | pass `api.anthropic.com:443` | pass `claude-opus-4-7` | credential-gated |

This is doctor preflight evidence, not full live task evidence. The live matrix
still has two explicit external gates: Anthropic needs a local API key, and
Vertex still needs a project/region/model combination that supports the
`computer_use` tool. The current Vertex capability boundary is recorded in
`results-vertex-model-probe-20260511.md`.
