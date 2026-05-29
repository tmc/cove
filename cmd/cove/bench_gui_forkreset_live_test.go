package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/guibench"
)

// TestForkResetInvariant_Live empirically proves the §6 invariant the benchmark
// relies on: a fresh RAM-overlay fork does NOT inherit the prior task's mutated
// state. It mutates the state classes a verifier reads — a cfprefsd-cached
// preference, a Launch Services / TCC-adjacent user store, and an app
// SQLite/WAL file — in fork A, discards fork A, then asserts fork B (a fresh
// fork of the same base image) sees none of those mutations.
//
// This requires live Apple-Silicon hardware, a runnable base image, and a guest
// agent, so it is gated behind COVE_GUIBENCH_LIVE=1 (the COVE_* live-test
// convention used elsewhere) and is a no-op skip otherwise. An operator runs:
//
//	make build && codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
//	COVE_GUIBENCH_LIVE=1 COVE_GUIBENCH_IMAGE=<image-ref> go test -run TestForkResetInvariant_Live -tags '' ./ -v -timeout 30m
//
// The test never fakes a pass: with the guard off it skips, and any probe error
// fails it rather than being swallowed.
func TestForkResetInvariant_Live(t *testing.T) {
	if os.Getenv("COVE_GUIBENCH_LIVE") != "1" {
		t.Skip("set COVE_GUIBENCH_LIVE=1 (and COVE_GUIBENCH_IMAGE) to run the live fork-reset invariant test")
	}
	image := os.Getenv("COVE_GUIBENCH_IMAGE")
	if image == "" {
		t.Fatal("COVE_GUIBENCH_LIVE=1 set but COVE_GUIBENCH_IMAGE is empty; name a base image to fork from")
	}

	// The reset invariant is a property of the substrate, not of any provider;
	// no agent runs, so the provider label is cosmetic. Tier A suffices: the
	// mutations and reads are user-space exec.
	backend, err := newVZForkBackend("none", guibench.TierA, os.Stderr, os.Stderr)
	if err != nil {
		t.Fatalf("backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	const (
		prefDomain = "com.cove.guibench.forkreset"
		prefKey    = "bled"
		prefValue  = "fork-a-was-here"
		markerFile = "/Users/Shared/.cove-guibench-forkreset-marker"
		dbPath     = "/Users/Shared/.cove-guibench-forkreset.db"
	)

	// Fork A: mutate the three state classes, then discard it.
	a, err := backend.Acquire(ctx, image)
	if err != nil {
		t.Fatalf("acquire fork A: %v", err)
	}
	pa := a.Probe()
	mustExec(t, pa, "fork A: write cfprefsd pref",
		[]string{"defaults", "write", prefDomain, prefKey, prefValue})
	// Force the pref through cfprefsd to disk so a naive reset would persist it.
	mustExec(t, pa, "fork A: flush cfprefsd", []string{"killall", "cfprefsd"})
	mustExec(t, pa, "fork A: drop filesystem marker",
		[]string{"sh", "-c", "echo fork-a > " + markerFile})
	mustExec(t, pa, "fork A: write app SQLite (WAL)",
		[]string{"sqlite3", dbPath, "PRAGMA journal_mode=WAL; CREATE TABLE t(v); INSERT INTO t VALUES('fork-a');"})
	if err := a.Close(); err != nil {
		t.Fatalf("close fork A: %v", err)
	}

	// Fork B: a fresh fork of the same base image must see none of fork A's
	// mutations. The RAM overlay threw fork A's writes away on shutdown.
	b, err := backend.Acquire(ctx, image)
	if err != nil {
		t.Fatalf("acquire fork B: %v", err)
	}
	defer b.Close()
	pb := b.Probe()

	// cfprefsd-cached preference: the pref domain/key must be absent. `defaults
	// read` of a missing key exits nonzero.
	if exit, stdout, _, err := pb.Exec([]string{"defaults", "read", prefDomain, prefKey}, nil, ""); err != nil {
		t.Fatalf("fork B: read pref: %v", err)
	} else if exit == 0 {
		t.Fatalf("fork B inherited fork A's cfprefsd pref %s/%s = %q (fork did not reset preferences)",
			prefDomain, prefKey, strings.TrimSpace(stdout))
	}

	// Filesystem / Launch Services-adjacent user store: the marker file must be
	// gone. `test -e` exits 0 when present.
	if exit, _, _, err := pb.Exec([]string{"test", "-e", markerFile}, nil, ""); err != nil {
		t.Fatalf("fork B: test marker: %v", err)
	} else if exit == 0 {
		t.Fatalf("fork B inherited fork A's filesystem marker %s (fork did not reset the filesystem)", markerFile)
	}

	// App SQLite/WAL store: the db file must be gone (and so its WAL).
	if exit, _, _, err := pb.Exec([]string{"test", "-e", dbPath}, nil, ""); err != nil {
		t.Fatalf("fork B: test db: %v", err)
	} else if exit == 0 {
		t.Fatalf("fork B inherited fork A's SQLite store %s (fork did not reset app databases)", dbPath)
	}
}

// mustExec runs args in the guest through p and fails the test on a transport
// error or a nonzero exit, so a failed mutation never reads as a passing reset.
func mustExec(t *testing.T, p guibench.Probe, what string, args []string) {
	t.Helper()
	exit, _, stderr, err := p.Exec(args, nil, "")
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if exit != 0 {
		t.Fatalf("%s: exit %d: %s", what, exit, strings.TrimSpace(stderr))
	}
}
