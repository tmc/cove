package guibench

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// metricAccessibilityMatch scores the accessibility getter's output against a
// node selector carried in options. It is the macOS-AX analogue of OSWorld's
// check_accessibility_tree (XPath/CSS over a dumped tree): rather than a full
// query language it offers a single flat selector keyed on the AX attributes a
// native task actually asserts — role, title, identifier (subrole) — plus an
// optional value/text target. Like the OSWorld metric it is two-phase: a node
// matching the selector must exist (else 0), and when a value/text is given the
// best-matching node's value is compared.
//
// It accepts both getter shapes (design: getAccessibility):
//
//   - Tree (Dump=true): result is the <ax>…</ax> XML document. role/title/
//     identifier select a node by containment; value/text scores its AXValue.
//   - Scalar (Attr read): result is a single attribute value. With no node
//     selector and a value/text/expected target, the comparison is the scalar
//     itself (exact, or substring when contains is set).
//
// Options (all optional, strings unless noted):
//
//	role        select nodes whose AX role equals this (e.g. "AXTextArea")
//	title       select nodes whose AX title equals this
//	identifier  select nodes whose AX subrole/identifier equals this
//	value, text the expected AXValue of a selected node (text is an alias)
//	exact       bool: value/text must match exactly (default true); when false,
//	            a normalized (case/whitespace) compare is used
//	contains    bool: value/text match is substring containment (overrides exact)
//
// The score is in [0,1]; an error reports a malformed call (bad option type, or
// a node selector against unparseable XML), never a low score (mirrors the
// Metric contract).
func metricAccessibilityMatch(result, expected string, options map[string]any) (float64, error) {
	role, err := stringOption(options, "role")
	if err != nil {
		return 0, fmt.Errorf("accessibility_match: %w", err)
	}
	title, err := stringOption(options, "title")
	if err != nil {
		return 0, fmt.Errorf("accessibility_match: %w", err)
	}
	identifier, err := stringOption(options, "identifier")
	if err != nil {
		return 0, fmt.Errorf("accessibility_match: %w", err)
	}
	want, err := stringOption(options, "value")
	if err != nil {
		return 0, fmt.Errorf("accessibility_match: %w", err)
	}
	if want == "" {
		// "text" is an alias for "value", matching OSWorld's rule key.
		if want, err = stringOption(options, "text"); err != nil {
			return 0, fmt.Errorf("accessibility_match: %w", err)
		}
	}
	// The expected value (or the "expected" option, already folded into expected
	// by ScoreMetrics) is a third spelling of the value target, so callers can
	// drive this metric the same way as the other comparison metrics.
	if want == "" {
		want = expected
	}
	exact, err := boolOption(options, "exact", true)
	if err != nil {
		return 0, fmt.Errorf("accessibility_match: %w", err)
	}
	contains, err := boolOption(options, "contains", false)
	if err != nil {
		return 0, fmt.Errorf("accessibility_match: %w", err)
	}

	hasSelector := role != "" || title != "" || identifier != ""
	if !hasSelector {
		// Scalar mode: no node selector, so result is the attribute value itself.
		return score(valueMatches(result, want, exact, contains)), nil
	}

	// Tree mode: result must be the AX XML dump. A selector against output that
	// is not the tree document is a malformed call, not a low score.
	nodes, err := parseAXTree(result)
	if err != nil {
		return 0, fmt.Errorf("accessibility_match: %w", err)
	}
	matched := selectAXNodes(nodes, role, title, identifier)
	if len(matched) == 0 {
		return 0, nil
	}
	if want == "" {
		// Node existence is the whole assertion.
		return 1, nil
	}
	for _, n := range matched {
		if valueMatches(n.Value, want, exact, contains) {
			return 1, nil
		}
	}
	return 0, nil
}

// valueMatches compares a node's value against the target under the exact /
// contains knobs: contains wins (substring), then exact (byte-for-byte after
// trimming), else a normalized compare (case + whitespace folded).
func valueMatches(got, want string, exact, contains bool) bool {
	if contains {
		return strings.Contains(got, want)
	}
	if exact {
		return strings.TrimSpace(got) == strings.TrimSpace(want)
	}
	return normalize(got) == normalize(want)
}

// axNode is one UI element of the dumped AX tree (see axDumpScript). Children
// are nested by containment; the metric flattens the tree before selecting.
type axNode struct {
	Role       string   `xml:"role,attr"`
	Title      string   `xml:"title,attr"`
	Identifier string   `xml:"identifier,attr"`
	Value      string   `xml:"value,attr"`
	Children   []axNode `xml:"node"`
}

// axTree is the document root the getter emits (<ax app="…">…</ax>).
type axTree struct {
	XMLName xml.Name `xml:"ax"`
	Nodes   []axNode `xml:"node"`
}

// parseAXTree decodes the AX XML dump into a flat slice of nodes in document
// order. An empty result yields no nodes (so an empty front window scores 0,
// not an error); malformed XML is an error.
func parseAXTree(result string) ([]axNode, error) {
	s := strings.TrimSpace(result)
	if s == "" {
		return nil, nil
	}
	var tree axTree
	if err := xml.Unmarshal([]byte(s), &tree); err != nil {
		return nil, fmt.Errorf("parse ax tree: %w", err)
	}
	var flat []axNode
	var walk func(ns []axNode)
	walk = func(ns []axNode) {
		for _, n := range ns {
			flat = append(flat, n)
			walk(n.Children)
		}
	}
	walk(tree.Nodes)
	return flat, nil
}

// selectAXNodes returns the nodes matching every non-empty selector field. An
// empty selector field matches any value, so passing only role selects on role
// alone (OSWorld's selectors compose by AND; this composes the present keys).
func selectAXNodes(nodes []axNode, role, title, identifier string) []axNode {
	var out []axNode
	for _, n := range nodes {
		if role != "" && n.Role != role {
			continue
		}
		if title != "" && n.Title != title {
			continue
		}
		if identifier != "" && n.Identifier != identifier {
			continue
		}
		out = append(out, n)
	}
	return out
}

// stringOption reads a string option, returning "" when absent. A non-string
// value is a malformed call.
func stringOption(options map[string]any, key string) (string, error) {
	v, ok := options[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("option %q: want string, got %T", key, v)
	}
	return s, nil
}
