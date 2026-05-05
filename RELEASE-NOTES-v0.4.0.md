# cove v0.4.0 Release Notes

## Agent Sandboxes

- `cove agent-sandbox run --provider anthropic` now uses the Go Anthropic
  computer-use adapter loop described by Design 022 v2. The loop calls the
  Anthropic Messages API with `computer-use-2025-11-24`, executes
  `computer_20251124` tool-use blocks through the cove control socket, and
  writes an Anthropic transcript JSONL file into the run replay directory.
