package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/ociimage"
	"golang.org/x/tools/txtar"
)

const agentProtocolVersion = "1"

type buildStep struct {
	Name   string
	Source string
	Data   []byte
	Meta   buildScriptMeta
}

type buildScriptMeta struct {
	CacheEnv   []string
	CacheURL   []string
	CacheFile  []string
	CacheTTL   time.Duration
	Secrets    []string
	Compact    string
	HasMount   bool
	MountValue string
}

type buildCacheKeyInput struct {
	ParentDigest         string            `json:"parent_digest"`
	ScriptDigest         string            `json:"script_digest"`
	AgentProtocolVersion string            `json:"agent_protocol_version"`
	CacheEnv             map[string]string `json:"cache_env,omitempty"`
	CacheURL             map[string]string `json:"cache_url,omitempty"`
	CacheFile            map[string]string `json:"cache_file,omitempty"`
	Secrets              []string          `json:"secrets,omitempty"`
	Compact              string            `json:"compact"`
}

type buildPlanStep struct {
	Name                 string
	Source               string
	Data                 []byte
	Key                  string
	ParentDigest         string
	ScriptDigest         string
	AgentProtocolVersion string
	LayerDigest          string
	CacheHit             bool
	Meta                 buildScriptMeta
}

type buildPlan struct {
	Name         string
	Base         string
	ParentDigest string
	Tags         []string
	Steps        []buildPlanStep
}

func loadBuildScript(name string) (buildStep, error) {
	data, err := loadVZScriptData(name)
	if err != nil {
		return buildStep{}, err
	}
	meta, err := parseBuildScriptMeta(data)
	if err != nil {
		return buildStep{}, fmt.Errorf("parse %s: %w", name, err)
	}
	return buildStep{Name: scriptDisplayName(name), Source: name, Data: data, Meta: meta}, nil
}

func scriptDisplayName(name string) string {
	base := filepath.Base(name)
	return strings.TrimSuffix(base, ".vzscript")
}

func parseBuildScriptMeta(data []byte) (buildScriptMeta, error) {
	var meta buildScriptMeta
	ar := txtar.Parse(data)
	s := bytes.Split(ar.Comment, []byte("\n"))
	for _, raw := range s {
		line := strings.TrimSpace(string(raw))
		if line == "" || line == "#" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			break
		}
		text := strings.TrimSpace(strings.TrimPrefix(line, "#"))
		key, value, ok := strings.Cut(text, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "cache-env":
			meta.CacheEnv = appendFields(meta.CacheEnv, value)
		case "cache-url":
			meta.CacheURL = appendFields(meta.CacheURL, value)
		case "cache-file":
			meta.CacheFile = appendFields(meta.CacheFile, value)
		case "cache-ttl":
			d, err := parseBuildDuration(value)
			if err != nil {
				return meta, err
			}
			meta.CacheTTL = d
		case "secret":
			meta.Secrets = appendFields(meta.Secrets, value)
		case "compact":
			compact := strings.ToLower(strings.TrimSpace(value))
			if err := validateCompactMode(compact); err != nil {
				return meta, err
			}
			meta.Compact = compact
		case "mount":
			meta.HasMount = true
			meta.MountValue = value
		}
	}
	meta.CacheEnv = uniqueSorted(meta.CacheEnv)
	meta.CacheURL = uniqueSorted(meta.CacheURL)
	meta.CacheFile = uniqueSorted(meta.CacheFile)
	meta.Secrets = uniqueSorted(meta.Secrets)
	if meta.Compact == "" {
		meta.Compact = "targeted"
	}
	return meta, nil
}

func appendFields(dst []string, value string) []string {
	for _, f := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if f != "" {
			dst = append(dst, f)
		}
	}
	return dst
}

func uniqueSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	m := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || m[s] {
			continue
		}
		m[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func parseBuildDuration(s string) (time.Duration, error) {
	if s == "" {
		return 0, fmt.Errorf("cache-ttl: empty duration")
	}
	if strings.HasSuffix(s, "d") {
		n, err := parsePositiveInt(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("cache-ttl: %w", err)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("cache-ttl: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("cache-ttl: duration must be positive")
	}
	return d, nil
}

func parsePositiveInt(s string) (int64, error) {
	var n int64
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid number %q", s)
		}
		n = n*10 + int64(r-'0')
	}
	if n <= 0 {
		return 0, fmt.Errorf("number must be positive")
	}
	return n, nil
}

func validateCompactMode(mode string) error {
	switch mode {
	case "fast", "targeted", "thorough":
		return nil
	case "":
		return fmt.Errorf("compact: empty mode")
	default:
		return fmt.Errorf("compact: invalid mode %q", mode)
	}
}

func buildCacheKey(ctx context.Context, parentDigest string, step buildStep, client *http.Client) (string, buildCacheKeyInput, error) {
	if parentDigest == "" {
		return "", buildCacheKeyInput{}, fmt.Errorf("build cache key: empty parent digest")
	}
	if step.Meta.HasMount {
		return "", buildCacheKeyInput{}, fmt.Errorf("vzscript step %q declares `# mount: %s` -- not allowed in `cove build` context; use `# cache-file:`, `# inject:`, or `# cache-url:`", step.Name, step.Meta.MountValue)
	}
	in := buildCacheKeyInput{
		ParentDigest:         parentDigest,
		ScriptDigest:         digestBytes(step.Data),
		AgentProtocolVersion: agentProtocolVersion,
		CacheEnv:             map[string]string{},
		CacheURL:             map[string]string{},
		CacheFile:            map[string]string{},
		Secrets:              append([]string(nil), step.Meta.Secrets...),
		Compact:              step.Meta.Compact,
	}
	for _, name := range step.Meta.CacheEnv {
		in.CacheEnv[name] = os.Getenv(name)
	}
	for _, rawurl := range step.Meta.CacheURL {
		digest, err := hashURL(ctx, client, rawurl)
		if err != nil {
			return "", in, err
		}
		in.CacheURL[rawurl] = digest
	}
	for _, path := range step.Meta.CacheFile {
		digest, err := hashFile(expandHome(path))
		if err != nil {
			return "", in, err
		}
		in.CacheFile[path] = digest
	}
	key := digestCanonical(in)
	return key, in, nil
}

func hashURL(ctx context.Context, client *http.Client, rawurl string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return "", fmt.Errorf("cache-url %s: %w", rawurl, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("cache-url %s: %w", rawurl, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cache-url %s: returned %s", rawurl, resp.Status)
	}
	return digestReader(resp.Body)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cache-file %s: %w", path, err)
	}
	defer f.Close()
	return digestReader(f)
}

func digestReader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestCanonical(in buildCacheKeyInput) string {
	var b strings.Builder
	writeKV(&b, "parent", in.ParentDigest)
	writeKV(&b, "script", in.ScriptDigest)
	writeKV(&b, "agent", in.AgentProtocolVersion)
	writeMap(&b, "env", in.CacheEnv)
	writeMap(&b, "url", in.CacheURL)
	writeMap(&b, "file", in.CacheFile)
	writeList(&b, "secret", in.Secrets)
	writeKV(&b, "compact", in.Compact)
	return digestBytes([]byte(b.String()))
}

func writeKV(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(value)
	b.WriteByte('\n')
}

func writeMap(b *strings.Builder, key string, m map[string]string) {
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeKV(b, key+":"+k, m[k])
	}
}

func writeList(b *strings.Builder, key string, list []string) {
	for _, v := range list {
		writeKV(b, key, v)
	}
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func resolveBuildBaseDigest(ctx context.Context, refText string) (ociimage.Reference, string, error) {
	path := expandHome(refText)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		digest, err := digestLocalBuildBase(path)
		if err != nil {
			return ociimage.Reference{}, "", err
		}
		return ociimage.Reference{}, digest, nil
	}
	ref, err := ociimage.ParseReference(refText)
	if err != nil {
		return ref, "", err
	}
	if ref.Digest != "" {
		return ref, ref.Digest, nil
	}
	client := ociimage.RegistryClient{TokenCache: ociimage.NewRegistryTokenCache()}
	_, digest, err := client.FetchManifest(ctx, ref)
	if err != nil {
		return ref, "", err
	}
	return ref, digest, nil
}

func digestLocalBuildBase(dir string) (string, error) {
	disk, err := pushDiskPath(dir)
	if err != nil {
		return "", fmt.Errorf("local build base: %w", err)
	}
	sourceOS := "macOS"
	if filepath.Base(disk) == "linux-disk.img" {
		sourceOS = "Linux"
	}
	names := []string{filepath.Base(disk)}
	for _, spec := range cloneRequiredFiles(sourceOS) {
		if spec.name != filepath.Base(disk) {
			names = append(names, spec.name)
		}
	}
	names = append(names, cloneOptionalFiles(sourceOS)...)
	names = uniqueSorted(names)

	var b strings.Builder
	writeKV(&b, "type", "local-vm")
	writeKV(&b, "os", sourceOS)
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("local build base %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		digest, err := hashFile(path)
		if err != nil {
			return "", fmt.Errorf("local build base %s: %w", name, err)
		}
		writeKV(&b, "file", name)
		writeKV(&b, "digest", digest)
	}
	return digestBytes([]byte(b.String())), nil
}
