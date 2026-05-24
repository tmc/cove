package main

import (
	"strings"
	"testing"

	"github.com/tmc/cove/internal/imagestore"
)

func TestVerifyImageCoveCommitMissing(t *testing.T) {
	status, detail := verifyImageCoveCommit(&imagestore.Manifest{})
	if status != imageVerifyWarn {
		t.Fatalf("status = %v, want warn", status)
	}
	if detail != "missing cove_commit" {
		t.Fatalf("detail = %q", detail)
	}
}

func TestVerifyImageCoveCommitWhitespaceOnlyMissing(t *testing.T) {
	status, _ := verifyImageCoveCommit(&imagestore.Manifest{CoveCommit: "   "})
	if status != imageVerifyWarn {
		t.Fatalf("status = %v, want warn", status)
	}
}

func TestVerifyImageCoveCommitMatchesHost(t *testing.T) {
	host := hostVersion()
	if host == "" || host == "dev" || host == "unknown" {
		t.Skip("hostVersion is not comparable in this build")
	}
	status, detail := verifyImageCoveCommit(&imagestore.Manifest{CoveCommit: host})
	if status != imageVerifyPass {
		t.Fatalf("status = %v, want pass", status)
	}
	if !strings.Contains(detail, "matches current") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestVerifyImageCoveCommitFallsBackOnUnknownHost(t *testing.T) {
	host := hostVersion()
	if !(host == "" || host == "dev" || host == "unknown") {
		t.Skip("hostVersion is comparable; default branch unreachable")
	}
	status, detail := verifyImageCoveCommit(&imagestore.Manifest{CoveCommit: "abc1234"})
	if status != imageVerifyWarn {
		t.Fatalf("status = %v, want warn", status)
	}
	if !strings.Contains(detail, "cannot compare") {
		t.Fatalf("detail = %q, want cannot-compare hint", detail)
	}
}
