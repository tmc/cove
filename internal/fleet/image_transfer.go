package fleet

import (
	"context"
	"errors"
	"fmt"
	"io"
)

type CommandRunner interface {
	Run(ctx context.Context, remote Remote, args []string, stdin io.Reader, stdout, stderr io.Writer) error
}

func PushImage(ctx context.Context, ref string, src, dst Remote, runner CommandRunner) error {
	return TransferImage(ctx, ref, src, dst, runner)
}

func PullImage(ctx context.Context, ref string, src, dst Remote, runner CommandRunner) error {
	return TransferImage(ctx, ref, src, dst, runner)
}

func TransferImage(ctx context.Context, ref string, src, dst Remote, runner CommandRunner) error {
	if ref == "" {
		return errors.New("fleet image transfer: image ref required")
	}
	if runner == nil {
		return errors.New("fleet image transfer: runner required")
	}
	pr, pw := io.Pipe()
	errc := make(chan error, 2)
	go func() {
		defer pw.Close()
		err := runner.Run(ctx, src, []string{"image", "push", ref, "-"}, nil, pw, io.Discard)
		if err != nil {
			_ = pw.CloseWithError(err)
			errc <- fmt.Errorf("source image push: %w", err)
			return
		}
		errc <- nil
	}()
	go func() {
		defer pr.Close()
		err := runner.Run(ctx, dst, []string{"image", "load", "-"}, pr, io.Discard, io.Discard)
		if err != nil {
			errc <- fmt.Errorf("destination image load: %w", err)
			return
		}
		errc <- nil
	}()
	var first error
	for i := 0; i < 2; i++ {
		if err := <-errc; err != nil && first == nil {
			first = err
		}
	}
	return first
}
