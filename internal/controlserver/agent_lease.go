package controlserver

import (
	"context"

	agentstate "github.com/tmc/cove/internal/agent"
)

// WithCachedUserAgent pins an existing user client while fn runs. It does not
// probe, connect, or repair the agent. The client's HTTP transport may redial.
// The bool reports whether a cached client was present.
func (b *AgentBridge) WithCachedUserAgent(ctx context.Context, fn func(*agentstate.UserAgentClient) error) (bool, error) {
	if err := b.lockAgentContext(ctx, true); err != nil {
		return false, err
	}
	defer b.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if b.userAgent == nil {
		return false, nil
	}
	return true, fn(b.userAgent)
}
