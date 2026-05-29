package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/tmc/cove/internal/disposable"
)

func handleGCCommand(args []string) error {
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "print VMs without deleting them")
	olderThan := fs.Duration("older-than", 0, "only delete disposable clones older than this duration")
	if err := fs.Parse(args); err != nil {
		return err
	}

	disposable, err := GCDisposableClones(disposable.GCOptions{
		OlderThan: *olderThan,
		DryRun:    *dryRun,
	})
	if err != nil {
		return err
	}
	for _, path := range disposable.Paths {
		if *dryRun {
			fmt.Printf("would remove disposable %s\n", path)
			continue
		}
		fmt.Printf("removed disposable %s\n", path)
	}

	ephemeral, err := GCEphemeralForks(EphemeralGCOptions{DryRun: *dryRun})
	if err != nil {
		return err
	}
	for _, path := range ephemeral.Paths {
		if *dryRun {
			fmt.Printf("would remove ephemeral %s\n", path)
			continue
		}
		fmt.Printf("removed ephemeral %s\n", path)
	}

	if len(disposable.Paths) == 0 && len(ephemeral.Paths) == 0 {
		state := "No disposable clones or ephemeral forks matched."
		if *dryRun {
			state = "No disposable clones or ephemeral forks would be removed."
		}
		fmt.Println(state)
	}
	fmt.Printf("disposable: scanned=%d candidates=%d removed=%d skipped-active=%d older-than=%s\n",
		disposable.Scanned,
		disposable.Candidates,
		disposable.Removed,
		disposable.SkippedAlive,
		olderThanString(*olderThan),
	)
	fmt.Printf("ephemeral:  scanned=%d candidates=%d removed=%d skipped-active=%d\n",
		ephemeral.Scanned,
		ephemeral.Candidates,
		ephemeral.Removed,
		ephemeral.SkippedAlive,
	)
	return nil
}

func olderThanString(d time.Duration) string {
	if d <= 0 {
		return "any"
	}
	return d.String()
}
