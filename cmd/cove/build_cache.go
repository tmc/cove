package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tmc/cove/internal/agent"
	"github.com/tmc/cove/internal/builddigest"
	"github.com/tmc/cove/internal/buildmeta"
	"github.com/tmc/cove/internal/buildpaths"
	"github.com/tmc/cove/internal/ociimage"
)

const agentProtocolVersion = agent.ProtocolVersion

type buildStep struct {
	Name   string
	Source string
	Data   []byte
	Meta   buildScriptMeta
}

type buildScriptMeta = buildmeta.ScriptMeta

type buildSecretRef = buildmeta.SecretRef

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
	return buildmeta.ParseScript(data)
}

func parseBuildSecretFrom(value string, line int) ([]buildSecretRef, error) {
	return buildmeta.ParseSecretFrom(value, line)
}

func sortedBuildSecretRefs(in []buildSecretRef) []buildSecretRef {
	return buildmeta.SortedSecretRefs(in)
}

func uniqueSorted(in []string) []string {
	return buildmeta.UniqueSorted(in)
}

func parseBuildDuration(s string) (time.Duration, error) {
	return buildmeta.ParseDuration(s)
}

func validateCompactMode(mode string) error {
	return buildmeta.ValidateCompactMode(mode)
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
