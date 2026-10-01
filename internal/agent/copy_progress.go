package agent

import "context"

type copyProgressKey struct{}

// WithCopyProgress reports bytes transferred by copy streams in ctx.
// Total is zero when the stream size is unknown.
func WithCopyProgress(ctx context.Context, report func(bytes, total int64)) context.Context {
	return context.WithValue(ctx, copyProgressKey{}, report)
}

func reportCopyProgress(ctx context.Context, bytes, total int64) {
	if report, ok := ctx.Value(copyProgressKey{}).(func(int64, int64)); ok && report != nil {
		report(bytes, total)
	}
}
