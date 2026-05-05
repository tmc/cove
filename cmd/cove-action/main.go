package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	runmetrics "github.com/tmc/vz-macos/internal/metrics"
)

var (
	execCommandContext = exec.CommandContext
	cleanupWait        = 5 * time.Second
)

const cacheImageDefaultTTL = 7 * 24 * time.Hour

type config struct {
	CoveBin    string
	Image      string
	Command    string
	Script     string
	VMName     string
	Timeout    time.Duration
	ReadyEvery time.Duration
	Keep       bool
	CacheKey   string
	CachePaths string
	Env        []string
	Stdout     io.Writer
	Stderr     io.Writer
	Environ    []string
}

type result struct {
	Code        int
	MetricsPath string
	CacheHit    bool
	CacheImage  string
	CacheSaved  bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}

func run(args, environ []string, stdout, stderr io.Writer) int {
	cfg, err := parseConfig(args, environ, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "cove-action: %v\n", err)
		return 2
	}
	res, err := runJob(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "cove-action: %v\n", err)
		if res.Code != 0 {
			_ = writeOutputs(cfg, res)
			return res.Code
		}
		return 1
	}
	if err := writeOutputs(cfg, res); err != nil {
		fmt.Fprintf(stderr, "cove-action: write outputs: %v\n", err)
		return 1
	}
	return res.Code
}

func parseConfig(args, environ []string, stdout, stderr io.Writer) (config, error) {
	cfg := config{
		CoveBin:    envValue(environ, "COVE_ACTION_COVE_BIN", envValue(environ, "COVE_BIN", "cove")),
		Image:      envValue(environ, "COVE_ACTION_IMAGE", ""),
		Command:    envValue(environ, "COVE_ACTION_COMMAND", envValue(environ, "COVE_ARGS", "")),
		Script:     envValue(environ, "COVE_ACTION_SCRIPT", envValue(environ, "COVE_SCRIPT", "")),
		VMName:     envValue(environ, "COVE_ACTION_VM_NAME", ""),
		ReadyEvery: time.Second,
		Stdout:     stdout,
		Stderr:     stderr,
		Environ:    environ,
	}
	timeoutText := envValue(environ, "COVE_ACTION_TIMEOUT", envValue(environ, "COVE_TIMEOUT", "30m"))
	keepText := envValue(environ, "COVE_ACTION_KEEP", "false")
	envText := envValue(environ, "COVE_ACTION_ENV", "")
	cfg.CacheKey = envValue(environ, "COVE_ACTION_CACHE_KEY", "")
	cfg.CachePaths = envValue(environ, "COVE_ACTION_CACHE_PATHS", "")

	fs := flag.NewFlagSet("cove-action", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.CoveBin, "cove-bin", cfg.CoveBin, "cove binary path")
	fs.StringVar(&cfg.Image, "image", cfg.Image, "local cove image to fork from")
	fs.StringVar(&cfg.Command, "command", cfg.Command, "guest shell command")
	fs.StringVar(&cfg.Command, "args", cfg.Command, "guest shell command")
	fs.StringVar(&cfg.Script, "script", cfg.Script, "guest shell script")
	fs.StringVar(&cfg.VMName, "vm-name", cfg.VMName, "ephemeral VM name")
	fs.StringVar(&timeoutText, "timeout", timeoutText, "overall timeout")
	fs.BoolVar(&cfg.Keep, "keep", parseBool(keepText), "keep ephemeral fork")
	fs.StringVar(&envText, "env", envText, "newline-separated KEY=VALUE guest environment")
	fs.StringVar(&cfg.CacheKey, "cache-key", cfg.CacheKey, "whole-VM cache key")
	fs.StringVar(&cfg.CachePaths, "cache-paths", cfg.CachePaths, "newline-separated guest paths preserved by the whole-VM cache")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if fs.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	timeout, err := time.ParseDuration(timeoutText)
	if err != nil || timeout <= 0 {
		return cfg, fmt.Errorf("invalid timeout %q", timeoutText)
	}
	cfg.Timeout = timeout
	cfg.Env, err = parseEnvBlock(envText)
	if err != nil {
		return cfg, err
	}
	if strings.TrimSpace(cfg.Image) == "" {
		return cfg, errors.New("image is required")
	}
	if strings.TrimSpace(cfg.Command) == "" && strings.TrimSpace(cfg.Script) == "" {
		return cfg, errors.New("command or script is required")
	}
	if strings.TrimSpace(cfg.VMName) == "" {
		cfg.VMName = defaultVMName(environ)
	}
	return cfg, nil
}

