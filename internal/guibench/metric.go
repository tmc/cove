package guibench

import (
	"fmt"
	"strings"
)

// Metric scores a getter result against an optional expected reference.
//
// A metric is a pure function: it never touches a VM, the network, or the
// clock. result is the value the getter extracted off the guest; expected is
// the gold reference (empty when the metric is self-contained, e.g.
// file_exists); options carries metric-specific knobs. The returned score is
// in [0,1]; an error reports a malformed call (bad option type, missing
// expected value), never a low score.
type Metric func(result, expected string, options map[string]any) (float64, error)

// Metrics returns the registry of built-in metrics keyed by name. The map is
// freshly allocated on each call, so callers may mutate it freely.
func Metrics() map[string]Metric {
	return map[string]Metric{
		"exact_match":        metricExactMatch,
		"must_include":       metricMustInclude,
		"fuzzy_match":        metricFuzzyMatch,
		"file_exists":        metricFileExists,
		"hash_equals":        metricHashEquals,
		"plist_equals":       metricPlistEquals,
		"sqlite_row_matches": metricSQLiteRowMatches,
		"url_in":             metricURLIn,
		"infeasible":         metricInfeasible,
		// Before/after whole-table integrity (collateral-damage check),
		// implemented in integrity.go.
		"rows_added_integrity":   metricRowsAddedIntegrity,
		"rows_removed_integrity": metricRowsRemovedIntegrity,
		// AX-tree node existence + attribute match, implemented in
		// metric_accessibility.go.
		"accessibility_match": metricAccessibilityMatch,
		// Fidelity metrics: tolerant comparison for lossy native-macOS edits
		// (image re-encode, URL tracking params, PDF reflow), in
		// metric_fidelity.go.
		"image_similar":        metricImageSimilar,
		"pdf_contains":         metricPDFContains,
		"url_match_normalized": metricURLMatchNormalized,
	}
}

// metricNames returns the registered metric names, used for validation.
func metricNames() map[string]bool {
	names := make(map[string]bool)
	for name := range Metrics() {
		names[name] = true
	}
	return names
}

// score collapses a bool match into the [0,1] contract.
func score(ok bool) float64 {
	if ok {
		return 1
	}
	return 0
}

// metricExactMatch scores 1 iff result equals expected byte-for-byte.
func metricExactMatch(result, expected string, _ map[string]any) (float64, error) {
	return score(result == expected), nil
}

// metricMustInclude scores 1 iff result contains expected as a substring.
func metricMustInclude(result, expected string, _ map[string]any) (float64, error) {
	return score(strings.Contains(result, expected)), nil
}

// metricFuzzyMatch scores 1 iff result and expected are equal after
// normalization (lowercase, trim, collapse internal whitespace). This is a
// string normalizer, never an LLM judge (design 047 §5).
func metricFuzzyMatch(result, expected string, _ map[string]any) (float64, error) {
	return score(normalize(result) == normalize(expected)), nil
}

// metricFileExists scores 1 iff the getter reports the path present. The
// exec/file getter yields "true"/"false" (or "1"/"0"); anything truthy passes.
func metricFileExists(result, _ string, _ map[string]any) (float64, error) {
	return score(truthy(result)), nil
}

// metricHashEquals scores 1 iff the getter-computed hash equals expected,
// compared case-insensitively (hex digests vary in case).
func metricHashEquals(result, expected string, _ map[string]any) (float64, error) {
	if expected == "" {
		return 0, fmt.Errorf("hash_equals: expected hash is empty")
	}
	return score(strings.EqualFold(strings.TrimSpace(result), strings.TrimSpace(expected))), nil
}

// metricPlistEquals scores 1 iff the defaults/plist value equals expected.
// The getter has already flushed cfprefsd and emitted the live scalar value
// (design 047 §7), so the comparison is a trimmed string equality.
func metricPlistEquals(result, expected string, _ map[string]any) (float64, error) {
	return score(strings.TrimSpace(result) == strings.TrimSpace(expected)), nil
}

// metricSQLiteRowMatches scores 1 iff the extracted row value equals expected.
// The getter runs the query (with a WAL checkpoint) and passes the already
// extracted scalar here, keeping the metric pure and VM-free. The optional
// "ignore_case" option folds case.
func metricSQLiteRowMatches(result, expected string, options map[string]any) (float64, error) {
	ignoreCase, err := boolOption(options, "ignore_case", false)
	if err != nil {
		return 0, fmt.Errorf("sqlite_row_matches: %w", err)
	}
	r, e := strings.TrimSpace(result), strings.TrimSpace(expected)
	if ignoreCase {
		return score(strings.EqualFold(r, e)), nil
	}
	return score(r == e), nil
}

// metricURLIn scores 1 iff the gold expected URL appears in result (the
// agent-reported URL), tolerating a trailing slash. WebArena's url-match is
// gold-in-pred (design 047 §5).
func metricURLIn(result, expected string, _ map[string]any) (float64, error) {
	if expected == "" {
		return 0, fmt.Errorf("url_in: expected url is empty")
	}
	return score(strings.Contains(normalizeURL(result), normalizeURL(expected))), nil
}

// metricInfeasible scores 1 iff the agent's terminal answer is FAIL. result is
// the agent's final answer; the task is infeasible by construction, so success
// means the agent correctly declined.
func metricInfeasible(result, _ string, _ map[string]any) (float64, error) {
	return score(strings.EqualFold(strings.TrimSpace(result), "FAIL")), nil
}

// normalize lowercases, trims, and collapses internal whitespace runs to a
// single space.
func normalize(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// normalizeURL trims surrounding whitespace and a single trailing slash so
// "https://example.com/" and "https://example.com" compare equal.
func normalizeURL(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// truthy reports whether a getter string represents a positive result.
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

// boolOption reads a bool option, returning def when absent.
func boolOption(options map[string]any, key string, def bool) (bool, error) {
	v, ok := options[key]
	if !ok {
		return def, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("option %q: want bool, got %T", key, v)
	}
	return b, nil
}

// optionalBoolOption reads a bool option as a tri-state, returning nil when the
// key is absent so a caller can tell "unset" from "false". A non-bool value is a
// malformed call.
func optionalBoolOption(options map[string]any, key string) (*bool, error) {
	v, ok := options[key]
	if !ok {
		return nil, nil
	}
	b, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("option %q: want bool, got %T", key, v)
	}
	return &b, nil
}
