package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/store"
)

func TestParseBuildScriptMeta(t *testing.T) {
	data := []byte(`# test
# cache-env: BUILD_NUMBER, FEATURE
# cache-url: https://example.test/version
# cache-file: ./go.mod ./go.sum
# cache-ttl: 7d
# secret: GITHUB_TOKEN OPENAI_API_KEY
# compact: thorough

exec echo ok
`)
	got, err := parseBuildScriptMeta(data)
	if err != nil {
		t.Fatalf("parseBuildScriptMeta(): %v", err)
	}
	if !reflect.DeepEqual(got.CacheEnv, []string{"BUILD_NUMBER", "FEATURE"}) {
		t.Fatalf("CacheEnv = %#v", got.CacheEnv)
	}
	if got.CacheTTL != 7*24*time.Hour {
		t.Fatalf("CacheTTL = %s, want 168h", got.CacheTTL)
	}
	if got.Compact != "thorough" {
		t.Fatalf("Compact = %q", got.Compact)
	}
	if !reflect.DeepEqual(got.Secrets, []string{"GITHUB_TOKEN", "OPENAI_API_KEY"}) {
		t.Fatalf("Secrets = %#v", got.Secrets)
	}
}

func TestBuildCacheKeySortsDeclaredInputs(t *testing.T) {
	t.Setenv("A", "1")
	t.Setenv("B", "2")
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(fileA, []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.TrimPrefix(r.URL.Path, "/"))
	}))
	defer srv.Close()

	data := []byte("exec echo ok\n")
	stepA := buildStep{Name: "test", Source: "test.vzscript", Data: data, Meta: buildScriptMeta{
		CacheEnv:  []string{"B", "A"},
		CacheURL:  []string{srv.URL + "/b", srv.URL + "/a"},
		CacheFile: []string{fileB, fileA},
		Secrets:   []string{"Z", "A"},
		Compact:   "targeted",
	}}
	stepB := buildStep{Name: "test", Source: "test.vzscript", Data: data, Meta: buildScriptMeta{
		CacheEnv:  []string{"A", "B"},
		CacheURL:  []string{srv.URL + "/a", srv.URL + "/b"},
		CacheFile: []string{fileA, fileB},
		Secrets:   []string{"A", "Z"},
		Compact:   "targeted",
	}}
	stepA.Meta.CacheEnv = uniqueSorted(stepA.Meta.CacheEnv)
	stepA.Meta.CacheURL = uniqueSorted(stepA.Meta.CacheURL)
	stepA.Meta.CacheFile = uniqueSorted(stepA.Meta.CacheFile)
	stepA.Meta.Secrets = uniqueSorted(stepA.Meta.Secrets)
	stepB.Meta.CacheEnv = uniqueSorted(stepB.Meta.CacheEnv)
	stepB.Meta.CacheURL = uniqueSorted(stepB.Meta.CacheURL)
	stepB.Meta.CacheFile = uniqueSorted(stepB.Meta.CacheFile)
	stepB.Meta.Secrets = uniqueSorted(stepB.Meta.Secrets)
	keyA, _, err := buildCacheKey(context.Background(), "sha256:parent", stepA, srv.Client())
	if err != nil {
		t.Fatalf("buildCacheKey(A): %v", err)
	}
	keyB, _, err := buildCacheKey(context.Background(), "sha256:parent", stepB, srv.Client())
	if err != nil {
		t.Fatalf("buildCacheKey(B): %v", err)
	}
	if keyA != keyB {
		t.Fatalf("keys differ for sorted-equivalent inputs:\nA=%s\nB=%s", keyA, keyB)
	}
}

func TestBuildCacheKeyRejectsMount(t *testing.T) {
	step := testBuildStep(`# mount: ~/src ro

exec echo ok
`)
	_, _, err := buildCacheKey(context.Background(), "sha256:parent", step, nil)
	if err == nil {
		t.Fatal("buildCacheKey() error = nil, want mount rejection")
	}
	if !strings.Contains(err.Error(), "not allowed in `cove build` context") {
		t.Fatalf("error = %q", err)
	}
}

