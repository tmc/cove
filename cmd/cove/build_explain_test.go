package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/store"
)

func TestBuildExplanationMatchesEngine(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "input")
	script := filepath.Join(root, "one.vzscript")
	if err := os.WriteFile(file, []byte("one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("# cache-env: EXPLAIN_VALUE\n# cache-file: "+file+"\n# secret: TOKEN\nexec echo hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXPLAIN_VALUE", "do-not-print-secret-value")
	opts := buildOptions{Base: "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64), Scripts: []string{script}, Compact: "targeted"}
	s := store.New(filepath.Join(root, "store"))
	plan, err := buildDryPlanWithStore(context.Background(), "build", opts, nil, s)
	if err != nil {
		t.Fatal(err)
	}
	explain, err := explainBuild(context.Background(), "build", opts, s)
	if err != nil {
		t.Fatal(err)
	}
	if explain.Steps[0].Key != plan.Steps[0].Key || explain.Steps[0].CacheStatus != "miss" {
		t.Fatalf("explanation differs: %+v", explain)
	}
	if err := saveBuildCacheEntry(s, testCacheEntryForStep(plan.Steps[0], digestBytes([]byte("layer")))); err != nil {
		t.Fatal(err)
	}
	explain, err = explainBuild(context.Background(), "build", opts, s)
	if err != nil {
		t.Fatal(err)
	}
	if explain.Steps[0].CacheStatus != "hit" {
		t.Fatalf("cache = %+v", explain.Steps[0])
	}
	var out bytes.Buffer
	if err := printBuildExplanation(&out, explain, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "do-not-print-secret-value") || strings.Contains(out.String(), digestBytes([]byte("do-not-print-secret-value"))) {
		t.Fatal("environment value or fingerprint leaked")
	}
	t.Setenv("EXPLAIN_VALUE", "changed")
	changed, err := explainBuild(context.Background(), "build", opts, s)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Steps[0].Key == explain.Steps[0].Key || changed.Steps[0].CacheStatus != "miss" {
		t.Fatal("changed input reused key")
	}
}

func TestBuildExplanationOffline(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "one.vzscript")
	if err := os.WriteFile(script, []byte("# cache-url: http://user:password@127.0.0.1:1/data?token=do-not-print\nexec echo hello\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"127.0.0.1:1/acme/base:latest", "127.0.0.1:1/acme/base@sha256:" + strings.Repeat("a", 64)} {
		out, err := explainBuild(context.Background(), "build", buildOptions{Base: base, Scripts: []string{script, script}, Compact: "targeted"}, store.New(filepath.Join(root, "store")))
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Steps) != 2 || out.Steps[0].CacheStatus != "unresolved" || out.Steps[1].Key != "" {
			t.Fatalf("unexpected offline identity: %+v", out)
		}
		var data bytes.Buffer
		if err := printBuildExplanation(&data, out, true); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(data.String(), "password") || strings.Contains(data.String(), "do-not-print") {
			t.Fatal("URL credentials leaked")
		}
	}
}

func TestBuildExplanationCacheReasons(t *testing.T) {
	for _, tt := range []struct {
		name     string
		disabled bool
		expired  bool
		want     string
	}{{"disabled", true, false, "disabled"}, {"expired", false, true, "miss"}, {"fresh", false, false, "hit"}} {
		t.Run(tt.name, func(t *testing.T) {
			s := store.New(t.TempDir())
			step := buildPlanStep{Key: digestBytes([]byte("key")), ParentDigest: digestBytes([]byte("parent")), ScriptDigest: digestBytes([]byte("script")), AgentProtocolVersion: agentProtocolVersion, Meta: buildScriptMeta{Compact: "targeted", CacheTTL: time.Hour}}
			entry := testCacheEntryForStep(step, digestBytes([]byte("layer")))
			now := time.Now().UTC()
			entry.CreatedAt = now
			if tt.expired {
				entry.CreatedAt = now.Add(-2 * time.Hour)
			}
			if err := saveBuildCacheEntry(s, entry); err != nil {
				t.Fatal(err)
			}
			status, _, _ := explainBuildCache(s, step, tt.disabled, now)
			if status != tt.want {
				t.Fatalf("status = %s, want %s", status, tt.want)
			}
		})
	}
}

func TestBuildExplainFlagParsing(t *testing.T) {
	flags, pos, err := splitBuildArgs([]string{"demo", "--explain", "--json", "--base", "base", "--script", "one"})
	if err != nil || len(flags) != 6 || len(pos) != 1 || pos[0] != "demo" {
		t.Fatalf("flags=%v pos=%v err=%v", flags, pos, err)
	}
}

func TestBuildExplanationSecretCacheIdentityRedacted(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "secret.vzscript")
	if err := os.WriteFile(script, []byte("# cache-env: ACCESS_TOKEN\nexec true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACCESS_TOKEN", "never-fingerprint")
	out, err := explainBuild(context.Background(), "build", buildOptions{Base: "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64), Scripts: []string{script}, Compact: "targeted"}, store.New(filepath.Join(root, "store")))
	if err != nil {
		t.Fatal(err)
	}
	if out.Steps[0].Key != "" || out.Steps[0].CacheStatus != "redacted" {
		t.Fatalf("secret identity exposed: %+v", out.Steps[0])
	}
}
