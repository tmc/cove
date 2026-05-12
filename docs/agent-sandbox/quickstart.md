# Agent Sandbox Quickstart

From zero to a fork-isolated provider loop:

```bash
git clone git@github.com:tmc/cove.git
cd cove
go build -o cove .
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

Switch provider with one flag:

```bash
cove agent-sandbox run --provider gemini --image agentkit/macos-base:latest --task "Open Safari."
cove agent-sandbox run --provider vertex --image agentkit/macos-base:latest --task "Open Safari."
```

Every run writes a replay bundle under `~/.vz/runs/<run-id>/replay`.

Provider benchmark protocols live in `bench/agent-sandbox-providers/`. Use
`cove agent-sandbox bench --provider all` to record the protocol without API
calls, or add `--live` after `doctor --provider all` passes on the host.
