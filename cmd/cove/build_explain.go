package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/store"
)

type buildExplanation struct {
	Name         string                 `json:"name"`
	BaseDigest   string                 `json:"base_digest,omitempty"`
	BaseStatus   string                 `json:"base_status"`
	BasePlatform string                 `json:"base_platform"`
	BaseFormat   string                 `json:"base_format"`
	RemoteLookup string                 `json:"remote_lookup"`
	CacheImports string                 `json:"cache_imports"`
	Consistency  string                 `json:"consistency"`
	Steps        []buildStepExplanation `json:"steps"`
}

type buildStepExplanation struct {
	Name          string            `json:"name"`
	Key           string            `json:"key,omitempty"`
	ParentDigest  string            `json:"parent_digest,omitempty"`
	ScriptDigest  string            `json:"script_digest"`
	AgentProtocol string            `json:"agent_protocol"`
	Compact       string            `json:"compact"`
	Environment   []string          `json:"environment_names,omitempty"`
	DeclaredFiles []string          `json:"declared_files,omitempty"`
	Files         map[string]string `json:"file_digests,omitempty"`
	URLs          []string          `json:"unresolved_urls,omitempty"`
	SecretNames   []string          `json:"secret_names,omitempty"`
	TTL           string            `json:"ttl,omitempty"`
	CacheStatus   string            `json:"cache_status"`
	CacheReason   string            `json:"cache_reason"`
	LayerDigest   string            `json:"layer_digest,omitempty"`
}

func explainBuild(ctx context.Context, name string, opts buildOptions, s store.Store) (buildExplanation, error) {
	out := buildExplanation{Name: name, BasePlatform: "not inspected", BaseFormat: "not inspected", RemoteLookup: "not performed", CacheImports: "not performed; local entries only", Consistency: "declared inputs only; arbitrary guest shell network and time effects are not tracked"}
	parent := ""
	if _, local := localBuildBaseDir(opts.Base); local {
		_, digest, err := resolveBuildBaseDigestWithOptions(ctx, opts.Base, opts)
		if err != nil {
			return out, err
		}
		parent = digest
		out.BaseStatus = "local content digest resolved"
	} else {
		ref, err := ociimage.ParseReference(opts.Base)
		if err != nil {
			return out, err
		}
		parent = ref.Digest
		out.BaseStatus = "remote tag unresolved; no network lookup"
		if parent != "" {
			out.BaseStatus = "explicit digest; remote content and platform not verified"
		}
	}
	out.BaseDigest = parent
	for _, name := range opts.Scripts {
		step, err := loadBuildScript(name)
		if err != nil {
			return out, err
		}
		if step.Meta.Compact == "targeted" && opts.Compact != "targeted" {
			step.Meta.Compact = opts.Compact
		}
		if step.Meta.HasMount {
			return out, fmt.Errorf("explain step %s: mounts are not allowed in cove build", step.Name)
		}
		item := buildStepExplanation{Name: step.Name, ParentDigest: parent, ScriptDigest: digestBytes(step.Data), AgentProtocol: agentProtocolVersion, Compact: step.Meta.Compact, Environment: append([]string(nil), step.Meta.CacheEnv...), DeclaredFiles: append([]string(nil), step.Meta.CacheFile...), SecretNames: append([]string(nil), step.Meta.Secrets...)}
		for _, ref := range step.Meta.SecretFrom {
			item.SecretNames = append(item.SecretNames, ref.Name)
		}
		item.SecretNames = uniqueSorted(item.SecretNames)
		for _, raw := range step.Meta.CacheURL {
			item.URLs = append(item.URLs, safeBuildExplanationURL(raw))
		}
		if step.Meta.CacheTTL > 0 {
			item.TTL = step.Meta.CacheTTL.String()
		}
		secretCacheInput := false
		for _, name := range step.Meta.CacheEnv {
			if secretLikeCacheEnvName(name) {
				secretCacheInput = true
			}
			for _, secret := range item.SecretNames {
				if secret == name {
					secretCacheInput = true
				}
			}
		}
		if secretCacheInput {
			item.CacheStatus = "redacted"
			item.CacheReason = "secret-like or declared secret environment input contributes to cache identity; use # secret: instead of # cache-env:"
			parent = ""
		} else if parent == "" || len(step.Meta.CacheURL) > 0 {
			item.CacheStatus = "unresolved"
			item.CacheReason = "cache identity needs a resolved parent and URL content; explanation performs no remote lookup"
			parent = ""
		} else {
			key, input, err := buildCacheKey(ctx, parent, step, nil)
			if err != nil {
				return out, fmt.Errorf("explain step %s: %w", step.Name, err)
			}
			item.Key = key
			item.Files = input.CacheFile
			planStep := buildPlanStep{Key: key, ParentDigest: input.ParentDigest, ScriptDigest: input.ScriptDigest, AgentProtocolVersion: input.AgentProtocolVersion, Meta: step.Meta}
			item.CacheStatus, item.CacheReason, item.LayerDigest = explainBuildCache(s, planStep, opts.NoCache, time.Now().UTC())
			parent = key
		}
		out.Steps = append(out.Steps, item)
	}
	return out, nil
}

func safeBuildExplanationURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "invalid URL (redacted)"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func explainBuildCache(s store.Store, step buildPlanStep, disabled bool, now time.Time) (string, string, string) {
	if disabled {
		return "disabled", "--no-cache bypasses local entries", ""
	}
	entry, err := loadBuildCacheEntry(s, step.Key)
	if errors.Is(err, os.ErrNotExist) {
		return "miss", "no local entry for the exact current key; previous input changes are not recorded", ""
	}
	if err != nil {
		return "invalid", "local cache metadata is unreadable or invalid", ""
	}
	if !buildCacheEntryFresh(entry, step.Meta.CacheTTL, now) {
		return "miss", "local entry expired or has no creation time for its TTL", ""
	}
	if err := validateBuildCacheEntryForStep(entry, step); err != nil {
		return "invalid", "local entry metadata disagrees with current parent, script, protocol or compaction", ""
	}
	return "hit", "fresh matching local entry; layer blobs and runtime compatibility are checked during execution", entry.LayerDigest
}

func printBuildExplanation(w io.Writer, out buildExplanation, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(out)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "cove build %s\n  base: %s\n  base digest: %s\n  base platform: %s\n  base format: %s\n  remote lookup: %s\n  cache imports: %s\n  consistency: %s\n", out.Name, out.BaseStatus, out.BaseDigest, out.BasePlatform, out.BaseFormat, out.RemoteLookup, out.CacheImports, out.Consistency)
	for i, step := range out.Steps {
		fmt.Fprintf(&b, "  step %d: %s\n    key: %s\n    cache: %s (%s)\n    parent: %s\n    script: %s\n    agent protocol: %s\n    compact: %s\n", i+1, step.Name, step.Key, step.CacheStatus, step.CacheReason, step.ParentDigest, step.ScriptDigest, step.AgentProtocol, step.Compact)
		if step.LayerDigest != "" {
			fmt.Fprintf(&b, "    layer: %s\n", step.LayerDigest)
		}
		if len(step.Environment) > 0 {
			fmt.Fprintf(&b, "    environment names: %s (values omitted)\n", strings.Join(step.Environment, ", "))
		}
		for _, name := range step.DeclaredFiles {
			fmt.Fprintf(&b, "    declared file: %s\n", name)
		}
		var files []string
		for name := range step.Files {
			files = append(files, name)
		}
		sort.Strings(files)
		for _, name := range files {
			fmt.Fprintf(&b, "    file digest: %s %s\n", name, step.Files[name])
		}
		for _, raw := range step.URLs {
			fmt.Fprintf(&b, "    URL: %s (content unresolved)\n", raw)
		}
		if len(step.SecretNames) > 0 {
			fmt.Fprintf(&b, "    secret names: %s (values omitted)\n", strings.Join(step.SecretNames, ", "))
		}
		if step.TTL != "" {
			fmt.Fprintf(&b, "    TTL: %s\n", step.TTL)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
