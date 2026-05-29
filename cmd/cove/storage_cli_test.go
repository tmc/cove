package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStorageNoArgsShowsUsage(t *testing.T) {
	err := handleStorageCommand(nil)
	if err == nil || !strings.Contains(err.Error(), "command required") {
		t.Fatalf("err = %v, want command required", err)
	}
}

func TestStorageHelpUsage(t *testing.T) {
	var b strings.Builder
	printStorageUsage(&b)
	for _, want := range []string{"Usage: cove storage", "census", "budget", "prune"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("usage missing %q:\n%s", want, b.String())
		}
	}
}

func TestStorageCensusJSONUsageError(t *testing.T) {
	var out strings.Builder
	err := runStorageCensus([]string{"extra"}, &out)
	if err == nil {
		t.Fatal("runStorageCensus extra arg succeeded")
	}
	var got cliJSONError
	if jsonErr := json.Unmarshal([]byte(out.String()), &got); jsonErr != nil {
		t.Fatalf("storage census error output is not JSON: %v\n%s", jsonErr, out.String())
	}
	if got.OK || got.Command != "storage census" || !strings.Contains(got.Error, "usage: cove storage census") {
		t.Fatalf("storage census JSON error = %#v", got)
	}
}
