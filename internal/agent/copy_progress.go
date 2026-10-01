package agent

import (
	"context"
	"io"
)

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

// CopyProgressWriter reports successful writes as transferred bytes in ctx.
// The total stream size is unknown.
func CopyProgressWriter(ctx context.Context, w io.Writer) io.Writer {
	return &copyProgressWriter{ctx: ctx, w: w}
}

type copyProgressWriter struct {
	ctx   context.Context
	w     io.Writer
	bytes int64
}

func (w *copyProgressWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	w.bytes += int64(n)
	reportCopyProgress(w.ctx, w.bytes, 0)
	return n, err
}
