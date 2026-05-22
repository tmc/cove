package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tmc/cove/internal/ociimage"
)

func TestRegistryAuthorization(t *testing.T) {
	ref := ociimage.Reference{Registry: "ghcr.io"}

	t.Run("explicit token", func(t *testing.T) {
		clearRegistryAuthEnv(t)
		if got, want := registryAuthorization(ref, " explicit "), "Bearer explicit"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
	})

	t.Run("docker auth before env", func(t *testing.T) {
		clearRegistryAuthEnv(t)
		writeDockerConfig(t, `{"auths":{"ghcr.io":{"auth":"dXNlcjpwYXNz"}}}`)
		t.Setenv("COVE_REGISTRY_TOKEN", "env-token")
		if got, want := registryAuthorization(ref, ""), "Basic dXNlcjpwYXNz"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
	})

	t.Run("docker username password", func(t *testing.T) {
		clearRegistryAuthEnv(t)
		writeDockerConfig(t, `{"auths":{"https://ghcr.io":{"username":"me","password":"secret"}}}`)
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("me:secret"))
		if got := registryAuthorization(ref, ""); got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
	})

	t.Run("docker identity token", func(t *testing.T) {
		clearRegistryAuthEnv(t)
		writeDockerConfig(t, `{"auths":{"ghcr.io":{"identitytoken":"identity"}}}`)
		if got, want := registryAuthorization(ref, ""), "Bearer identity"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
	})

	t.Run("cove token", func(t *testing.T) {
		clearRegistryAuthEnv(t)
		t.Setenv("COVE_REGISTRY_TOKEN", "cove-token")
		if got, want := registryAuthorization(ref, ""), "Bearer cove-token"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
	})

	t.Run("github token", func(t *testing.T) {
		clearRegistryAuthEnv(t)
		t.Setenv("GITHUB_TOKEN", "github-token")
		if got, want := registryAuthorization(ref, ""), "Bearer github-token"; got != want {
			t.Fatalf("authorization = %q, want %q", got, want)
		}
	})

	t.Run("no github token outside ghcr", func(t *testing.T) {
		clearRegistryAuthEnv(t)
		t.Setenv("GITHUB_TOKEN", "github-token")
		if got := registryAuthorization(ociimage.Reference{Registry: "registry.example.com"}, ""); got != "" {
			t.Fatalf("authorization = %q, want empty", got)
		}
	})
}

func TestRegistryAuthorizationCredentialHelper(t *testing.T) {
	clearRegistryAuthEnv(t)
	binDir := t.TempDir()
	helper := filepath.Join(binDir, "docker-credential-testhelper")
	writeHelper(t, helper)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeDockerConfig(t, `{"credHelpers":{"ghcr.io":"testhelper"}}`)

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("helper-user:helper-secret"))
	if got := registryAuthorization(ociimage.Reference{Registry: "ghcr.io"}, ""); got != want {
		t.Fatalf("authorization = %q, want %q", got, want)
	}
}

func clearRegistryAuthEnv(t *testing.T) {
	t.Helper()

	t.Setenv("COVE_REGISTRY_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("DOCKER_CONFIG", t.TempDir())
}

func writeDockerConfig(t *testing.T, data string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0644); err != nil {
		t.Fatalf("WriteFile(config.json) error = %v", err)
	}
	t.Setenv("DOCKER_CONFIG", dir)
}

func writeHelper(t *testing.T, path string) {
	t.Helper()

	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s\\n' '{\"Username\":\"helper-user\",\"Secret\":\"helper-secret\"}'\n"
	if runtime.GOOS == "windows" {
		t.Skip("shell helper test requires sh")
	}
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile(helper) error = %v", err)
	}
}