func runJob(cfg config) (res result, err error) {
	actionStarted := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	forkFrom := cfg.Image
	cacheRef := ""
	cacheKey := strings.TrimSpace(cfg.CacheKey)
	var cacheEvictBytes int64
	var cacheEvictReason string
	if cacheKey != "" {
		cacheRef = cacheImageRef(cacheKey)
		res.CacheImage = cacheRef
		if hit, evict, bytesFreed, reason := cacheImageRestoreState(cfg, cacheRef); hit || evict {
			if evict {
				cacheEvictBytes = bytesFreed
				cacheEvictReason = reason
			}
			if hit {
				forkFrom = cacheRef
				res.CacheHit = true
			}
		}
		if res.CacheHit {
			forkFrom = cacheRef
		}
	}

	runArgs := []string{"run", "-fork-from", forkFrom, "-fork-name", cfg.VMName, "-ephemeral", "-headless"}
	keepForCache := cacheKey != "" && !res.CacheHit
	if cfg.Keep || keepForCache {
		runArgs = append(runArgs, "-keep")
	}
	runCmd := execCommandContext(ctx, cfg.CoveBin, runArgs...)
	runCmd.Stdout = cfg.Stdout
	runCmd.Stderr = cfg.Stderr
	runCmd.Env = cfg.Environ
	if err := runCmd.Start(); err != nil {
		return result{}, fmt.Errorf("start cove run: %w", err)
	}

	runDone := make(chan error, 1)
	go func() {
		runDone <- runCmd.Wait()
	}()
	cleaned := false
	defer func() {
		status := "ok"
		if err != nil {
			status = err.Error()
		}
		emitActionMetric(res.MetricsPath, "action_complete", actionStarted, status, map[string]any{"exit_code": res.Code})
		if !cleaned {
			cleanup(cfg, runCmd, runDone)
			if keepForCache && !cfg.Keep {
				deleteVM(cfg)
			}
		}
	}()

	res.MetricsPath = waitForMetricsPath(ctx, cfg, actionStarted)
	emitActionMetric(res.MetricsPath, "action_start", actionStarted, "ok", nil)
	if cacheEvictBytes > 0 {
		extra := map[string]any{
			"cache_key":    cacheKey,
			"cache_image":  cacheRef,
			"bytes_freed":  cacheEvictBytes,
			"cache_reason": cacheEvictReason,
		}
		emitActionMetric(res.MetricsPath, "run_cache_evict", actionStarted, "ok", extra)
	}
	if cacheKey != "" {
		emitActionMetric(res.MetricsPath, "cache_lookup", actionStarted, "ok", map[string]any{
			"cache_key":   cacheKey,
			"cache_image": cacheRef,
			"hit":         res.CacheHit,
			"cache_paths": parseCachePaths(cfg.CachePaths),
		})
	}

	if err := waitReady(ctx, cfg); err != nil {
		res.Code = 1
		return res, err
	}
	code, err := execGuestCommand(ctx, cfg)
	res.Code = code
	status := "ok"
	if err != nil {
		status = err.Error()
	}
	emitActionMetric(res.MetricsPath, "command_complete", actionStarted, status, map[string]any{"exit_code": code})
	if err != nil {
		return res, err
	}
	if keepForCache && res.Code == 0 {
		cleanup(cfg, runCmd, runDone)
		cleaned = true
		if err := saveCacheImage(ctx, cfg, cacheKey, cacheRef, actionStarted); err != nil {
			if !cfg.Keep {
				deleteVM(cfg)
			}
			if duplicateCacheSave(err) {
				fmt.Fprintf(cfg.Stderr, "cove-action: cache image %s already exists; another writer won\n", cacheRef)
				return res, nil
			}
			return res, err
		}
		res.CacheSaved = true
		if !cfg.Keep {
			deleteVM(cfg)
		}
	}
	return res, nil
}

func cacheImageRef(key string) string {
	key = strings.TrimSpace(key)
	component := regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(key, "-")
	component = strings.Trim(component, "-.")
	if component == "" || len(component) > 80 {
		sum := sha256.Sum256([]byte(key))
		component = hex.EncodeToString(sum[:])[:32]
	}
	return "cache/" + component + ":latest"
}

func cacheImageExists(cfg config, ref string) bool {
	path, ok := localImagePath(cfg, ref)
	if !ok {
		return false
	}
	_, err := os.Stat(filepath.Join(path, "manifest.json"))
	return err == nil
}

