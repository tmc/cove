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
//	description select nodes whose AX description equals this
//	enabled     bool: select only nodes the dump reports as AXEnabled == this
//	settable    bool: select only nodes the dump reports as AXSettable == this
//	value, text the expected AXValue of a selected node (text is an alias)
//	exact       bool: value/text must match exactly (default true); when false,
//	            a normalized (case/whitespace) compare is used
//	contains    bool: value/text match is substring containment (overrides exact)
//
// The enabled/settable selectors are the design-048 ElementNode state flags
// surfaced metric-side (the in-guest snapshot RPC is a later slice): they let a
// task assert logical state — that a control became enabled, or a field is
// editable — without depending on screen geometry. A node whose dump omitted the
// flag never matches a non-nil enabled/settable constraint.
//
// The score is in [0,1]; an error reports a malformed call (bad option type, or
// a node selector against unparseable XML), never a low score (mirrors the
// Metric contract).
//
// Limitations (deliberate; prefer a disk-state getter when a task admits one):
//
//   - Flat selector, not a path language. role/title/identifier are matched by
//     equality against any node in the flattened tree; there is no XPath/CSS
//     path, no ancestor/descendant or sibling constraint, and no negation. A
//     task that needs "the text field inside the Save sheet" cannot be expressed
//     beyond what those three attributes happen to disambiguate.
//   - OR over matches, so it cannot assert cardinality. When several nodes match
//     the selector the value test passes if ANY of them matches (existence is
//     "at least one"); the metric cannot assert "exactly one such node" or count.
//   - The target is the live AX subtree of the front window only (see
//     [axDumpScript]), so off-window or background state is invisible, and the
//     tree's shape, attribute spelling, and localization can shift across macOS
//     releases — a more brittle signal than a plist/SQLite read of the same fact.
//   - It needs the Tier-C Accessibility grant (independent of Apple Events and
//     Full Disk Access). A denial surfaces in the getter as a hard error, never
//     as a low score, so a miswired grant fails the task rather than silently
//     scoring 0 (design 047 §5).
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
	description, err := stringOption(options, "description")
	if err != nil {
		return 0, fmt.Errorf("accessibility_match: %w", err)
	}
	enabled, err := optionalBoolOption(options, "enabled")
	if err != nil {
		return 0, fmt.Errorf("accessibility_match: %w", err)
	}
	settable, err := optionalBoolOption(options, "settable")
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

	sel := axSelector{
		role:        role,
		title:       title,
		identifier:  identifier,
		description: description,
		enabled:     enabled,
		settable:    settable,
	}
	if !sel.active() {
		// Scalar mode: no node selector, so result is the attribute value itself.
		return score(valueMatches(result, want, exact, contains)), nil
	}

	// Tree mode: result must be the AX XML dump. A selector against output that
	// is not the tree document is a malformed call, not a low score.
	nodes, err := parseAXTree(result)
	if err != nil {
		return 0, fmt.Errorf("accessibility_match: %w", err)
	}
	matched := selectAXNodes(nodes, sel)
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
// are nested by containment; the metric flattens the tree before selecting. The
// shape tracks the design-048 ElementNode superset: role/title/identifier/value
// plus description and the enabled/settable state flags, which the selectors
// query, and the geometry/index fields, which are parsed but reserved for the
// in-guest AX snapshot (design 048) — the verifier asserts logical state, not
// screen geometry, so no geometry selector exists yet.
type axNode struct {
	Role        string `xml:"role,attr"`
	Title       string `xml:"title,attr"`
	Identifier  string `xml:"identifier,attr"`
	Value       string `xml:"value,attr"`
	Description string `xml:"description,attr"`
	// Enabled and Settable are tri-state: "" (the dumper did not report the flag),
	// "true", or "false". A selector on them matches only when the flag is present
	// and equal, so a dump that omits the attribute never spuriously matches.
	Enabled  string `xml:"enabled,attr"`
	Settable string `xml:"settable,attr"`
	// Geometry and index are parsed for the design-048 ElementNode shape but not
	// exposed to the selector API: screen geometry is an action-space primitive
	// (where to click), too fragile for outcome assertions, and index/parent are
	// for the stateful get_app_state→index→act loop the in-guest snapshot enables.
	X           int      `xml:"x,attr"`
	Y           int      `xml:"y,attr"`
	Width       int      `xml:"w,attr"`
	Height      int      `xml:"h,attr"`
	Index       int      `xml:"index,attr"`
	ParentIndex int      `xml:"parent,attr"`
	Children    []axNode `xml:"node"`
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
// axSelector is the set of node-attribute constraints a selection ANDs together.
// An empty string field (or nil bool) is "don't constrain on this attribute".
type axSelector struct {
	role        string
	title       string
	identifier  string
	description string
	// enabled and settable, when non-nil, require the node to report that flag
	// present and equal. A node whose dump omitted the flag never matches a
	// non-nil constraint, so a missing flag is treated as "unknown", not a match.
	enabled  *bool
	settable *bool
}

// active reports whether the selector constrains anything; a no-op selector
// means scalar mode (no node selection).
func (s axSelector) active() bool {
	return s.role != "" || s.title != "" || s.identifier != "" ||
		s.description != "" || s.enabled != nil || s.settable != nil
}

// selectAXNodes returns the flattened nodes matching every constraint in sel.
func selectAXNodes(nodes []axNode, sel axSelector) []axNode {
	var out []axNode
	for _, n := range nodes {
		if sel.role != "" && n.Role != sel.role {
			continue
		}
		if sel.title != "" && n.Title != sel.title {
			continue
		}
		if sel.identifier != "" && n.Identifier != sel.identifier {
			continue
		}
		if sel.description != "" && n.Description != sel.description {
			continue
		}
		if sel.enabled != nil && !flagEquals(n.Enabled, *sel.enabled) {
			continue
		}
		if sel.settable != nil && !flagEquals(n.Settable, *sel.settable) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// flagEquals reports whether a tri-state AX flag ("", "true", "false") is present
// and equals want. An absent flag ("") never matches, so a dump that did not
// report the attribute does not spuriously satisfy an enabled/settable selector.
func flagEquals(flag string, want bool) bool {
	switch strings.ToLower(strings.TrimSpace(flag)) {
	case "true", "1", "yes":
		return want
	case "false", "0", "no":
		return !want
	default:
		return false
	}
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
