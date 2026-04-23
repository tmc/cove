package ociimage

import (
	"fmt"
	"strings"
)

// Reference is an OCI image reference split into registry, repository, and
// optional tag or digest.
type Reference struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string
}

// ParseReference parses refs such as ghcr.io/acme/macos:latest.
func ParseReference(ref string) (Reference, error) {
	var out Reference
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return out, fmt.Errorf("empty reference")
	}
	if strings.Contains(ref, "://") {
		return out, fmt.Errorf("reference must not include a URL scheme")
	}

	name, digest, ok := strings.Cut(ref, "@")
	if ok {
		if digest == "" {
			return out, fmt.Errorf("empty digest")
		}
		if strings.Contains(digest, "@") {
			return out, fmt.Errorf("invalid digest")
		}
		out.Digest = digest
	}

	slash := strings.LastIndex(name, "/")
	colon := strings.LastIndex(name, ":")
	if colon > slash {
		out.Tag = name[colon+1:]
		name = name[:colon]
		if err := ValidateTag(out.Tag); err != nil {
			return out, err
		}
	}
	if name == "" {
		return out, fmt.Errorf("empty repository")
	}

	registry, repo, ok := strings.Cut(name, "/")
	if !ok || registry == "" || repo == "" {
		return out, fmt.Errorf("reference must include registry and repository")
	}
	if err := validateRegistry(registry); err != nil {
		return out, err
	}
	if err := validateRepository(repo); err != nil {
		return out, err
	}

	out.Registry = registry
	out.Repository = repo
	return out, nil
}

// ValidateTag reports whether tag is a valid OCI tag.
func ValidateTag(tag string) error {
	if tag == "" {
		return fmt.Errorf("empty tag")
	}
	if len(tag) > 128 {
		return fmt.Errorf("tag too long")
	}
	for i, r := range tag {
		ok := r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' ||
			r == '_' ||
			i > 0 && (r == '.' || r == '-')
		if !ok {
			return fmt.Errorf("invalid tag %q", tag)
		}
	}
	return nil
}

func (r Reference) String() string {
	ref := r.Registry + "/" + r.Repository
	if r.Tag != "" {
		ref += ":" + r.Tag
	}
	if r.Digest != "" {
		ref += "@" + r.Digest
	}
	return ref
}

func validateRegistry(registry string) error {
	if strings.ContainsAny(registry, " \t\n/") {
		return fmt.Errorf("invalid registry %q", registry)
	}
	if strings.HasPrefix(registry, ".") || strings.HasSuffix(registry, ".") {
		return fmt.Errorf("invalid registry %q", registry)
	}
	if registry != "localhost" && !strings.ContainsAny(registry, ".:") {
		return fmt.Errorf("reference must include registry and repository")
	}
	if host, port, ok := strings.Cut(registry, ":"); ok {
		if host == "" || port == "" {
			return fmt.Errorf("invalid registry %q", registry)
		}
		for _, r := range port {
			if r < '0' || r > '9' {
				return fmt.Errorf("invalid registry %q", registry)
			}
		}
	}
	return nil
}

func validateRepository(repo string) error {
	for _, part := range strings.Split(repo, "/") {
		if part == "" {
			return fmt.Errorf("invalid repository %q", repo)
		}
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") ||
			strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return fmt.Errorf("invalid repository %q", repo)
		}
		for _, r := range part {
			ok := r >= 'a' && r <= 'z' ||
				r >= '0' && r <= '9' ||
				r == '.' ||
				r == '_' ||
				r == '-'
			if !ok {
				return fmt.Errorf("invalid repository %q", repo)
			}
		}
	}
	return nil
}
