package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/agent"
	"github.com/tmc/vz-macos/internal/builddigest"
	"github.com/tmc/vz-macos/internal/buildpaths"
	"github.com/tmc/vz-macos/internal/ociimage"
	"golang.org/x/tools/txtar"
)

const agentProtocolVersion = agent.ProtocolVersion

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
	SecretFrom []buildSecretRef
	Compact    string
	HasMount   bool
	MountValue string
}

type buildSecretRef struct {
	Name string
	URI  string
	Line int
}

type buildCacheKeyInput struct {
	ParentDigest         string            `json:"parent_digest"`
	ScriptDigest         string            `json:"script_digest"`
	AgentProtocolVersion string            `json:"agent_protocol_version"`
	CacheEnv             map[string]string `json:"cache_env,omitempty"`
	CacheURL             map[string]string `json:"cache_url,omitempty"`
	CacheFile            map[string]string `json:"cache_file,omitempty"`
	Secrets              []string          `json:"secrets,omitempty"`
	SecretFrom           []string          `json:"secret_from,omitempty"`
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
	for i, raw := range s {
		lineNo := i + 1
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
		case "secret-from":
			refs, err := parseBuildSecretFrom(value, lineNo)
			if err != nil {
				return meta, err
			}
			meta.SecretFrom = append(meta.SecretFrom, refs...)
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
	meta.SecretFrom = sortedBuildSecretRefs(meta.SecretFrom)
	if meta.Compact == "" {
		meta.Compact = "targeted"
	}
	return meta, nil
}

func parseBuildSecretFrom(value string, line int) ([]buildSecretRef, error) {
	var refs []buildSecretRef
	for _, f := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if f == "" {
			continue
		}
		name, uri, ok := strings.Cut(f, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: secret-from: missing '=' in %q", line, f)
		}
		name = strings.TrimSpace(name)
		uri = strings.TrimSpace(uri)
		if !validBuildSecretName(name) {
			return nil, fmt.Errorf("line %d: secret-from: invalid secret name %q", line, name)
		}
		if uri == "" {
			return nil, fmt.Errorf("line %d: secret-from: empty URI for %s", line, name)
		}
		u, err := url.Parse(uri)
		if err != nil {
			return nil, fmt.Errorf("line %d: secret-from: secret URI %q: %w", line, uri, err)
		}
		if u.Scheme == "" {
			return nil, fmt.Errorf("line %d: secret-from: secret URI %q: missing scheme", line, uri)
		}
		refs = append(refs, buildSecretRef{Name: name, URI: uri, Line: line})
	}
	return refs, nil
}

func sortedBuildSecretRefs(in []buildSecretRef) []buildSecretRef {
	if len(in) == 0 {
		return nil
	}
	out := append([]buildSecretRef(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].URI != out[j].URI {
			return out[i].URI < out[j].URI
		}
		return out[i].Line < out[j].Line
	})
	return out
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
		SecretFrom:           buildSecretRefStrings(step.Meta.SecretFrom),
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
	return builddigest.URL(ctx, client, rawurl)
}

func hashFile(path string) (string, error) {
	return builddigest.File(path)
}

func digestBytes(data []byte) string {
	return builddigest.Bytes(data)
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
	writeList(&b, "secret-from", in.SecretFrom)
	writeKV(&b, "compact", in.Compact)
	return digestBytes([]byte(b.String()))
}

func writeKV(b *strings.Builder, key, value string) {
	builddigest.WriteKV(b, key, value)
}

func writeMap(b *strings.Builder, key string, m map[string]string) {
	builddigest.WriteMap(b, key, m)
}

func writeList(b *strings.Builder, key string, list []string) {
	builddigest.WriteList(b, key, list)
}

func buildSecretRefStrings(refs []buildSecretRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, ref.Name+"="+ref.URI)
	}
	sort.Strings(out)
	return out
}

func expandHome(path string) string {
	return buildpaths.ExpandHome(path)
}

func localBuildBaseDir(refText string) (string, bool) {
	return buildpaths.LocalBaseDir(refText)
}

func resolveBuildBaseDigest(ctx context.Context, refText string) (ociimage.Reference, string, error) {
	if path, ok := localBuildBaseDir(refText); ok {
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
	required := map[string]bool{filepath.Base(disk): true}
	for _, spec := range cloneRequiredFiles(sourceOS) {
		if spec.required {
			required[spec.name] = true
		}
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
			if errors.Is(err, os.ErrNotExist) {
				if required[name] {
					return "", fmt.Errorf("local build base %s: %w", name, err)
				}
				continue
			}
			return "", fmt.Errorf("local build base %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			if required[name] {
				return "", fmt.Errorf("local build base %s is not a regular file", name)
			}
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
