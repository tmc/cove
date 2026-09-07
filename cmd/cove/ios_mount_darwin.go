package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tmc/cove/internal/ios/firmware"
)

func runIOSMountBridge(env commandEnv, args []string) int {
	flags := flag.NewFlagSet("ios firmware _mount", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	directory := flags.String("journal", "", "attempt mount journal")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	rest := flags.Args()
	if *directory == "" || len(rest) == 0 {
		return commandUsageError(env, fmt.Errorf("mount journal and operation are required"))
	}
	operation := rest[0]
	rest = rest[1:]
	readonly := false
	if operation == "attach" {
		attach := flag.NewFlagSet("attach", flag.ContinueOnError)
		attach.SetOutput(env.Stderr)
		attach.BoolVar(&readonly, "readonly", false, "read-only image")
		if err := attach.Parse(rest); err != nil {
			return 2
		}
		rest = attach.Args()
	}
	counts := map[string]int{"attach": 1, "detach": 1, "unmount": 1, "remount": 2, "recover": 0}
	count, ok := counts[operation]
	if !ok || len(rest) != count {
		return commandUsageError(env, fmt.Errorf("invalid mount bridge operation or arguments"))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	journal, err := firmware.OpenMountJournal(*directory)
	if err != nil {
		return commandError(env, err)
	}
	defer journal.Close()
	switch operation {
	case "attach":
		var data []byte
		data, err = journal.Attach(ctx, rest[0], readonly)
		if err == nil {
			_, err = env.Stdout.Write(data)
			if err != nil {
				cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				err = errors.Join(err, journal.Recover(cleanup))
				cancel()
			}
		}
	case "detach":
		err = journal.Detach(ctx, rest[0])
	case "unmount":
		err = journal.Unmount(ctx, rest[0])
	case "remount":
		err = journal.Remount(ctx, rest[0], rest[1])
	case "recover":
		err = journal.Recover(ctx)
	}
	if err != nil {
		return commandError(env, err)
	}
	return 0
}
