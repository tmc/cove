package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	runmetrics "github.com/tmc/vz-macos/internal/metrics"
)

func TestRunSuccess(t *testing.T) {
	dir := t.TempDir()
	oldCleanupWait := cleanupWait
	cleanupWait = 10 * time.Millisecond
	t.Cleanup(func() { cleanupWait = oldCleanupWait })
	stub := writeStubCove(t, dir, 0)
	out := filepath.Join(dir, "out")
	code := run([]string{"-cove-bin", stub, "-image", "ubuntu-ci", "-command", "echo ok", "-env", "TOKEN=secret\nEMPTY="}, []string{
		"HOME=" + dir,
		"GITHUB_OUTPUT=" + out,
		"GITHUB_RUN_ID=123",
		"GITHUB_RUN_ATTEMPT=2",
		"COVE_STUB_LOG=" + filepath.Join(dir, "log"),
		"COVE_STUB_COUNT=" + filepath.Join(dir, "count"),
	}, os.Stdout, os.Stderr)
	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	got := readFile(t, out)
	for _, want := range []string{
		"vm-name=cove-action-123-2",
		"exit-code=0",
		"log-path=" + filepath.Join(dir, ".vz", "runs"),
		"metrics-path=" + filepath.Join(dir, ".vz", "runs", "stub-run", "metrics.jsonl"),
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("outputs missing %q in:\n%s", want, got)
		}
	}
	events := readEvents(t, filepath.Join(dir, ".vz", "runs", "stub-run", "metrics.jsonl"))
	for _, want := range []string{"action_start", "vm_create", "vm_start", "agent_ready", "command_complete", "action_complete"} {
		if !events[want] {
			t.Fatalf("missing event %q in %#v", want, events)
		}
	}
	log := readFile(t, filepath.Join(dir, "log"))
	for _, want := range []string{
		"run -fork-from ubuntu-ci -fork-name cove-action-123-2 -ephemeral -headless",
		"shell cove-action-123-2 -- /bin/sh -lc true",
		"env 'TOKEN=secret' 'EMPTY=' /bin/sh -lc 'echo ok'",
		"ctl -vm cove-action-123-2 stop",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q in:\n%s", want, log)
		}
	}
}

func TestRunReturnsGuestExitCode(t *testing.T) {
	dir := t.TempDir()
	oldCleanupWait := cleanupWait
	cleanupWait = 10 * time.Millisecond
	t.Cleanup(func() { cleanupWait = oldCleanupWait })
	stub := writeStubCove(t, dir, 7)
	code := run([]string{"-cove-bin", stub, "-image", "ubuntu-ci", "-command", "exit 7"}, []string{
		"HOME=" + dir,
		"COVE_STUB_LOG=" + filepath.Join(dir, "log"),
		"COVE_STUB_COUNT=" + filepath.Join(dir, "count"),
	}, os.Stdout, os.Stderr)
	if code != 7 {
		t.Fatalf("run returned %d, want 7", code)
	}
	log := readFile(t, filepath.Join(dir, "log"))
	if !strings.Contains(log, "ctl -vm cove-action-local-1 stop") {
		t.Fatalf("cleanup did not stop VM:\n%s", log)
	}
}

func TestRunCacheHitUsesCacheImage(t *testing.T) {
	dir := t.TempDir()
	oldCleanupWait := cleanupWait
	cleanupWait = 10 * time.Millisecond
	t.Cleanup(func() { cleanupWait = oldCleanupWait })
	stub := writeStubCove(t, dir, 0)
	stageActionCacheImage(t, dir, "go-main")
	out := filepath.Join(dir, "out")
	code := run([]string{"-cove-bin", stub, "-image", "ubuntu-ci", "-command", "echo ok", "-cache-key", "go-main"}, []string{
		"HOME=" + dir,
		"GITHUB_OUTPUT=" + out,
		"COVE_STUB_LOG=" + filepath.Join(dir, "log"),
		"COVE_STUB_COUNT=" + filepath.Join(dir, "count"),
	}, os.Stdout, os.Stderr)
	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	log := readFile(t, filepath.Join(dir, "log"))
	if !strings.Contains(log, "run -fork-from cache/go-main:latest -fork-name cove-action-local-1 -ephemeral -headless") {
		t.Fatalf("cache hit did not fork from cache image:\n%s", log)
	}
	if strings.Contains(log, "image build") {
		t.Fatalf("cache hit saved unexpectedly:\n%s", log)
	}
	got := readFile(t, out)
	for _, want := range []string{
		"cache-hit=true",
		"cache-image=cache/go-main:latest",
		"cache-saved=false",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("outputs missing %q in:\n%s", want, got)
		}
	}
}

