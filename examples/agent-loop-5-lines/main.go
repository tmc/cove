package main

import (
	"context"

	"github.com/tmc/vz-macos/agentsandbox"
)

func main() {
	ctx := context.Background()
	sb, _ := agentsandbox.New("openai", "agentkit/macos-base:latest")
	defer sb.Close()
	_ = sb.Run(ctx, "Open Safari and search for cove vs lume")
}
