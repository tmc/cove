package fleet

import (
	"context"
	"time"
)

const DefaultQueryTimeout = 10 * time.Second

type QueryFunc[T any] func(context.Context, Entry) (T, error)

type FleetResult[T any] struct {
	Host   string
	Remote Remote
	Value  T
	Error  error
}

func QueryAll[T any](ctx context.Context, entries []Entry, fn QueryFunc[T]) []FleetResult[T] {
	return QueryAllWithTimeout(ctx, entries, DefaultQueryTimeout, fn)
}

func QueryAllWithTimeout[T any](ctx context.Context, entries []Entry, timeout time.Duration, fn QueryFunc[T]) []FleetResult[T] {
	if timeout <= 0 {
		timeout = DefaultQueryTimeout
	}
	results := make([]FleetResult[T], len(entries))
	done := make(chan int, len(entries))
	for i, entry := range entries {
		i, entry := i, entry
		results[i].Host = entry.Name
		results[i].Remote = entry.Remote
		go func() {
			qctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			value, err := fn(qctx, entry)
			results[i].Value = value
			results[i].Error = err
			done <- i
		}()
	}
	for range entries {
		<-done
	}
	return results
}
