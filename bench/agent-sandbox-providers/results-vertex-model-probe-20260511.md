# Vertex model capability probe

- Date: 2026-05-11T20:32Z
- Project: `tmcdev`
- Region: `us-central1`
- Auth: local Application Default Credentials
- Purpose: identify whether a known available Vertex Gemini model accepts the `computer_use` tool before spending another VM fork.

| Model | Request | Status | Result |
| --- | --- | ---: | --- |
| `gemini-2.5-flash` | `generateContent` with inline PNG and `tools: [{computer_use: {environment: ENVIRONMENT_BROWSER}}]` | 400 | model exists, but Vertex rejects `computer_use` for this model |
| `gemini-2.5-computer-use-preview-10-2025` | same endpoint and tool shape | 404 | publisher model not found or not accessible in `tmcdev/us-central1` |

The actionable Vertex blocker is therefore not project auth. The project can call
Vertex Gemini, but the available model tested here does not support the
computer-use tool, and the computer-use preview model is not accessible in this
project/region.
