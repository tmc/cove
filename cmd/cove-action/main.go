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

	runmetrics "github.com/tmc/cove/internal/metrics"
)

var (
	execCommandContext = exec.CommandContext
	cleanupWait        = 5 * time.Second
)

var errDuplicateCacheSave = errors.New("duplicate cache image")

const cacheImageDefaultTTL = 7 * 24 * time.Hour

// Cache modes mirror GitHub actions/cache: restore-save (default) does both,
// restore-only skips save, save-only skips restore, off disables cache entirely.
const (
	cacheModeRestoreSave = "restore-save"
	cacheModeRestoreOnly = "restore-only"
	cacheModeSaveOnly    = "save-only"
	cacheModeOff         = "off"
)

const (
	guestGitHubAnnotationsPath = "/tmp/cove-github-annotations.log"
	githubAnnotationsFile      = "github-annotations.log"
)

func validateCacheMode(m string) error {
	switch m {
	case cacheModeRestoreSave, cacheModeRestoreOnly, cacheModeSaveOnly, cacheModeOff:
		return nil
	}
	return fmt.Errorf("invalid cache-mode %q (want restore-save, restore-only, save-only, off)", m)
}

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
	CacheMode  string
	CacheScope string
	Artifacts  []string
	Env        []string
	Secrets    []string
	Stdout     io.Writer
	Stderr     io.Writer
	Environ    []string
}

