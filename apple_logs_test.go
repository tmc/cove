package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestDefaultAppleLogPredicateShape(t *testing.T) {
	term := `(?:subsystem\s+BEGINSWITH\[c\]|senderImagePath\s+CONTAINS|process\s+CONTAINS)\s+"[^"]+"`
	predicateRE, err := regexp.Compile(`^` + term + `(?:\s+OR\s+` + term + `)*$`)
	if err != nil {
		t.Fatalf("compile predicate regex: %v", err)
	}
	if !predicateRE.MatchString(defaultAppleLogPredicate) {
		t.Fatalf("defaultAppleLogPredicate has unexpected shape: %s", defaultAppleLogPredicate)
	}
	for _, want := range []string{
		`subsystem BEGINSWITH[c] "com.apple.virtualization"`,
		`senderImagePath CONTAINS "Virtualization.framework"`,
		`process CONTAINS "Virtual Machine Service"`,
		`subsystem BEGINSWITH[c] "com.apple.MobileDevice"`,
		`senderImagePath CONTAINS "MobileDevice.framework"`,
	} {
		if !strings.Contains(defaultAppleLogPredicate, want) {
			t.Fatalf("defaultAppleLogPredicate missing %q: %s", want, defaultAppleLogPredicate)
		}
	}
}