func TestRunCacheExpiredEmitsEvict(t *testing.T) {
	dir := t.TempDir()
	oldCleanupWait := cleanupWait
	cleanupWait = 10 * time.Millisecond
	t.Cleanup(func() { cleanupWait = oldCleanupWait })
	stub := writeStubCove(t, dir, 0)
	out := filepath.Join(dir, "out")
	stageActionCacheImage(t, dir, "go-old")
	cacheDir := filepath.Join(dir, ".vz", "images", "cache", "go-old", "latest")
	if err := os.WriteFile(filepath.Join(cacheDir, "CACHE-TTL"), []byte("1h\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "manifest.json"), []byte(`{"name":"cache/go-old","tag":"latest","createdAt":"2026-05-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code := run([]string{"-cove-bin", stub, "-image", "ubuntu-ci", "-command", "echo ok", "-cache-key", "go-old"}, []string{
		"HOME=" + dir,
		"GITHUB_OUTPUT=" + out,
		"COVE_STUB_LOG=" + filepath.Join(dir, "log"),
		"COVE_STUB_COUNT=" + filepath.Join(dir, "count"),
	}, os.Stdout, os.Stderr)
	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	events := readActionMetricEventsDetailed(t, filepath.Join(dir, ".vz", "runs", "stub-run", "metrics.jsonl"))
	found := false
	for _, e := range events {
		if e.EventType != "run_cache_evict" {
			continue
		}
		found = true
		if got := actionAsInt64(t, e.Extra["bytes_freed"]); got <= 0 {
			t.Fatalf("bytes_freed = %d, want > 0: %#v", got, e)
		}
		if e.Extra["run_id"] == "" || e.Extra["cache_image"] == "" {
			t.Fatalf("evict event missing fields: %#v", e)
		}
	}
	if !found {
		t.Fatalf("missing run_cache_evict in %+v", events)
	}
	if !strings.Contains(readFile(t, out), "cache-saved=false") {
		t.Fatalf("outputs missing cache-saved=false:\n%s", readFile(t, out))
	}
}

func TestRunCacheMissSavesImage(t *testing.T) {
	dir := t.TempDir()
	oldCleanupWait := cleanupWait
	cleanupWait = 10 * time.Millisecond
	t.Cleanup(func() { cleanupWait = oldCleanupWait })
	stub := writeStubCove(t, dir, 0)
	out := filepath.Join(dir, "out")
	code := run([]string{
		"-cove-bin", stub,
		"-image", "ubuntu-ci",
		"-command", "echo ok",
		"-cache-key", "go-main",
		"-cache-paths", "/home/runner/.cache/go-build",
	}, []string{
		"HOME=" + dir,
		"GITHUB_OUTPUT=" + out,
		"COVE_STUB_LOG=" + filepath.Join(dir, "log"),
		"COVE_STUB_COUNT=" + filepath.Join(dir, "count"),
	}, os.Stdout, os.Stderr)
	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	log := readFile(t, filepath.Join(dir, "log"))
	for _, want := range []string{
		"run -fork-from ubuntu-ci -fork-name cove-action-local-1 -ephemeral -headless -keep",
		"image build -from cove-action-local-1 -tag cache/go-main:latest",
		"vm delete cove-action-local-1",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q in:\n%s", want, log)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".vz", "images", "cache", "go-main", "latest", "CACHE-TTL")); err != nil {
		t.Fatalf("CACHE-TTL marker missing: %v", err)
	}
	got := readFile(t, out)
	for _, want := range []string{
		"cache-hit=false",
		"cache-image=cache/go-main:latest",
		"cache-saved=true",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("outputs missing %q in:\n%s", want, got)
		}
	}
}

func TestRunCacheFailureDoesNotSave(t *testing.T) {
	dir := t.TempDir()
	oldCleanupWait := cleanupWait
	cleanupWait = 10 * time.Millisecond
	t.Cleanup(func() { cleanupWait = oldCleanupWait })
	stub := writeStubCove(t, dir, 9)
	code := run([]string{"-cove-bin", stub, "-image", "ubuntu-ci", "-command", "exit 9", "-cache-key", "go-main"}, []string{
		"HOME=" + dir,
		"COVE_STUB_LOG=" + filepath.Join(dir, "log"),
		"COVE_STUB_COUNT=" + filepath.Join(dir, "count"),
	}, os.Stdout, os.Stderr)
	if code != 9 {
		t.Fatalf("run returned %d, want 9", code)
	}
	log := readFile(t, filepath.Join(dir, "log"))
	if strings.Contains(log, "image build") {
		t.Fatalf("failed run saved cache:\n%s", log)
	}
}

func TestRunCacheDuplicateSaveIsNonfatal(t *testing.T) {
	dir := t.TempDir()
	oldCleanupWait := cleanupWait
	cleanupWait = 10 * time.Millisecond
	t.Cleanup(func() { cleanupWait = oldCleanupWait })
	stub := writeStubCove(t, dir, 0)
	out := filepath.Join(dir, "out")
	code := run([]string{"-cove-bin", stub, "-image", "ubuntu-ci", "-command", "echo ok", "-cache-key", "go-main"}, []string{
		"HOME=" + dir,
		"GITHUB_OUTPUT=" + out,
		"COVE_STUB_LOG=" + filepath.Join(dir, "log"),
		"COVE_STUB_COUNT=" + filepath.Join(dir, "count"),
		"COVE_STUB_DUPLICATE_SAVE=1",
	}, os.Stdout, os.Stderr)
	if code != 0 {
		t.Fatalf("run returned %d, want 0", code)
	}
	log := readFile(t, filepath.Join(dir, "log"))
	if !strings.Contains(log, "image build -from cove-action-local-1 -tag cache/go-main:latest") {
		t.Fatalf("duplicate test did not attempt cache save:\n%s", log)
	}
	got := readFile(t, out)
	if !strings.Contains(got, "cache-saved=false") {
		t.Fatalf("duplicate save should not report cache-saved=true:\n%s", got)
	}
}

func TestCacheImageRefNormalizesUnsafeKey(t *testing.T) {
	got := cacheImageRef("linux/go main@abc")
	if got != "cache/linux-go-main-abc:latest" {
		t.Fatalf("cacheImageRef = %q, want cache/linux-go-main-abc:latest", got)
	}
}

func TestParseConfigRequiresCommand(t *testing.T) {
	_, err := parseConfig(nil, []string{"COVE_ACTION_IMAGE=ubuntu-ci"}, os.Stdout, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "command or script is required") {
		t.Fatalf("parseConfig error = %v, want missing command", err)
	}
}

func writeStubCove(t *testing.T, dir string, jobExit int) string {
	t.Helper()
	path := filepath.Join(dir, "cove-stub")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$COVE_STUB_LOG"
case "$1" in
run)
	vm=""
	image=""
	prev=""
	for arg in "$@"; do
		if [ "$prev" = "-fork-name" ]; then vm="$arg"; fi
		if [ "$prev" = "-fork-from" ]; then image="$arg"; fi
		prev="$arg"
	done
	metrics_dir="$HOME/.vz/runs/stub-run"
	mkdir -p "$metrics_dir"
	{
		printf '{"timestamp":"2026-05-05T00:00:00Z","event_type":"vm_create","vm_name":"%s","image_ref":"%s","status":"ok"}\n' "$vm" "$image"
		printf '{"timestamp":"2026-05-05T00:00:01Z","event_type":"fork_created","vm_name":"%s","image_ref":"%s","status":"ok"}\n' "$vm" "$image"
		printf '{"timestamp":"2026-05-05T00:00:02Z","event_type":"vm_start","vm_name":"%s","image_ref":"%s","status":"ok"}\n' "$vm" "$image"
		printf '{"timestamp":"2026-05-05T00:00:03Z","event_type":"agent_ready","vm_name":"%s","image_ref":"%s","status":"ok"}\n' "$vm" "$image"
	} > "$metrics_dir/metrics.jsonl"
	trap 'exit 0' TERM INT
	while :; do sleep 1; done
	;;
shell)
	count=0
	if [ -f "$COVE_STUB_COUNT" ]; then
		count=$(cat "$COVE_STUB_COUNT")
	fi
	next=$((count + 1))
	printf '%s' "$next" > "$COVE_STUB_COUNT"
	if [ "$count" = 0 ]; then
		exit 0
	fi
	exit ` + strconv.Itoa(jobExit) + `
	;;
