package main

import (
	"strings"
	"testing"
)

func TestRunWorkerChildEnvEnablesSandboxMacgo(t *testing.T) {
	got := runWorkerChildEnv([]string{
		"PATH=/bin",
		"COVE_APP_SANDBOX_MACGO=0",
		"GOPATH=/tmp/go",
	})
	if strings.Join(got, "\n") != "PATH=/bin\nGOPATH=/tmp/go\nCOVE_APP_SANDBOX_MACGO=1" {
		t.Fatalf("runWorkerChildEnv() = %q", got)
	}
}

func TestFirstJSONObjectBytes(t *testing.T) {
	got, err := firstJSONObjectBytes([]byte("warning\n{\"ok\":true}\ntrailer\n"))
	if err != nil {
		t.Fatalf("firstJSONObjectBytes: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("firstJSONObjectBytes = %q", got)
	}
}
