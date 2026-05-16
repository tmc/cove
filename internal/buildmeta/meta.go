// Package buildmeta parses metadata comments in cove build scripts.
package buildmeta

import (
	"bytes"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/txtar"
)

// ScriptMeta is the parsed metadata from a build script header.
type ScriptMeta struct {
	CacheEnv   []string
	CacheURL   []string
	CacheFile  []string
	CacheTTL   time.Duration
	Secrets    []string
	SecretFrom []SecretRef
	Compact    string
	HasMount   bool
	MountValue string
}

// SecretRef maps a target secret name to a source URI.
type SecretRef struct {
	Name string
	URI  string
	Line int
}

// ParseScript parses build script metadata comments.
func ParseScript(data []byte) (ScriptMeta, error) {
	var meta ScriptMeta
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
			d, err := ParseDuration(value)
			if err != nil {
				return meta, err
			}
			meta.CacheTTL = d
		case "secret":
			meta.Secrets = appendFields(meta.Secrets, value)
		case "secret-from":
			refs, err := ParseSecretFrom(value, lineNo)
			if err != nil {
				return meta, err
			}
			meta.SecretFrom = append(meta.SecretFrom, refs...)
		case "compact":
			compact := strings.ToLower(strings.TrimSpace(value))
			if err := ValidateCompactMode(compact); err != nil {
				return meta, err
			}
			meta.Compact = compact
		case "mount":
			meta.HasMount = true
			meta.MountValue = value
		}
	}
	meta.CacheEnv = UniqueSorted(meta.CacheEnv)
	meta.CacheURL = UniqueSorted(meta.CacheURL)
	meta.CacheFile = UniqueSorted(meta.CacheFile)
	meta.Secrets = UniqueSorted(meta.Secrets)
	meta.SecretFrom = SortedSecretRefs(meta.SecretFrom)
	if meta.Compact == "" {
		meta.Compact = "targeted"
	}
	return meta, nil
}

// ParseSecretFrom parses a secret-from header value.
func ParseSecretFrom(value string, line int) ([]SecretRef, error) {
	var refs []SecretRef
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
		if !ValidSecretName(name) {
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
		refs = append(refs, SecretRef{Name: name, URI: uri, Line: line})
	}
	return refs, nil
}

// SortedSecretRefs returns refs in canonical order.
func SortedSecretRefs(in []SecretRef) []SecretRef {
	if len(in) == 0 {
		return nil
	}
	out := append([]SecretRef(nil), in...)
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

// UniqueSorted returns trimmed, deduplicated strings in sorted order.
func UniqueSorted(in []string) []string {
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

// ParseDuration parses a positive build cache TTL.
func ParseDuration(s string) (time.Duration, error) {
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

// ValidateCompactMode reports whether mode is a supported build compact mode.
func ValidateCompactMode(mode string) error {
	switch mode {
	case "fast", "targeted", "thorough":
		return nil
	case "":
		return fmt.Errorf("compact: empty mode")
	default:
		return fmt.Errorf("compact: invalid mode %q", mode)
	}
}

// ValidSecretName reports whether name can be used as a build secret name.
func ValidSecretName(name string) bool {
	return name != "" && !strings.Contains(name, "/")
}
