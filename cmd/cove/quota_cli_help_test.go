package main

import (
	"bytes"
	"context"
	"testing"
)

func TestRunQuotaHelpReturnsNil(t *testing.T) {
	for _, alias := range []string{"-h", "--help"} {
		var out bytes.Buffer
		if err := runQuota(context.Background(), []string{alias}, nil, &out); err != nil {
			t.Fatalf("runQuota(%q) = %v, want nil", alias, err)
		}
	}
}
