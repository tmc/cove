package main

import (
	"context"
	"strings"
	"testing"
)

func TestWriteBuildSecretInvalidName(t *testing.T) {
	cases := []string{"", "bad/name"}
	for _, name := range cases {
		err := writeBuildSecret(context.Background(), "/tmp/sock", name, []byte("v"))
		if err == nil || !strings.Contains(err.Error(), "invalid secret name") {
			t.Fatalf("err = %v for %q, want invalid secret name", err, name)
		}
	}
}
