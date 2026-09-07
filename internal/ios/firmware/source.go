//go:build darwin || linux

package firmware

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// SourceCommit is the upstream revision used by cove's firmware adapter.
const SourceCommit = "87f796c62a7cb385cd37afce121f6e222d83e5b5"
const sourceURL = "https://github.com/Lakr233/vphone-cli.git"

// Source records verified source inputs, not compiler, Python or guest readiness.
type Source struct {
	SchemaVersion      int         `json:"schemaVersion"`
	Commit             string      `json:"commit"`
	RequirementsSHA256 string      `json:"requirementsSHA256"`
	Submodules         []Submodule `json:"submodules"`
	Problems           []string    `json:"problems,omitempty"`
}

// Submodule identifies a checked-out dependency and its Git status.
type Submodule struct {
	Path   string `json:"path"`
	Commit string `json:"commit"`
	Status string `json:"status"`
}

// InspectSource verifies a checkout against cove's pinned vphone revision.
// Source problems are returned in the report; command and filesystem errors are
// returned separately. It does not modify the checkout or fetch dependencies.
func InspectSource(ctx context.Context, dir string) (Source, error) {
	report := Source{SchemaVersion: 1, Submodules: []Submodule{}}
	if err := ctx.Err(); err != nil {
		return report, err
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		report.Problems = append(report.Problems, "toolchain source directory is missing")
		return report, nil
	}
	if err != nil {
		return report, fmt.Errorf("inspect toolchain source directory: %w", err)
	}
	if !info.IsDir() {
		report.Problems = append(report.Problems, "toolchain source path is not a directory")
		return report, nil
	}
	commit, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return report, err
	}
	report.Commit = strings.TrimSpace(commit)
	if report.Commit != SourceCommit {
		report.Problems = append(report.Problems, "upstream commit does not match cove's pinned revision")
	}
	status, err := git(ctx, dir, "status", "--porcelain", "--untracked-files=no", "--ignore-submodules=none")
	if err != nil {
		return report, err
	}
	if strings.TrimSpace(status) != "" {
		report.Problems = append(report.Problems, "checkout has tracked or submodule changes")
	}
	modules, err := git(ctx, dir, "submodule", "status", "--recursive")
	if err != nil {
		return report, err
	}
	report.Submodules, err = parseSubmodules(modules)
	if err != nil {
		return report, err
	}
	for _, module := range report.Submodules {
		if module.Status != "clean" {
			report.Problems = append(report.Problems, module.Path+": "+module.Status)
		}
	}
	requirements, err := os.ReadFile(filepath.Join(dir, "requirements.txt"))
	if err != nil {
		return report, fmt.Errorf("read toolchain requirements: %w", err)
	}
	report.RequirementsSHA256 = fmt.Sprintf("%x", sha256.Sum256(requirements))
	committed, err := git(ctx, dir, "show", "HEAD:requirements.txt")
	if err != nil {
		return report, err
	}
	if string(requirements) != committed {
		report.Problems = append(report.Problems, "requirements differ from the committed file")
	}
	return report, nil
}

// PrepareSource checks out pinned sources and all recursive submodules under
// destination/source. An empty repository uses the pinned upstream URL; a local
// repository can supply Git objects without modifying that checkout. Existing
// destinations are verified and reused only when their source manifest exists.
// Call with a deadline. A successful result does not imply runnable tools.
func PrepareSource(ctx context.Context, destination, repository string) (report Source, err error) {
	if err := ctx.Err(); err != nil {
		return report, err
	}
	source := filepath.Join(destination, "source")
	if _, err := os.Lstat(destination); err == nil {
		data, err := os.ReadFile(filepath.Join(destination, "source-manifest.json"))
		if err != nil {
			return report, fmt.Errorf("read existing source manifest: %w", err)
		}
		var previous Source
		if err := json.Unmarshal(data, &previous); err != nil {
			return report, fmt.Errorf("parse source manifest: %w", err)
		}
		report, err = InspectSource(ctx, source)
		if err != nil {
			return report, err
		}
		if len(report.Problems) != 0 {
			return report, fmt.Errorf("toolchain source verification failed: %s", strings.Join(report.Problems, "; "))
		}
		if previous.SchemaVersion != 1 || previous.Commit != report.Commit || previous.RequirementsSHA256 != report.RequirementsSHA256 || !slices.Equal(previous.Submodules, report.Submodules) || len(previous.Problems) != 0 {
			return report, fmt.Errorf("toolchain source manifest mismatch")
		}
		return report, nil
	} else if !os.IsNotExist(err) {
		return report, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return report, err
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return report, fmt.Errorf("create toolchain source directory: %w", err)
	}
	defer func() {
		if err != nil {
			if cleanupErr := os.RemoveAll(destination); cleanupErr != nil {
				err = fmt.Errorf("%w; remove incomplete toolchain source: %v", err, cleanupErr)
			}
		}
	}()
	if repository == "" {
		repository = sourceURL
	}
	if _, err := git(ctx, "", "clone", "--quiet", "--no-checkout", "--no-hardlinks", "--", repository, source); err != nil {
		return report, err
	}
	if _, err := git(ctx, source, "checkout", "--quiet", "--detach", SourceCommit); err != nil {
		return report, err
	}
	if _, err := git(ctx, source, "submodule", "update", "--quiet", "--init", "--recursive"); err != nil {
		return report, err
	}
	report, err = InspectSource(ctx, source)
	if err != nil {
		return report, err
	}
	if len(report.Problems) != 0 {
		return report, fmt.Errorf("toolchain source verification failed: %s", strings.Join(report.Problems, "; "))
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return report, err
	}
	if err := os.WriteFile(filepath.Join(destination, "source-manifest.json"), append(data, '\n'), 0600); err != nil {
		return report, err
	}
	return report, nil
}

func parseSubmodules(output string) ([]Submodule, error) {
	modules := []Submodule{}
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line[1:])
		if len(fields) < 2 || len(fields[0]) != 40 {
			return nil, fmt.Errorf("invalid git submodule status")
		}
		for _, c := range fields[0] {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return nil, fmt.Errorf("invalid submodule commit")
			}
		}
		status := ""
		switch line[0] {
		case ' ':
			status = "clean"
		case '-':
			status = "uninitialized"
		case '+':
			status = "commit mismatch"
		case 'U':
			status = "conflict"
		default:
			return nil, fmt.Errorf("unknown submodule status")
		}
		path := fields[1]
		if filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, "../") {
			return nil, fmt.Errorf("invalid submodule path")
		}
		modules = append(modules, Submodule{Path: path, Commit: fields[0], Status: status})
	}
	return modules, nil
}
