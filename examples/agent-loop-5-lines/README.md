# 5-line agent loop

This is the smallest Go shape for a fork-isolated local agent loop:

```go
ctx := context.Background()
sb, _ := agentsandbox.New("openai", "agentkit/macos-base:latest")
defer sb.Close()
_ = sb.Run(ctx, "Open Safari and search for cove vs lume")
```

Swap only the first argument to change providers:

```go
agentsandbox.New("openai", "agentkit/macos-base:latest")
agentsandbox.New("anthropic", "agentkit/macos-base:latest")
agentsandbox.New("gemini", "agentkit/macos-base:latest")
agentsandbox.New("vertex", "agentkit/macos-base:latest")
```

Run:

```bash
go run ./examples/agent-loop-5-lines
```