func cacheImageRestoreState(cfg config, ref string) (hit bool, evict bool, bytesFreed int64, reason string) {
	path, ok := localImagePath(cfg, ref)
	if !ok {
		return false, false, 0, ""
	}
	data, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		return false, false, 0, ""
	}
	var manifest struct {
		CreatedAt time.Time `json:"createdAt"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return false, false, 0, ""
	}
	ttl := cacheImageTTLMarker(path)
	if ttl <= 0 {
		ttl = cacheImageDefaultTTL
	}
	age := time.Since(manifest.CreatedAt)
	if age < ttl {
		return true, false, 0, ""
	}
	return false, true, cacheImageSize(cfg, ref), fmt.Sprintf("expired after %s (ttl %s)", age.Round(time.Second), ttl)
}

func cacheImageTTLMarker(path string) time.Duration {
	data, err := os.ReadFile(filepath.Join(path, "CACHE-TTL"))
	if err != nil {
		return cacheImageDefaultTTL
	}
	ttl, err := time.ParseDuration(strings.TrimSpace(string(data)))
	if err != nil || ttl <= 0 {
		return cacheImageDefaultTTL
	}
	return ttl
}

func localImagePath(cfg config, ref string) (string, bool) {
	name, tag, ok := strings.Cut(ref, ":")
	if !ok || name == "" || tag == "" {
		return "", false
	}
	home := envValue(cfg.Environ, "HOME", "")
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", false
		}
	}
	parts := append([]string{home, ".vz", "images"}, strings.Split(name, "/")...)
	parts = append(parts, tag)
	return filepath.Join(parts...), true
}

func saveCacheImage(ctx context.Context, cfg config, key, ref string, started time.Time) error {
	cmd := execCommandContext(ctx, cfg.CoveBin, "image", "build", "-from", cfg.VMName, "-tag", ref)
	cmd.Env = cfg.Environ
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		_, _ = cfg.Stdout.Write(out)
	}
	if err != nil {
		emitActionMetric(cfgMetricsPath(cfg, started), "cache_save", started, err.Error(), map[string]any{
			"cache_key":   key,
			"cache_image": ref,
		})
		return fmt.Errorf("save cache image: %w: %s", err, strings.TrimSpace(string(out)))
	}
	size := cacheImageSize(cfg, ref)
	writeCacheTTLMarker(cfg, ref)
	emitActionMetric(cfgMetricsPath(cfg, started), "cache_save", started, "ok", map[string]any{
		"cache_key":   key,
		"cache_image": ref,
		"image_size":  size,
	})
	return nil
}

func cfgMetricsPath(cfg config, since time.Time) string {
	return findMetricsPath(cfg, since)
}

func cacheImageSize(cfg config, ref string) int64 {
	path, ok := localImagePath(cfg, ref)
	if !ok {
		return 0
	}
	var total int64
	filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func writeCacheTTLMarker(cfg config, ref string) {
	path, ok := localImagePath(cfg, ref)
	if !ok {
		return
	}
	_ = os.WriteFile(filepath.Join(path, "CACHE-TTL"), []byte("168h\n"), 0o644)
}

func duplicateCacheSave(err error) bool {
	return strings.Contains(err.Error(), "already exists")
}

func deleteVM(cfg config) {
	cmd := execCommandContext(context.Background(), cfg.CoveBin, "vm", "delete", cfg.VMName)
	cmd.Stdin = strings.NewReader("y\n")
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	cmd.Env = cfg.Environ
	_ = cmd.Run()
}

func parseCachePaths(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func waitReady(ctx context.Context, cfg config) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for guest readiness: %w", ctx.Err())
		default:
		}

		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := runShell(probeCtx, cfg, "true")
		cancel()
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for guest readiness: %w", ctx.Err())
		case <-time.After(cfg.ReadyEvery):
		}
	}
}

func execGuestCommand(ctx context.Context, cfg config) (int, error) {
	command := cfg.Command
	if strings.TrimSpace(cfg.Script) != "" {
		command = cfg.Script
	}
	if len(cfg.Env) > 0 {
		var b strings.Builder
		b.WriteString("env")
		for _, kv := range cfg.Env {
			b.WriteByte(' ')
			b.WriteString(shellQuote(kv))
		}
		b.WriteString(" /bin/sh -lc ")
		b.WriteString(shellQuote(command))
		command = b.String()
	}
	err := runShell(ctx, cfg, command)
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return 124, fmt.Errorf("guest command timed out: %w", ctx.Err())
	}
	return 1, fmt.Errorf("guest command: %w", err)
}

func runShell(ctx context.Context, cfg config, command string) error {
	cmd := execCommandContext(ctx, cfg.CoveBin, "shell", cfg.VMName, "--", "/bin/sh", "-lc", command)
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	cmd.Env = cfg.Environ
	return cmd.Run()
}

func cleanup(cfg config, runCmd *exec.Cmd, runDone <-chan error) {
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop := execCommandContext(stopCtx, cfg.CoveBin, "ctl", "-vm", cfg.VMName, "stop")
	stop.Stdout = cfg.Stdout
	stop.Stderr = cfg.Stderr
	stop.Env = cfg.Environ
	_ = stop.Run()

	select {
	case <-runDone:
		return
	case <-time.After(cleanupWait):
	}
	if runCmd.Process != nil {
		_ = runCmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-runDone:
	case <-time.After(cleanupWait):
		if runCmd.Process != nil {
			_ = runCmd.Process.Kill()
		}
		<-runDone
	}
}

func writeOutputs(cfg config, res result) error {
	path := envValue(cfg.Environ, "GITHUB_OUTPUT", "")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "vm-name=%s\nexit-code=%d\nlog-path=%s\nmetrics-path=%s\ncache-hit=%t\ncache-image=%s\ncache-saved=%t\n",
		cfg.VMName, res.Code, defaultLogPath(cfg.Environ), res.MetricsPath, res.CacheHit, res.CacheImage, res.CacheSaved)
	return err
}

func waitForMetricsPath(ctx context.Context, cfg config, since time.Time) string {
	deadline := time.Now().Add(30 * time.Second)
	for {
		if path := findMetricsPath(cfg, since); path != "" {
			return path
		}
		if time.Now().After(deadline) {
			return ""
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func findMetricsPath(cfg config, since time.Time) string {
	runsRoot := defaultLogPath(cfg.Environ)
	entries, err := os.ReadDir(runsRoot)
	if err != nil {
		return ""
	}
	var bestPath string
	var bestMod time.Time
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(runsRoot, entry.Name(), "metrics.jsonl")
		info, err := os.Stat(path)
		if err != nil || info.ModTime().Before(since.Add(-time.Second)) {
			continue
		}
		if !metricsFileMatches(path, cfg) {
			continue
		}
		if bestPath == "" || info.ModTime().After(bestMod) {
			bestPath = path
			bestMod = info.ModTime()
		}
	}
	return bestPath
}

func metricsFileMatches(path string, cfg config) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var event runmetrics.Event
		if err := json.Unmarshal(scan.Bytes(), &event); err != nil {
			continue
		}
		if event.VMName == cfg.VMName || event.ImageRef == cfg.Image {
			return true
		}
	}
	return false
}

func emitActionMetric(path, eventType string, started time.Time, status string, extra map[string]any) {
	if path == "" {
		return
	}
	runID := filepath.Base(filepath.Dir(path))
	if runID == "." || runID == string(filepath.Separator) {
		runID = ""
	}
	if runID != "" {
		if extra == nil {
			extra = map[string]any{}
		} else {
			extra = copyActionMetricExtra(extra)
		}
		extra["run_id"] = runID
	}
	sink, err := runmetrics.NewJSONLSink(path)
	if err != nil {
		return
	}
	defer sink.Close()
	durationMS := int64(0)
	if !started.IsZero() {
		durationMS = time.Since(started).Milliseconds()
	}
	_ = sink.Emit(context.Background(), runmetrics.Event{
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		EventType:  eventType,
		DurationMS: durationMS,
		Status:     status,
		Extra:      extra,
	})
}

func copyActionMetricExtra(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func parseEnvBlock(s string) ([]string, error) {
	var env []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			return nil, fmt.Errorf("invalid env entry %q", line)
		}
		env = append(env, line)
	}
	return env, nil
}

func envValue(environ []string, key, fallback string) string {
	prefix := key + "="
	for _, kv := range environ {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return fallback
}

func parseBool(s string) bool {
	ok, err := strconv.ParseBool(strings.TrimSpace(s))
	return err == nil && ok
}

func defaultVMName(environ []string) string {
	runID := envValue(environ, "GITHUB_RUN_ID", "local")
	attempt := envValue(environ, "GITHUB_RUN_ATTEMPT", "1")
	name := "cove-action-" + runID + "-" + attempt
	name = regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(name, "-")
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.Trim(name, "-.")
}

func defaultLogPath(environ []string) string {
	home := envValue(environ, "HOME", "")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".vz", "runs")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
