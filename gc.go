package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func handleGCCommand(args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "print disposable clones without deleting them")
	olderThan := fs.Duration("older-than", 0, "only delete disposable clones older than this duration")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, err := GCDisposableClones(DisposableGCOptions{
		OlderThan: *olderThan,
		DryRun:    *dryRun,
	})
	if err != nil {
		return err
	}

	for _, path := range result.Paths {
		if *dryRun {
			fmt.Printf("would remove %s\n", path)
			continue
		}
		fmt.Printf("removed %s\n", path)
	}
	if len(result.Paths) == 0 {
		state := "No disposable clones matched."
		if *dryRun {
			state = "No disposable clones would be removed."
		}
		fmt.Println(state)
	}
	fmt.Printf("scanned=%d candidates=%d removed=%d skipped-active=%d older-than=%s\n",
		result.Scanned,
		result.Candidates,
		result.Removed,
		result.SkippedAlive,
		olderThanString(*olderThan),
	)
	return nil
}

func olderThanString(d time.Duration) string {
	if d <= 0 {
		return "any"
	}
	return d.String()
}
