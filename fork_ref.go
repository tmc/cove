// fork_ref.go — parse "parent[@snapshot]" refs for cove fork --from.
package main

import (
	"errors"
	"fmt"
	"strings"
)

// parseForkRef parses a fork ref of the form "parent" or
// "parent@snapshot" into its components. The parent name must not be
// empty; the snapshot name, when present, is validated against
// validateSnapshotName so it is safe for use as a path component.
//
// Examples:
//
//	"macos-base"          → parent="macos-base",   snapshot=""
//	"macos-base@clean"    → parent="macos-base",   snapshot="clean"
//	"macos-base@"         → error (empty snapshot)
//	"@clean"              → error (empty parent)
//	"a@b@c"               → error (multiple @)
func parseForkRef(s string) (parent, snapshot string, err error) {
	if s == "" {
		return "", "", errors.New("fork ref must not be empty")
	}
	if strings.Count(s, "@") > 1 {
		return "", "", fmt.Errorf("fork ref %q contains multiple '@'", s)
	}
	parent, snapshot, hasAt := strings.Cut(s, "@")
	if parent == "" {
		return "", "", fmt.Errorf("fork ref %q is missing parent name", s)
	}
	if hasAt {
		if snapshot == "" {
			return "", "", fmt.Errorf("fork ref %q has '@' but empty snapshot name", s)
		}
		if err := validateSnapshotName(snapshot); err != nil {
			return "", "", fmt.Errorf("fork ref %q: %w", s, err)
		}
	}
	return parent, snapshot, nil
}
