package main

import (
	"strings"
	"testing"
)

func TestBuildNoArgsShowsUsage(t *testing.T) {
	err := handleBuild(nil)
	if err == nil || !strings.Contains(err.Error(), "name required") {
		t.Fatalf("err = %v, want name required", err)
	}
}
