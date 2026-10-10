package controlserver

import (
	"context"
	"time"
)

func (b *AgentBridge) defaultContext() context.Context {
	if b.host == nil {
		return context.Background()
	}
	return b.host.LifecycleContext()
}

func (b *AgentBridge) lockAgentContext(ctx context.Context, read bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		locked := false
		if read {
			locked = b.mu.TryRLock()
		} else {
			locked = b.mu.TryLock()
		}
		if locked {
			if err := ctx.Err(); err != nil {
				if read {
					b.mu.RUnlock()
				} else {
					b.mu.Unlock()
				}
				return err
			}
			return nil
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
