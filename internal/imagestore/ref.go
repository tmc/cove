// Package imagestore defines local image-store paths and references.
package imagestore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BaseDir returns the local image store root.
func BaseDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "images")
}

// Ref is a parsed name[:tag] image reference.
type Ref struct {
	Name string
	Tag  string
}

// String renders the canonical "name:tag" form.
func (r Ref) String() string { return r.Name + ":" + r.Tag }

// Path returns the on-disk directory for this image ref.
func (r Ref) Path() string {
	parts := append([]string{BaseDir()}, strings.Split(r.Name, "/")...)
	parts = append(parts, r.Tag)
	return filepath.Join(parts...)
}

// ErrRefInvalid is returned by ParseRef when a reference is invalid.
var ErrRefInvalid = errors.New("invalid image ref")

// ParseRef parses "name" or "name:tag" into a Ref. Default tag is "latest".
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Ref{}, fmt.Errorf("%w: empty", ErrRefInvalid)
	}
	if strings.Count(s, ":") > 1 {
		return Ref{}, fmt.Errorf("%w: %q contains multiple ':'", ErrRefInvalid, s)
	}
	name, tag, hasTag := strings.Cut(s, ":")
	if name == "" {
		return Ref{}, fmt.Errorf("%w: %q has empty name", ErrRefInvalid, s)
	}
	if !hasTag {
		tag = "latest"
	}
	if tag == "" {
		return Ref{}, fmt.Errorf("%w: %q has empty tag", ErrRefInvalid, s)
	}
	if err := validateName(name); err != nil {
		return Ref{}, fmt.Errorf("%w: name: %v", ErrRefInvalid, err)
	}
	if err := validateComponent(tag); err != nil {
		return Ref{}, fmt.Errorf("%w: tag: %v", ErrRefInvalid, err)
	}
	return Ref{Name: name, Tag: tag}, nil
}

func validateName(s string) error {
	if strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") || strings.Contains(s, "//") {
		return fmt.Errorf("%q must be slash-separated path components", s)
	}
	parts := strings.Split(s, "/")
	if len(parts) > 1 && strings.ContainsAny(parts[0], ".:") {
		return fmt.Errorf("%q looks like a registry reference; use registry/repo:tag for remote images", s)
	}
	for _, part := range parts {
		if err := validateComponent(part); err != nil {
			return err
		}
	}
	return nil
}

func validateComponent(s string) error {
	if len(s) == 0 || len(s) > 128 {
		return fmt.Errorf("%q must be 1..128 characters", s)
	}
	if s == "." || s == ".." {
		return fmt.Errorf("%q is reserved", s)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return fmt.Errorf("%q contains invalid character %q", s, r)
		}
	}
	return nil
}
