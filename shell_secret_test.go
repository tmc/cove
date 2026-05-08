package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/metrics"
)

func TestResolveShellEnvPlain(t *testing.T) {
	m := metrics.NewMasker()
	stderr := &bytes.Buffer{}
	got, err := resolveShellEnv([]string{"FOO=bar", "BAZ=qux"}, nil, m, stderr)
	if err != nil {
		t.Fatalf("resolveShellEnv: %v", err)
	}
	if got["FOO"] != "bar" || got["BAZ"] != "qux" {
		t.Fatalf("env mismatch: %v", got)
	}
	// --env values must NOT be registered with the masker.
	if m.ApplyString("bar qux") != "bar qux" {
		t.Errorf("--env values should not be masked")
	}
}

func TestResolveShellEnvSecretLiteral(t *testing.T) {
	m := metrics.NewMasker()
	stderr := &bytes.Buffer{}
	got, err := resolveShellEnv(nil, []string{"TOKEN=hunter2"}, m, stderr)
	if err != nil {
		t.Fatalf("resolveShellEnv: %v", err)
	}
	if got["TOKEN"] != "hunter2" {
		t.Fatalf("TOKEN = %q, want %q", got["TOKEN"], "hunter2")
	}
	if m.ApplyString("leak hunter2 leak") != "leak *** leak" {
		t.Errorf("secret literal not registered with masker")
	}
}

func TestResolveShellEnvSecretFromEnv(t *testing.T) {
	t.Setenv("R62_SECRET_FIXTURE", "s3kret-from-env")
	m := metrics.NewMasker()
	got, err := resolveShellEnv(nil, []string{"GH_TOKEN=env://R62_SECRET_FIXTURE"}, m, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveShellEnv: %v", err)
	}
	if got["GH_TOKEN"] != "s3kret-from-env" {
		t.Fatalf("GH_TOKEN = %q", got["GH_TOKEN"])
	}
	if m.ApplyString("s3kret-from-env") != "***" {
		t.Errorf("env-resolved secret not masked")
	}
}

func TestResolveShellEnvSecretFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok")
	if err := os.WriteFile(path, []byte("filetoken"), 0600); err != nil {
		t.Fatal(err)
	}
	m := metrics.NewMasker()
	got, err := resolveShellEnv(nil, []string{"TK=file://" + path}, m, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveShellEnv: %v", err)
	}
	if got["TK"] != "filetoken" {
		t.Fatalf("TK = %q", got["TK"])
	}
	if m.ApplyString("filetoken") != "***" {
		t.Errorf("file-resolved secret not masked")
	}
}

func TestResolveShellEnvEmptyValueIsError(t *testing.T) {
	t.Setenv("R62_EMPTY_FIXTURE", "")
	_, err := resolveShellEnv(nil, []string{"X=env://R62_EMPTY_FIXTURE"}, metrics.NewMasker(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-value error, got %v", err)
	}
}

func TestResolveShellEnvMissingEquals(t *testing.T) {
	_, err := resolveShellEnv([]string{"NOPE"}, nil, metrics.NewMasker(), &bytes.Buffer{})
	if err == nil {
		t.Fatalf("expected error for missing '='")
	}
}

func TestResolveShellEnvSecretOverridesEnvWithWarning(t *testing.T) {
	stderr := &bytes.Buffer{}
	got, err := resolveShellEnv([]string{"GH_TOKEN=plain"}, []string{"GH_TOKEN=secret"}, metrics.NewMasker(), stderr)
	if err != nil {
		t.Fatalf("resolveShellEnv: %v", err)
	}
	if got["GH_TOKEN"] != "secret" {
		t.Fatalf("--secret-env should override --env: got %q", got["GH_TOKEN"])
	}
	if !strings.Contains(stderr.String(), "overrides") {
		t.Errorf("expected override warning on stderr, got %q", stderr.String())
	}
}

func TestResolveShellEnvUnknownScheme(t *testing.T) {
	_, err := resolveShellEnv(nil, []string{"X=vault://foo"}, metrics.NewMasker(), &bytes.Buffer{})
	// vault:// is not a registered scheme; secrets.Resolve returns
	// "unsupported secret URI scheme". But isSecretURI only flags the
	// known schemes, so vault://foo is treated as a literal value here.
	// Document the current behaviour: literal pass-through.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