type result struct {
	Code                  int
	MetricsPath           string
	CacheHit              bool
	CacheImage            string
	CacheSaved            bool
	ArtifactDir           string
	GitHubAnnotationsPath string
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
		if res.Code != 0 || res.MetricsPath != "" || res.ArtifactDir != "" || res.CacheImage != "" {
			_ = writeOutputs(cfg, res)
		}
		if res.Code != 0 {
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
	secretsText := envValue(environ, "COVE_ACTION_SECRETS", "")
	artifactsText := envValue(environ, "COVE_ACTION_ARTIFACTS", "")
	cfg.CacheKey = envValue(environ, "COVE_ACTION_CACHE_KEY", "")
	cfg.CachePaths = envValue(environ, "COVE_ACTION_CACHE_PATHS", "")
	cfg.CacheMode = envValue(environ, "COVE_ACTION_CACHE_MODE", cacheModeRestoreSave)
	cfg.CacheScope = envValue(environ, "COVE_ACTION_CACHE_SCOPE", "")

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
	fs.StringVar(&secretsText, "secrets", secretsText, "newline-separated KEY=value|env://VAR|file:///path guest secrets")
	fs.StringVar(&artifactsText, "artifacts", artifactsText, "newline-separated absolute guest artifact paths to copy into the run bundle")
	fs.StringVar(&cfg.CacheKey, "cache-key", cfg.CacheKey, "whole-VM cache key")
	fs.StringVar(&cfg.CachePaths, "cache-paths", cfg.CachePaths, "newline-separated guest paths preserved by the whole-VM cache")
	fs.StringVar(&cfg.CacheMode, "cache-mode", cfg.CacheMode, "cache behavior: restore-save, restore-only, save-only, off")
	fs.StringVar(&cfg.CacheScope, "cache-scope", cfg.CacheScope, "namespace prefix joined to cache-key as <scope>:<key>")
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
	cfg.Secrets, err = parseSecretsBlock(secretsText)
	if err != nil {
		return cfg, err
	}
	cfg.Artifacts, err = parseArtifactPaths(artifactsText)
	if err != nil {
		return cfg, err
	}
	cfg.CacheMode = strings.TrimSpace(cfg.CacheMode)
	if cfg.CacheMode == "" {
		cfg.CacheMode = cacheModeRestoreSave
	}
	if err := validateCacheMode(cfg.CacheMode); err != nil {
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
	if cfg.CacheMode == cacheModeOff {
		cacheKey = ""
	}
	cacheRestore := cfg.CacheMode == cacheModeRestoreSave || cfg.CacheMode == cacheModeRestoreOnly
	cacheSave := cfg.CacheMode == cacheModeRestoreSave || cfg.CacheMode == cacheModeSaveOnly
	var cacheEvictBytes int64
	var cacheEvictReason string
	if cacheKey != "" {
		cacheRef = cacheImageRef(scopedCacheKey(cfg.CacheScope, cacheKey))
		res.CacheImage = cacheRef
		if hit, evict, bytesFreed, reason := cacheImageRestoreState(cfg, cacheRef); cacheRestore && (hit || evict) {
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
	keepForCache := cacheKey != "" && cacheSave && !res.CacheHit
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
		forwardGitHubAnnotations(cfg.Stdout, res.GitHubAnnotationsPath)
	}()

	res.MetricsPath = waitForMetricsPath(ctx, cfg, actionStarted)
	res.ArtifactDir = artifactDirForMetrics(res.MetricsPath)
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
	res.GitHubAnnotationsPath = copyGitHubAnnotations(ctx, cfg, res.MetricsPath, actionStarted)
	if err != nil {
		return res, err
	}
	if len(cfg.Artifacts) > 0 {
		if err := copyArtifacts(ctx, cfg, res.MetricsPath, actionStarted); err != nil {
			return res, err
		}
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

// scopedCacheKey joins scope and key as "<scope>:<key>". Empty scope is a
// pass-through, preserving the historical per-repo cache layout.
func scopedCacheKey(scope, key string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return key
	}
	return scope + ":" + key
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
		if cacheImageExists(cfg, ref) {
			return fmt.Errorf("save cache image: %w", errDuplicateCacheSave)
		}
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
	return errors.Is(err, errDuplicateCacheSave)
}

func cacheImageExists(cfg config, ref string) bool {
	path, ok := localImagePath(cfg, ref)
	if !ok {
		return false
	}
	info, err := os.Stat(filepath.Join(path, "manifest.json"))
	return err == nil && !info.IsDir()
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

func parseArtifactPaths(s string) ([]string, error) {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		clean, err := cleanArtifactPath(line)
		if err != nil {
			return nil, err
		}
		out = append(out, clean)
	}
	return out, nil
}

func waitReady(ctx context.Context, cfg config) error {
	ticker := time.NewTicker(cfg.ReadyEvery)
	defer ticker.Stop()
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
		case <-ticker.C:
		}
	}
}

func execGuestCommand(ctx context.Context, cfg config) (int, error) {
	command := cfg.Command
	if strings.TrimSpace(cfg.Script) != "" {
		command = cfg.Script
	}
	env := actionGuestEnv(cfg.Env)
	if len(env) > 0 {
		var b strings.Builder
		b.WriteString("env")
		for _, kv := range env {
			b.WriteByte(' ')
			b.WriteString(shellQuote(kv))
		}
		b.WriteString(" /bin/sh -lc ")
		b.WriteString(shellQuote(command))
		command = b.String()
	}
	err := runShellWithSecrets(ctx, cfg, command, cfg.Secrets)
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
	return runShellWithSecrets(ctx, cfg, command, nil)
}

func runShellWithSecrets(ctx context.Context, cfg config, command string, secrets []string) error {
	args := []string{"shell"}
	for _, s := range secrets {
		args = append(args, "--secret-env", s)
	}
	args = append(args, cfg.VMName, "--", "/bin/sh", "-lc", command)
	cmd := execCommandContext(ctx, cfg.CoveBin, args...)
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
	_, err = fmt.Fprintf(f, "vm-name=%s\nexit-code=%d\nlog-path=%s\nmetrics-path=%s\nartifact-path=%s\ncache-hit=%t\ncache-image=%s\ncache-saved=%t\n",
		cfg.VMName, res.Code, defaultLogPath(cfg.Environ), res.MetricsPath, res.ArtifactDir, res.CacheHit, res.CacheImage, res.CacheSaved)
	return err
}

func artifactDirForMetrics(metricsPath string) string {
	if metricsPath == "" {
		return ""
	}
	return filepath.Dir(metricsPath)
}

func copyArtifacts(ctx context.Context, cfg config, metricsPath string, started time.Time) error {
	if metricsPath == "" {
		return errors.New("copy artifacts: metrics path not found")
	}
	runDir := filepath.Dir(metricsPath)
	for _, guestPath := range cfg.Artifacts {
		hostPath, err := artifactHostPath(runDir, guestPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(hostPath), 0755); err != nil {
			return fmt.Errorf("copy artifact: prepare host path: %w", err)
		}
		src := cfg.VMName + ":" + guestPath
		cmd := execCommandContext(ctx, cfg.CoveBin, "cp", src, hostPath)
		cmd.Stdout = cfg.Stdout
		cmd.Stderr = cfg.Stderr
		cmd.Env = cfg.Environ
		if err := cmd.Run(); err != nil {
			emitActionMetric(metricsPath, "artifact_copy", started, err.Error(), map[string]any{
				"guest_path": guestPath,
				"host_path":  hostPath,
			})
			return fmt.Errorf("copy artifact %s: %w", guestPath, err)
		}
		extra := map[string]any{
			"guest_path": guestPath,
			"host_path":  hostPath,
		}
		if bytes, ok := artifactBytes(hostPath); ok {
			extra["bytes"] = bytes
		}
		emitActionMetric(metricsPath, "artifact_copy", started, "ok", extra)
	}
	return nil
}

func artifactHostPath(runDir, guestPath string) (string, error) {
	clean, err := cleanArtifactPath(guestPath)
	if err != nil {
		return "", err
	}
	rel := strings.TrimPrefix(clean, string(filepath.Separator))
	return filepath.Join(runDir, "guest", rel), nil
}

func cleanArtifactPath(guestPath string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(guestPath))
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("artifact path %q must be absolute", guestPath)
	}
	rel := strings.TrimPrefix(clean, string(filepath.Separator))
	if rel == "" || rel == "." {
		return "", fmt.Errorf("artifact path %q does not name a file or directory", guestPath)
	}
	return clean, nil
}

func artifactBytes(path string) (int64, bool) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err == nil
}

