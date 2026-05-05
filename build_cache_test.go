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
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestParseBuildScriptMeta(t *testing.T) {
	data := []byte(`# test
# cache-env: BUILD_NUMBER, FEATURE
# cache-url: https://example.test/version
# cache-file: ./go.mod ./go.sum
# cache-ttl: 7d
# secret: GITHUB_TOKEN OPENAI_API_KEY
# secret-from: API_TOKEN=env://API_TOKEN
# secret-from: SIGNING_KEY=file:///tmp/signing-key
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
	wantRefs := []buildSecretRef{
		{Name: "API_TOKEN", URI: "env://API_TOKEN", Line: 7},
		{Name: "SIGNING_KEY", URI: "file:///tmp/signing-key", Line: 8},
	}
	if !reflect.DeepEqual(got.SecretFrom, wantRefs) {
		t.Fatalf("SecretFrom = %#v, want %#v", got.SecretFrom, wantRefs)
	}
}

func TestParseBuildScriptMetaSecretFromErrors(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "missing equals", line: "# secret-from: TOKEN", want: "missing '='"},
		{name: "empty name", line: "# secret-from: =env://TOKEN", want: `invalid secret name ""`},
		{name: "invalid name", line: "# secret-from: BAD/NAME=env://TOKEN", want: `invalid secret name "BAD/NAME"`},
		{name: "missing scheme", line: "# secret-from: TOKEN=TOKEN", want: `missing scheme`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseBuildScriptMeta([]byte(tt.line + "\n\nexec echo ok\n"))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseBuildScriptMeta() = %v, want %q", err, tt.want)
			}
		})
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
		SecretFrom: []buildSecretRef{
			{Name: "REF_B", URI: "env://B"},
			{Name: "REF_A", URI: "env://A"},
		},
		Compact: "targeted",
	}}
	stepB := buildStep{Name: "test", Source: "test.vzscript", Data: data, Meta: buildScriptMeta{
		CacheEnv:  []string{"A", "B"},
		CacheURL:  []string{srv.URL + "/a", srv.URL + "/b"},
		CacheFile: []string{fileA, fileB},
		Secrets:   []string{"A", "Z"},
		SecretFrom: []buildSecretRef{
			{Name: "REF_A", URI: "env://A"},
			{Name: "REF_B", URI: "env://B"},
		},
		Compact: "targeted",
	}}
	stepA.Meta.CacheEnv = uniqueSorted(stepA.Meta.CacheEnv)
	stepA.Meta.CacheURL = uniqueSorted(stepA.Meta.CacheURL)
	stepA.Meta.CacheFile = uniqueSorted(stepA.Meta.CacheFile)
	stepA.Meta.Secrets = uniqueSorted(stepA.Meta.Secrets)
	stepA.Meta.SecretFrom = sortedBuildSecretRefs(stepA.Meta.SecretFrom)
	stepB.Meta.CacheEnv = uniqueSorted(stepB.Meta.CacheEnv)
	stepB.Meta.CacheURL = uniqueSorted(stepB.Meta.CacheURL)
	stepB.Meta.CacheFile = uniqueSorted(stepB.Meta.CacheFile)
	stepB.Meta.Secrets = uniqueSorted(stepB.Meta.Secrets)
	stepB.Meta.SecretFrom = sortedBuildSecretRefs(stepB.Meta.SecretFrom)
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

func TestBuildCacheKeyIncludesSecretFromURIOnly(t *testing.T) {
	t.Setenv("TOKEN", "one")
	step := buildStep{Name: "test", Source: "test.vzscript", Data: []byte("exec echo ok\n"), Meta: buildScriptMeta{
		SecretFrom: []buildSecretRef{{Name: "TOKEN", URI: "env://TOKEN"}},
		Compact:    "targeted",
	}}
	keyA, inA, err := buildCacheKey(context.Background(), "sha256:parent", step, nil)
	if err != nil {
		t.Fatalf("buildCacheKey(A): %v", err)
	}
	t.Setenv("TOKEN", "two")
	keyB, inB, err := buildCacheKey(context.Background(), "sha256:parent", step, nil)
	if err != nil {
		t.Fatalf("buildCacheKey(B): %v", err)
	}
	if keyA != keyB {
		t.Fatalf("secret value changed cache key: %s != %s", keyA, keyB)
	}
	step.Meta.SecretFrom[0].URI = "env://OTHER"
	keyC, _, err := buildCacheKey(context.Background(), "sha256:parent", step, nil)
	if err != nil {
		t.Fatalf("buildCacheKey(C): %v", err)
	}
	if keyC == keyA {
		t.Fatal("secret URI change did not change cache key")
	}
	if !reflect.DeepEqual(inA.SecretFrom, []string{"TOKEN=env://TOKEN"}) || !reflect.DeepEqual(inB.SecretFrom, []string{"TOKEN=env://TOKEN"}) {
		t.Fatalf("SecretFrom inputs = %#v %#v", inA.SecretFrom, inB.SecretFrom)
	}
}

func TestValidateBuildStepSecretsSecretFromDuplicate(t *testing.T) {
	t.Setenv("BUILD_SECRET", "legacy")
	err := validateBuildStepSecrets(buildPlanStep{Name: "test", Meta: buildScriptMeta{
		Secrets:    []string{"BUILD_SECRET"},
		SecretFrom: []buildSecretRef{{Name: "BUILD_SECRET", URI: "env://OTHER", Line: 1}},
	}})
	if err == nil || !strings.Contains(err.Error(), "secret BUILD_SECRET declared more than once") {
		t.Fatalf("validateBuildStepSecrets() = %v, want duplicate error", err)
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
	opts := buildOptions{Base: "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64), Scripts: []string{step1, step2}, Compact: "targeted"}
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

func TestBuildDryPlanAcceptsLocalBaseDir(t *testing.T) {
	dir := t.TempDir()
	parentDir := filepath.Join(dir, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":   "base image\n",
		"aux.img":    "aux",
		"hw.model":   "hw",
		"machine.id": "machine",
	} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(dir, "one.vzscript")
	if err := os.WriteFile(script, []byte("exec echo one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	opts := buildOptions{Base: parentDir, Scripts: []string{script}, Compact: "targeted"}
	plan, err := buildDryPlan(context.Background(), "vm", opts, nil)
	if err != nil {
		t.Fatalf("buildDryPlan(): %v", err)
	}
	if plan.ParentDigest == "" || plan.Steps[0].ParentDigest != plan.ParentDigest {
		t.Fatalf("parent digest = %q step parent = %q", plan.ParentDigest, plan.Steps[0].ParentDigest)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "disk.img"), []byte("changed image\n"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := buildDryPlan(context.Background(), "vm", opts, nil)
	if err != nil {
		t.Fatalf("buildDryPlan(changed): %v", err)
	}
	if changed.ParentDigest == plan.ParentDigest {
		t.Fatalf("parent digest did not change after disk mutation: %s", plan.ParentDigest)
	}
}

func TestBuildDryPlanLocalBaseRequiresMetadata(t *testing.T) {
	dir := t.TempDir()
	parentDir := filepath.Join(dir, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "disk.img"), []byte("base image\n"), 0644); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "one.vzscript")
	if err := os.WriteFile(script, []byte("exec echo one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	opts := buildOptions{Base: parentDir, Scripts: []string{script}, Compact: "targeted"}
	_, err := buildDryPlan(context.Background(), "vm", opts, nil)
	if err == nil {
		t.Fatal("buildDryPlan() error = nil, want missing metadata")
	}
	if !strings.Contains(err.Error(), "aux.img") {
		t.Fatalf("buildDryPlan() = %v, want missing aux.img", err)
	}
}

func TestBuildDryPlanReportsLocalCacheHit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "one.vzscript")
	if err := os.WriteFile(script, []byte("exec echo one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := store.New(filepath.Join(dir, "store"))
	opts := buildOptions{Base: "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64), Scripts: []string{script}, Compact: "targeted"}
	plan, err := buildDryPlanWithStore(context.Background(), "vm", opts, nil, s)
	if err != nil {
		t.Fatalf("buildDryPlanWithStore(): %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(plan.Steps))
	}
	layer := digestBytes([]byte("layer"))
	if err := saveBuildCacheEntry(s, testCacheEntryForStep(plan.Steps[0], layer)); err != nil {
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

func TestBuildDryPlanRejectsMismatchedLocalCacheHit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "one.vzscript")
	if err := os.WriteFile(script, []byte("exec echo one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := store.New(filepath.Join(dir, "store"))
	opts := buildOptions{Base: "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64), Scripts: []string{script}, Compact: "targeted"}
	plan, err := buildDryPlanWithStore(context.Background(), "vm", opts, nil, s)
	if err != nil {
		t.Fatalf("buildDryPlanWithStore(): %v", err)
	}
	layer := digestBytes([]byte("layer"))
	entry := testCacheEntryForStep(plan.Steps[0], layer)
	entry.ScriptDigest = digestBytes([]byte("other script"))
	if err := saveBuildCacheEntry(s, entry); err != nil {
		t.Fatalf("saveBuildCacheEntry(): %v", err)
	}
	_, err = buildDryPlanWithStore(context.Background(), "vm", opts, nil, s)
	if err == nil {
		t.Fatal("buildDryPlanWithStore() error = nil, want script digest mismatch")
	}
	if !strings.Contains(err.Error(), "script digest") {
		t.Fatalf("buildDryPlanWithStore() = %v, want script digest mismatch", err)
	}
}

func TestBuildDryPlanExpiresLocalCacheHit(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "one.vzscript")
	if err := os.WriteFile(script, []byte("# cache-ttl: 1h\nexec echo one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := store.New(filepath.Join(dir, "store"))
	opts := buildOptions{Base: "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64), Scripts: []string{script}, Compact: "targeted"}
	plan, err := buildDryPlanWithStore(context.Background(), "vm", opts, nil, s)
	if err != nil {
		t.Fatalf("buildDryPlanWithStore(): %v", err)
	}
	layer := digestBytes([]byte("layer"))
	entry := testCacheEntryForStep(plan.Steps[0], layer)
	entry.CreatedAt = time.Now().Add(-2 * time.Hour).UTC()
	if err := saveBuildCacheEntry(s, entry); err != nil {
		t.Fatalf("saveBuildCacheEntry(): %v", err)
	}
	plan, err = buildDryPlanWithStore(context.Background(), "vm", opts, nil, s)
	if err != nil {
		t.Fatalf("buildDryPlanWithStore(expired): %v", err)
	}
	if plan.Steps[0].CacheHit {
		t.Fatalf("CacheHit = true for expired entry created at %s", entry.CreatedAt)
	}
	entry.CreatedAt = time.Now().UTC()
	if err := saveBuildCacheEntry(s, entry); err != nil {
		t.Fatalf("saveBuildCacheEntry(fresh): %v", err)
	}
	plan, err = buildDryPlanWithStore(context.Background(), "vm", opts, nil, s)
	if err != nil {
		t.Fatalf("buildDryPlanWithStore(fresh): %v", err)
	}
	if !plan.Steps[0].CacheHit || plan.Steps[0].LayerDigest != layer {
		t.Fatalf("cache hit = %v layer = %q, want hit %q", plan.Steps[0].CacheHit, plan.Steps[0].LayerDigest, layer)
	}
}

func TestBuildPlanWarnings(t *testing.T) {
	plan := buildPlan{Steps: []buildPlanStep{{
		Name: "env",
		Meta: buildScriptMeta{
			CacheEnv: []string{"BUILD_NUMBER", "GITHUB_TOKEN", "OPENAI_API_KEY", "DB_PASSWORD"},
			Compact:  "targeted",
		},
	}, {
		Name: "fast-secret",
		Meta: buildScriptMeta{
			Secrets: []string{"SIGNING_KEY"},
			Compact: "fast",
		},
	}}}
	warnings := buildPlanWarnings(plan)
	for _, want := range []string{"GITHUB_TOKEN", "OPENAI_API_KEY", "DB_PASSWORD", "compact: fast"} {
		if !containsBuildWarning(warnings, want) {
			t.Fatalf("warnings missing %q: %#v", want, warnings)
		}
	}
	if containsBuildWarning(warnings, "BUILD_NUMBER") {
		t.Fatalf("warnings included non-secret cache env: %#v", warnings)
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
	if err := saveBuildCacheEntry(store.New(storeDir), testCacheEntryForStep(plan.Steps[0], layer)); err != nil {
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

func TestHandleBuildRejectsRegistryCacheRefs(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "hello.vzscript")
	if err := os.WriteFile(script, []byte("exec echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := handleBuild([]string{
		"test-image",
		"--base", "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64),
		"--script", script,
		"--cache-from", "ghcr.io/acme/build-cache:cache",
		"--cache-to", "ghcr.io/acme/build-cache:cache",
		"--dry-run",
	})
	if err == nil {
		t.Fatal("handleBuild() error = nil, want unsupported registry cache error")
	}
	for _, want := range []string{"--cache-from", "--cache-to", "not implemented"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("handleBuild() error = %q, want %q", err, want)
		}
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

func TestHandleBuildRunsLocalBase(t *testing.T) {
	restoreControl := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restoreControl()
	oldStart := defaultBuildGuestStart
	oldCompact := defaultBuildCompact
	defer func() {
		defaultBuildGuestStart = oldStart
		defaultBuildCompact = oldCompact
	}()
	defaultBuildGuestStart = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		return func(context.Context) error { return nil }, nil
	}
	defaultBuildCompact = func(context.Context, buildScratch, string) error { return nil }

	home := t.TempDir()
	t.Setenv("HOME", home)
	parentDir := filepath.Join(home, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":   "base image\n",
		"aux.img":    "aux",
		"hw.model":   "hw",
		"machine.id": "machine",
	} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(home, "hello.vzscript")
	if err := os.WriteFile(script, []byte("echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdoutResult(t, func() error {
		return handleBuild([]string{
			"test-image",
			"--base", parentDir,
			"--script", script,
			"--store-dir", filepath.Join(home, "store"),
		})
	})
	if err != nil {
		t.Fatalf("handleBuild(): %v", err)
	}
	if !strings.Contains(out, "Build complete") || !strings.Contains(out, "steps: 1") {
		t.Fatalf("output missing build result:\n%s", out)
	}
}

func TestHandleBuildPushesTags(t *testing.T) {
	restoreControl := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restoreControl()
	oldStart := defaultBuildGuestStart
	oldCompact := defaultBuildCompact
	oldPusher := defaultBuildResultPusher
	defer func() {
		defaultBuildGuestStart = oldStart
		defaultBuildCompact = oldCompact
		defaultBuildResultPusher = oldPusher
	}()
	defaultBuildGuestStart = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		return func(context.Context) error { return nil }, nil
	}
	defaultBuildCompact = func(context.Context, buildScratch, string) error { return nil }
	var pushed []string
	var pushedDir string
	var pushedChunkSize int64
	defaultBuildResultPusher = func(ctx context.Context, vmDir, ref string, opts pushOptions) error {
		pushedDir = vmDir
		pushed = append(pushed, ref)
		pushedChunkSize = opts.ChunkSize
		return nil
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	parentDir := filepath.Join(home, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":   "base image\n",
		"aux.img":    "aux",
		"hw.model":   "hw",
		"machine.id": "machine",
	} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(home, "hello.vzscript")
	if err := os.WriteFile(script, []byte("echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdoutResult(t, func() error {
		return handleBuild([]string{
			"test-image",
			"--base", parentDir,
			"--script", script,
			"--tag", "ghcr.io/me/test:v1",
			"--tag", "ghcr.io/me/test:latest",
			"--push",
			"--chunk-size", "1",
			"--store-dir", filepath.Join(home, "store"),
		})
	})
	if err != nil {
		t.Fatalf("handleBuild(): %v", err)
	}
	if !reflect.DeepEqual(pushed, []string{"ghcr.io/me/test:v1", "ghcr.io/me/test:latest"}) {
		t.Fatalf("pushed refs = %#v", pushed)
	}
	if pushedDir == "" {
		t.Fatal("pushed vm dir is empty")
	}
	if pushedChunkSize != 1<<20 {
		t.Fatalf("pushed chunk size = %d, want %d", pushedChunkSize, int64(1<<20))
	}
	if !strings.Contains(out, "pushed: 2") {
		t.Fatalf("output missing push count:\n%s", out)
	}
}

func TestHandleBuildNonDryRunRequiresLocalBase(t *testing.T) {
	err := handleBuild([]string{"--base", "ghcr.io/acme/base:latest", "--script", "missing.vzscript", "vm"})
	if err == nil {
		t.Fatal("handleBuild() error = nil, want local-base error")
	}
	if !strings.Contains(err.Error(), "requires local VM base directory") {
		t.Fatalf("handleBuild() error = %q", err)
	}
}

func TestHandleBuildPushRequiresTag(t *testing.T) {
	err := handleBuild([]string{"--base", "ghcr.io/acme/base:latest", "--script", "missing.vzscript", "--push", "vm"})
	if err == nil {
		t.Fatal("handleBuild() error = nil, want tag error")
	}
	if !strings.Contains(err.Error(), "--push requires at least one --tag") {
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

func containsBuildWarning(list []string, substr string) bool {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
