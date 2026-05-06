# Agent Sandbox Provider Matrix

`cove agent-sandbox run` uses a fresh fork from a local image, waits for the
guest agent, runs a provider computer-use loop, records a replay bundle, and
stops the fork. The table below describes the provider surface that exists in
this repository today.

| Provider | Unified CLI state | Screenshot | Click | Type | Scroll | Wait | Env vars needed |
| --- | --- | --- | --- | --- | --- | --- | --- |
| OpenAI Agents | Stub in `cove agent-sandbox run`; full Python SDK adapter exists in `adapters/openai-agents-python` | yes in adapter | yes in adapter | yes in adapter | yes in adapter | yes in adapter | `OPENAI_API_KEY` |
| Anthropic computer-use | First-class Go runtime path plus Python sandbox package | yes | yes | yes | yes | yes | `ANTHROPIC_API_KEY` |
| Gemini computer-use | Python bridge invoked by unified CLI | yes | yes | yes | yes | yes | `GEMINI_API_KEY` |
| Vertex AI computer-use | Python bridge invoked by unified CLI | yes | yes | yes | yes | yes | `GOOGLE_CLOUD_PROJECT` or `COVE_VERTEX_PROJECT`; optional `COVE_VERTEX_REGION`; ADC or gcloud auth |

## Notes

- OpenAI is not yet a first-class `cove agent-sandbox run --provider openai`
  provider. The CLI accepts the provider name but returns a clear
  not-supported error until the Agents SDK adapter is wired into the unified
  runner.
- Anthropic is special today: the unified CLI routes it through the Go runtime
  adapter instead of `internal/agentsandbox.Run`.
- Gemini and Vertex use the repository Python bridge scripts:
  `adapters/google-bridge/computer_use.py` and
  `adapters/google-bridge/vertex-ai/computer_use.py`.
- The capability columns mean the provider bridge can issue the action against
  cove's control socket. They do not claim provider model quality or task
  success rate.
- Tests must not call provider APIs. Provider API runs require local credentials
  and should be run manually or through an explicit benchmark command.