func actionGuestEnv(env []string) []string {
	out := append([]string{}, env...)
	out = append(out,
		"COVE_GITHUB_ANNOTATIONS="+guestGitHubAnnotationsPath,
		"GITHUB_ANNOTATION_FILE="+guestGitHubAnnotationsPath,
	)
	return out
}

func copyGitHubAnnotations(ctx context.Context, cfg config, metricsPath string, started time.Time) string {
	if metricsPath == "" {
		return ""
	}
	hostPath := filepath.Join(filepath.Dir(metricsPath), githubAnnotationsFile)
	cmd := execCommandContext(ctx, cfg.CoveBin, "cp", cfg.VMName+":"+guestGitHubAnnotationsPath, hostPath)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Env = cfg.Environ
	if err := cmd.Run(); err != nil {
		return ""
	}
	count := countGitHubAnnotationLines(hostPath)
	emitActionMetric(metricsPath, "github_annotation_forward", started, "ok", map[string]any{
		"guest_path": guestGitHubAnnotationsPath,
		"host_path":  hostPath,
		"count":      count,
	})
	return hostPath
}

func countGitHubAnnotationLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	count := 0
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		if _, ok := formatGitHubAnnotationLine(scan.Text()); ok {
			count++
		}
	}
	return count
}

func forwardGitHubAnnotations(w io.Writer, path string) {
	if w == nil || path == "" {
		return
	}
	_, _ = forwardGitHubAnnotationFileOnce(w, path)
}

func forwardGitHubAnnotationFileOnce(w io.Writer, path string) (found bool, forwarded bool) {
	f, err := os.Open(path)
	if err != nil {
		return false, false
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		if line, ok := formatGitHubAnnotationLine(scan.Text()); ok {
			fmt.Fprintln(w, line)
			forwarded = true
		}
	}
	return true, forwarded
}

type githubAnnotation struct {
	Level     string `json:"level"`
	Type      string `json:"type"`
	Message   string `json:"message"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	EndLine   int    `json:"end_line"`
	EndColumn int    `json:"end_column"`
	Title     string `json:"title"`
}

func formatGitHubAnnotationLine(line string) (string, bool) {
	line = strings.TrimSuffix(line, "\r")
	if githubAnnotationCommand(line) {
		return line, true
	}
	var ann githubAnnotation
	if err := json.Unmarshal([]byte(line), &ann); err != nil {
		return "", false
	}
	cmd := strings.ToLower(strings.TrimSpace(ann.Level))
	if cmd == "" {
		cmd = strings.ToLower(strings.TrimSpace(ann.Type))
	}
	if !githubAnnotationName(cmd) || ann.Message == "" {
		return "", false
	}
	props := githubAnnotationProperties(ann)
	if props == "" {
		return fmt.Sprintf("::%s::%s", cmd, escapeGitHubCommandData(ann.Message)), true
	}
	return fmt.Sprintf("::%s %s::%s", cmd, props, escapeGitHubCommandData(ann.Message)), true
}

func githubAnnotationCommand(line string) bool {
	if !strings.HasPrefix(line, "::") {
		return false
	}
	head, _, ok := strings.Cut(strings.TrimPrefix(line, "::"), "::")
	if !ok {
		return false
	}
	name, _, _ := strings.Cut(head, " ")
	return githubAnnotationName(name)
}

func githubAnnotationName(name string) bool {
	switch strings.TrimSpace(name) {
	case "error", "warning", "notice":
		return true
	default:
		return false
	}
}

func githubAnnotationProperties(ann githubAnnotation) string {
	var props []string
	add := func(name, value string) {
		if value != "" {
			props = append(props, name+"="+escapeGitHubCommandProperty(value))
		}
	}
	addInt := func(name string, value int) {
		if value > 0 {
			add(name, strconv.Itoa(value))
		}
	}
	add("file", ann.File)
	addInt("line", ann.Line)
	addInt("col", ann.Col)
	addInt("endLine", ann.EndLine)
	addInt("endColumn", ann.EndColumn)
	add("title", ann.Title)
	return strings.Join(props, ",")
}

func escapeGitHubCommandData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

func escapeGitHubCommandProperty(s string) string {
	s = escapeGitHubCommandData(s)
	s = strings.ReplaceAll(s, ":", "%3A")
	s = strings.ReplaceAll(s, ",", "%2C")
	return s
}

func waitForMetricsPath(ctx context.Context, cfg config, since time.Time) string {
	deadline := time.Now().Add(30 * time.Second)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
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
		case <-ticker.C:
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

// parseSecretsBlock parses a multi-line GHA `secrets:` input.
//
// Each non-blank, non-comment line is `KEY=value`, where value may be a
// literal, `env://VAR`, or `file:///path`. URI resolution and redaction
// happen in `cove shell --secret-env`; this function only validates shape
// and rejects duplicate keys. KEY must be a non-empty identifier.
func parseSecretsBlock(s string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("invalid secret entry %q: want KEY=value|env://VAR|file:///path", line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("invalid secret entry %q: empty key", line)
		}
		if seen[key] {
			return nil, fmt.Errorf("duplicate secret key %q", key)
		}
		seen[key] = true
		out = append(out, line)
	}
	return out, nil
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