ctl)
	exit 0
	;;
image)
	if [ "$2" != "build" ]; then exit 2; fi
	from=""
	tag=""
	prev=""
	for arg in "$@"; do
		if [ "$prev" = "-from" ]; then from="$arg"; fi
		if [ "$prev" = "-tag" ]; then tag="$arg"; fi
		prev="$arg"
	done
	if [ "${COVE_STUB_DUPLICATE_SAVE:-}" = "1" ]; then
		echo "image build: image $tag already exists" >&2
		exit 1
	fi
	name=${tag%:*}
	version=${tag##*:}
	img_dir="$HOME/.vz/images/$name/$version"
	if [ -e "$img_dir/manifest.json" ]; then
		echo "image build: image $tag already exists" >&2
		exit 1
	fi
	mkdir -p "$img_dir"
	printf '{"name":"%s","tag":"%s","createdAt":"2026-05-05T00:00:00Z","diskSize":1}\n' "$name" "$version" > "$img_dir/manifest.json"
	printf 'disk' > "$img_dir/disk.img"
	echo "Built image $tag from $from"
	exit 0
	;;
vm)
	exit 0
	;;
*)
	exit 2
	;;
esac
`
	if runtime.GOOS == "windows" {
		t.Skip("shell stub requires sh")
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func stageActionCacheImage(t *testing.T, home, key string) {
	t.Helper()
	dir := filepath.Join(home, ".vz", "images", "cache", key, "latest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{"name":"cache/`+key+`","tag":"latest","createdAt":"2026-05-05T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readActionMetricEventsDetailed(t *testing.T, path string) []runmetrics.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []runmetrics.Event
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var e runmetrics.Event
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func actionAsInt64(t *testing.T, v any) int64 {
	t.Helper()
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		t.Fatalf("unexpected numeric type %T (%v)", v, v)
		return 0
	}
}

func readEvents(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	events := map[string]bool{}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var row struct {
			EventType string `json:"event_type"`
		}
		if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
			t.Fatal(err)
		}
		events[row.EventType] = true
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		b, err := os.ReadFile(path)
		if err == nil {
			return string(b)
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestParseSecretsBlock(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr string
	}{
		{name: "empty"},
		{name: "blank-and-comments", in: "\n  \n# c\n"},
		{name: "literal", in: "FOO=bar", want: []string{"FOO=bar"}},
		{name: "env-uri", in: "GH=env://GH_TOKEN", want: []string{"GH=env://GH_TOKEN"}},
		{name: "file-uri", in: "K=file:///tmp/k", want: []string{"K=file:///tmp/k"}},
		{name: "multi", in: "A=1\nB=env://B\nC=file:///c\n", want: []string{"A=1", "B=env://B", "C=file:///c"}},
		{name: "trim-line", in: "  A=1  \n", want: []string{"A=1"}},
		{name: "value-with-equals", in: "K=a=b=c", want: []string{"K=a=b=c"}},
		{name: "no-equals", in: "BAREWORD", wantErr: "want KEY=value"},
		{name: "empty-key", in: "=v", wantErr: "empty key"},
		{name: "duplicate", in: "A=1\nA=2", wantErr: "duplicate secret key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSecretsBlock(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Fatalf("got[%d]=%q, want %q", i, v, tt.want[i])
				}
			}
		})
	}
}

func TestParseEnvBlock(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr string
	}{
		{name: "empty"},
		{name: "blank-and-comments", in: "\n  \n# c\n  # d\n"},
		{name: "single", in: "FOO=bar", want: []string{"FOO=bar"}},
		{name: "multi", in: "A=1\nB=2\n", want: []string{"A=1", "B=2"}},
		{name: "trim-line", in: "  A=1  ", want: []string{"A=1"}},
		{name: "value-with-equals", in: "URL=http://x?k=v", want: []string{"URL=http://x?k=v"}},
		{name: "empty-value-allowed", in: "EMPTY=", want: []string{"EMPTY="}},
		{name: "no-equals", in: "BAREWORD", wantErr: "invalid env entry"},
		{name: "comment-after-content-not-stripped", in: "A=1 # not a comment", want: []string{"A=1 # not a comment"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEnvBlock(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Fatalf("got[%d]=%q, want %q", i, v, tt.want[i])
				}
			}
		})
	}
}

func TestParseSecretsBlockMoreEdges(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr string
	}{
		{name: "key-with-hyphen", in: "MY-KEY=v", want: []string{"MY-KEY=v"}},
		{name: "key-with-dot", in: "my.key=v", want: []string{"my.key=v"}},
		{name: "trailing-blank-line", in: "A=1\n\n", want: []string{"A=1"}},
		{name: "comment-mid-block", in: "A=1\n# skip\nB=2", want: []string{"A=1", "B=2"}},
		{name: "indented-comment", in: "  # c\nA=1", want: []string{"A=1"}},
		{name: "empty-value", in: "A=", want: []string{"A="}},
		{name: "whitespace-only-key", in: "   =v", wantErr: "empty key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSecretsBlock(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want contains %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Fatalf("got[%d]=%q, want %q", i, v, tt.want[i])
				}
			}
		})
	}
}

func TestParseConfigSecretsFromEnv(t *testing.T) {
	environ := []string{
		"COVE_ACTION_IMAGE=img:tag",
		"COVE_ACTION_COMMAND=true",
		"COVE_ACTION_SECRETS=GH=env://GH_TOKEN\nDB=file:///tmp/db",
	}
	cfg, err := parseConfig(nil, environ, os.Stdout, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"GH=env://GH_TOKEN", "DB=file:///tmp/db"}
	if len(cfg.Secrets) != len(want) {
		t.Fatalf("Secrets = %v, want %v", cfg.Secrets, want)
	}
	for i, v := range want {
		if cfg.Secrets[i] != v {
			t.Fatalf("Secrets[%d] = %q, want %q", i, cfg.Secrets[i], v)
		}
	}
}

func TestParseConfigCacheModeValidation(t *testing.T) {
	base := []string{"COVE_ACTION_IMAGE=ubuntu-ci", "COVE_ACTION_COMMAND=echo ok"}
	cases := []struct {
		name    string
		mode    string
		want    string
		wantErr bool
	}{
		{"default", "", "restore-save", false},
		{"restore-save", "restore-save", "restore-save", false},
		{"restore-only", "restore-only", "restore-only", false},
		{"save-only", "save-only", "save-only", false},
		{"off", "off", "off", false},
		{"invalid", "bogus", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := append([]string{}, base...)
			if tc.mode != "" {
				env = append(env, "COVE_ACTION_CACHE_MODE="+tc.mode)
			}
			cfg, err := parseConfig(nil, env, os.Stdout, os.Stderr)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseConfig(%q) err = nil, want error", tc.mode)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseConfig(%q) err = %v", tc.mode, err)
			}
			if cfg.CacheMode != tc.want {
				t.Fatalf("CacheMode = %q, want %q", cfg.CacheMode, tc.want)
			}
		})
	}
}

func TestRunCacheModeDispatch(t *testing.T) {
	cases := []struct {
		name        string
		mode        string
		stageHit    bool
		wantForkRef string
		wantSave    bool
	}{
		{"off-skips-restore-and-save", "off", true, "ubuntu-ci", false},
		{"restore-only-skips-save", "restore-only", false, "ubuntu-ci", false},
		{"save-only-skips-restore", "save-only", true, "ubuntu-ci", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			oldCleanupWait := cleanupWait
			cleanupWait = 10 * time.Millisecond
			t.Cleanup(func() { cleanupWait = oldCleanupWait })
			stub := writeStubCove(t, dir, 0)
			if tc.stageHit {
				stageActionCacheImage(t, dir, "k1")
			}
			out := filepath.Join(dir, "out")
			code := run([]string{
				"-cove-bin", stub, "-image", "ubuntu-ci", "-command", "echo ok",
				"-cache-key", "k1", "-cache-mode", tc.mode,
			}, []string{
				"HOME=" + dir,
				"GITHUB_OUTPUT=" + out,
				"COVE_STUB_LOG=" + filepath.Join(dir, "log"),
				"COVE_STUB_COUNT=" + filepath.Join(dir, "count"),
			}, os.Stdout, os.Stderr)
			if code != 0 {
				t.Fatalf("run returned %d, want 0", code)
			}
			log := readFile(t, filepath.Join(dir, "log"))
			if !strings.Contains(log, "run -fork-from "+tc.wantForkRef+" ") {
				t.Fatalf("expected fork-from %q in log:\n%s", tc.wantForkRef, log)
			}
			savedLog := strings.Contains(log, "image build")
			if savedLog != tc.wantSave {
				t.Fatalf("save in log = %v, want %v:\n%s", savedLog, tc.wantSave, log)
			}
		})
	}
}

func TestScopedCacheKey(t *testing.T) {
	cases := []struct {
		name  string
		scope string
		key   string
		want  string
	}{
		{"empty scope passes through", "", "go-main", "go-main"},
		{"whitespace scope passes through", "   ", "go-main", "go-main"},
		{"repo scope prefixes", "octo/cove", "go-main", "octo/cove:go-main"},
		{"scope is trimmed", "  octo/cove\n", "go-main", "octo/cove:go-main"},
		{"scopes isolate identical keys", "branch/main", "deps", "branch/main:deps"},
		{"empty key keeps separator-free passthrough", "octo/cove", "", "octo/cove:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopedCacheKey(tc.scope, tc.key); got != tc.want {
				t.Fatalf("scopedCacheKey(%q,%q) = %q, want %q", tc.scope, tc.key, got, tc.want)
			}
		})
	}
}

func TestCacheImageRefHonorsScope(t *testing.T) {
	a := cacheImageRef(scopedCacheKey("octo/cove", "go-main"))
	b := cacheImageRef(scopedCacheKey("octo/other", "go-main"))
	if a == b {
		t.Fatalf("different scopes should produce different cache refs, both = %q", a)
	}
	if c := cacheImageRef(scopedCacheKey("", "go-main")); c != cacheImageRef("go-main") {
		t.Fatalf("empty scope should preserve historical ref, got %q vs %q", c, cacheImageRef("go-main"))
	}
}

// TestParseConfigCacheFlagEnvPrecedence covers cache-mode and cache-scope
// resolution across (a) flag-wins-over-env, (b) env-when-flag-absent, and
// (c) defaults when neither is set.
func TestParseConfigCacheFlagEnvPrecedence(t *testing.T) {
	base := []string{"COVE_ACTION_IMAGE=ubuntu-ci", "COVE_ACTION_COMMAND=echo ok"}
	cases := []struct {
		name      string
		env       []string
		args      []string
		wantMode  string
		wantScope string
	}{
		{
			name:      "defaults when neither flag nor env",
			wantMode:  "restore-save",
			wantScope: "",
		},
		{
			name:      "env supplies mode and scope when flags absent",
			env:       []string{"COVE_ACTION_CACHE_MODE=restore-only", "COVE_ACTION_CACHE_SCOPE=octo/cove"},
			wantMode:  "restore-only",
			wantScope: "octo/cove",
		},
		{
			name:      "flag wins over env for mode",
			env:       []string{"COVE_ACTION_CACHE_MODE=off"},
			args:      []string{"-cache-mode", "save-only"},
			wantMode:  "save-only",
			wantScope: "",
		},
		{
			name:      "flag wins over env for scope",
			env:       []string{"COVE_ACTION_CACHE_SCOPE=env/scope"},
			args:      []string{"-cache-scope", "flag/scope"},
			wantMode:  "restore-save",
			wantScope: "flag/scope",
		},
		{
			name:      "both flags override both env vars",
			env:       []string{"COVE_ACTION_CACHE_MODE=off", "COVE_ACTION_CACHE_SCOPE=env/scope"},
			args:      []string{"-cache-mode", "restore-only", "-cache-scope", "flag/scope"},
			wantMode:  "restore-only",
			wantScope: "flag/scope",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := append(append([]string{}, base...), tc.env...)
			cfg, err := parseConfig(tc.args, env, os.Stdout, os.Stderr)
			if err != nil {
				t.Fatalf("parseConfig: %v", err)
			}
			if cfg.CacheMode != tc.wantMode {
				t.Fatalf("CacheMode = %q, want %q", cfg.CacheMode, tc.wantMode)
			}
			if cfg.CacheScope != tc.wantScope {
				t.Fatalf("CacheScope = %q, want %q", cfg.CacheScope, tc.wantScope)
			}
		})
	}
}
