package main

import (
	"strings"
	"testing"
)

func TestParseFindings(t *testing.T) {
	concerns := profiles["soft-reset"]
	got, err := parseFindings(concerns, []string{"tcc=pass:clean", "daemon=limit:plist survives"})
	if err != nil {
		t.Fatalf("parseFindings: %v", err)
	}
	if got["tcc"].Status != "pass" || got["tcc"].Note != "clean" {
		t.Fatalf("tcc finding = %+v", got["tcc"])
	}
	if got["daemon"].Status != "limit" || got["daemon"].Note != "plist survives" {
		t.Fatalf("daemon finding = %+v", got["daemon"])
	}
}

func TestParseFindingsRejectsUnknownConcern(t *testing.T) {
	if _, err := parseFindings(profiles["soft-reset"], []string{"weird=pass"}); err == nil {
		t.Fatalf("parseFindings unknown concern: got nil error")
	}
}

func TestConcernsForProfile(t *testing.T) {
	concerns, err := concernsForProfile("boundary")
	if err != nil {
		t.Fatalf("concernsForProfile: %v", err)
	}
	if len(concerns) != 6 {
		t.Fatalf("len(boundary concerns) = %d, want 6", len(concerns))
	}
	if _, err := concernsForProfile("weird"); err == nil {
		t.Fatalf("concernsForProfile unknown profile: got nil error")
	}
}

func TestRenderMatrix(t *testing.T) {
	concerns := profiles["soft-reset"]
	findings, err := parseFindings(concerns, []string{"tcc=fail:permission leaked"})
	if err != nil {
		t.Fatalf("parseFindings: %v", err)
	}
	doc := renderMatrix("soft-reset", concerns, "soft-reset-vm", "m4", findings)
	for _, want := range []string{
		"# cove soft-reset isolation matrix",
		"- Profile: `soft-reset`",
		"- VM: `soft-reset-vm`",
		"| TCC residue | fail |",
		"- Fail: 1",
		"- Pending: 5",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("markdown missing %q:\n%s", want, doc)
		}
	}
}

func TestRenderBoundaryMatrix(t *testing.T) {
	concerns := profiles["boundary"]
	findings, err := parseFindings(concerns, []string{"disk=pass:bounded"})
	if err != nil {
		t.Fatalf("parseFindings: %v", err)
	}
	doc := renderMatrix("boundary", concerns, "", "", findings)
	for _, want := range []string{
		"# cove boundary isolation matrix",
		"| Disk-write boundary | pass |",
		"- Pass: 1",
		"- Pending: 5",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("markdown missing %q:\n%s", want, doc)
		}
	}
}

func TestConcernIDs(t *testing.T) {
	ids := concernIDs(profiles["boundary"])
	if len(ids) != 6 {
		t.Fatalf("len(concernIDs) = %d, want 6", len(ids))
	}
	if ids[0] != "disk" {
		t.Fatalf("ids[0] = %q, want disk", ids[0])
	}
}