func TestBuildDryPlanChainsKeys(t *testing.T) {
	dir := t.TempDir()
	step1 := filepath.Join(dir, "one.vzscript")
	step2 := filepath.Join(dir, "two.vzscript")
	if err := os.WriteFile(step1, []byte("exec echo one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(step2, []byte("exec echo two\n"), 0644); err != nil {
		t.Fatal(err)
	}
	opts := buildOptions{Base: "ghcr.io/acme/base@sha256:base", Scripts: []string{step1, step2}, Compact: "targeted"}
	plan, err := buildDryPlan(context.Background(), "vm", opts, nil)
	if err != nil {
		t.Fatalf("buildDryPlan(): %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(plan.Steps))
	}
	if plan.Steps[0].Key == plan.Steps[1].Key {
		t.Fatalf("chained step keys should differ: %s", plan.Steps[0].Key)
	}
	if plan.Steps[0].ParentDigest != plan.ParentDigest {
		t.Fatalf("step 1 parent digest = %q, want %q", plan.Steps[0].ParentDigest, plan.ParentDigest)
	}
	if plan.Steps[1].ParentDigest != plan.Steps[0].Key {
		t.Fatalf("step 2 parent digest = %q, want %q", plan.Steps[1].ParentDigest, plan.Steps[0].Key)
	}
	if plan.Steps[0].ScriptDigest == "" || plan.Steps[1].ScriptDigest == "" {
		t.Fatalf("script digests = %q, %q; want non-empty", plan.Steps[0].ScriptDigest, plan.Steps[1].ScriptDigest)
	}
	if plan.Steps[0].Source != step1 || string(plan.Steps[0].Data) != "exec echo one\n" {
		t.Fatalf("step 1 source/data = %q/%q, want script contents", plan.Steps[0].Source, plan.Steps[0].Data)
	}
	if plan.Steps[1].Source != step2 || string(plan.Steps[1].Data) != "exec echo two\n" {
		t.Fatalf("step 2 source/data = %q/%q, want script contents", plan.Steps[1].Source, plan.Steps[1].Data)
	}
}

func TestBuildDryPlanReportsLocalCacheHit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "one.vzscript")
	if err := os.WriteFile(script, []byte("exec echo one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := store.New(filepath.Join(dir, "store"))
	opts := buildOptions{Base: "ghcr.io/acme/base@sha256:base", Scripts: []string{script}, Compact: "targeted"}
	plan, err := buildDryPlanWithStore(context.Background(), "vm", opts, nil, s)
	if err != nil {
		t.Fatalf("buildDryPlanWithStore(): %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(plan.Steps))
	}
	layer := digestBytes([]byte("layer"))
	if err := saveBuildCacheEntry(s, buildCacheEntry{Key: plan.Steps[0].Key, LayerDigest: layer}); err != nil {
		t.Fatalf("saveBuildCacheEntry(): %v", err)
	}
	plan, err = buildDryPlanWithStore(context.Background(), "vm", opts, nil, s)
	if err != nil {
		t.Fatalf("buildDryPlanWithStore(): %v", err)
	}
	if !plan.Steps[0].CacheHit || plan.Steps[0].LayerDigest != layer {
		t.Fatalf("cache hit = %v layer = %q, want hit %q", plan.Steps[0].CacheHit, plan.Steps[0].LayerDigest, layer)
	}
	opts.NoCache = true
	plan, err = buildDryPlanWithStore(context.Background(), "vm", opts, nil, s)
	if err != nil {
		t.Fatalf("buildDryPlanWithStore(no-cache): %v", err)
	}
	if plan.Steps[0].CacheHit {
		t.Fatal("CacheHit = true with NoCache")
	}
}

func TestHandleBuildAcceptsDocumentedFlagOrder(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hello.vzscript")
	if err := os.WriteFile(script, []byte("exec echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := handleBuild([]string{
		"test-image",
		"--base", "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64),
		"--script", script,
		"--dry-run",
	})
	if err != nil {
		t.Fatalf("handleBuild(): %v", err)
	}
}

func TestHandleBuildReportsCacheHitWithStoreDir(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hello.vzscript")
	if err := os.WriteFile(script, []byte("exec echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(dir, "store")
	opts := buildOptions{
		Base:     "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64),
		Scripts:  []string{script},
		Compact:  "targeted",
		StoreDir: storeDir,
	}
	plan, err := buildDryPlan(context.Background(), "test-image", opts, nil)
	if err != nil {
		t.Fatalf("buildDryPlan(): %v", err)
	}
	layer := digestBytes([]byte("layer"))
	if err := saveBuildCacheEntry(store.New(storeDir), buildCacheEntry{Key: plan.Steps[0].Key, LayerDigest: layer}); err != nil {
		t.Fatalf("saveBuildCacheEntry(): %v", err)
	}
	out, err := captureStdoutResult(t, func() error {
		return handleBuild([]string{
			"test-image",
			"--base", opts.Base,
			"--script", script,
			"--store-dir", storeDir,
			"--dry-run",
		})
	})
	if err != nil {
		t.Fatalf("handleBuild(): %v", err)
	}
	if !strings.Contains(out, "cache: hit ("+layer+")") {
		t.Fatalf("output missing cache hit %q:\n%s", layer, out)
	}
}

func TestSplitBuildArgs(t *testing.T) {
	args := []string{
		"test-image",
		"--base", "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64),
		"--script=one",
		"--script", "two",
		"--dry-run",
		"--tag", "out",
		"--store-dir", "/tmp/store",
	}
	flagArgs, posArgs, err := splitBuildArgs(args)
	if err != nil {
		t.Fatalf("splitBuildArgs(): %v", err)
	}
	if !reflect.DeepEqual(posArgs, []string{"test-image"}) {
		t.Fatalf("posArgs = %#v", posArgs)
	}
	wantFlags := []string{
		"--base", "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64),
		"--script=one",
		"--script", "two",
		"--dry-run",
		"--tag", "out",
		"--store-dir", "/tmp/store",
	}
	if !reflect.DeepEqual(flagArgs, wantFlags) {
		t.Fatalf("flagArgs = %#v, want %#v", flagArgs, wantFlags)
	}
}

func TestHandleBuildRequiresDryRun(t *testing.T) {
	err := handleBuild([]string{"--base", "ghcr.io/acme/base:latest", "--script", "missing.vzscript", "vm"})
	if err == nil {
		t.Fatal("handleBuild() error = nil, want dry-run-only error")
	}
	if !strings.Contains(err.Error(), "only --dry-run is implemented") {
		t.Fatalf("handleBuild() error = %q", err)
	}
}

func testBuildStep(data string) buildStep {
	meta, err := parseBuildScriptMeta([]byte(data))
	if err != nil {
		panic(err)
	}
	return buildStep{Name: "test", Source: "test.vzscript", Data: []byte(data), Meta: meta}
}
