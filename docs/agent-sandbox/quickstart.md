# Agent Sandbox Quickstart

From zero to a fork-isolated provider loop:

```bash
git clone git@github.com:tmc/cove.git
cd cove
go build -o cove ./cmd/cove
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
export PATH="$PWD:$PATH"

cove run -fork-from agentkit/macos-base:latest -fork-name agent-smoke -ephemeral -gui
cove agent-sandbox run --provider anthropic --image agentkit/macos-base:latest --task "Take a screenshot and describe the desktop."
```

Provider credentials:

```bash
export OPENAI_API_KEY=sk-...
export ANTHROPIC_API_KEY=sk-ant-...
export GEMINI_API_KEY=...
export GOOGLE_CLOUD_PROJECT=my-project
```

Check auth and network reachability:

```bash
cove agent-sandbox doctor --provider all
```

`cove agent-sandbox run` also checks the selected provider credential before it
starts a VM fork. If a key or project is missing, it exits immediately with the
matching `doctor --provider ...` command.

Switch provider with one flag:

```bash
cove agent-sandbox run --provider gemini --image agentkit/macos-base:latest --task "Open Safari."
cove agent-sandbox run --provider vertex --image agentkit/macos-base:latest --task "Open Safari."
```

Every successful run prints three paths:

```text
agent-sandbox run: ~/.vz/runs/<run-id>
agent-sandbox replay: ~/.vz/runs/<run-id>/replay
agent-sandbox summary: ~/.vz/runs/<run-id>/replay/summary.md
```

The replay bundle includes `summary.md`, screenshots, OCR text, control events,
the final answer, and a metrics symlink. Use a dedicated throwaway guest session
for agent runs; cove isolates the VM fork, but the current macOS capture/control
path is not a Cua Driver-style focus-safe background automation guarantee.

Export the run bundle when you need to hand it to CI or another operator:

```bash
run_id=<run-id>
cove runs export "$run_id" --format tar > "agent-sandbox-$run_id.tar.gz"
cove runs export "$run_id" --format gha-summary >> "$GITHUB_STEP_SUMMARY"
```

Provider benchmark protocols live in `bench/agent-sandbox-providers/`. Use
`cove agent-sandbox bench --provider all` to record the protocol without API
calls, or add `--live` after `doctor --provider all` passes on the host.
