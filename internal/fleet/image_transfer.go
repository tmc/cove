package fleet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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
	var srcStderr, dstStderr bytes.Buffer
	go func() {
		defer pw.Close()
		err := runner.Run(ctx, src, []string{"image", "push", ref, "-"}, nil, pw, &srcStderr)
		if err != nil {
			_ = pw.CloseWithError(err)
			errc <- wrapTransferErr("source image push", err, &srcStderr)
			return
		}
		errc <- nil
	}()
	go func() {
		defer pr.Close()
		err := runner.Run(ctx, dst, []string{"image", "load", "-"}, pr, io.Discard, &dstStderr)
		if err != nil {
			errc <- wrapTransferErr("destination image load", err, &dstStderr)
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

func wrapTransferErr(stage string, err error, stderr *bytes.Buffer) error {
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		return fmt.Errorf("%s: %w", stage, err)
	}
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return fmt.Errorf("%s: %w: %s", stage, err, msg)
}
