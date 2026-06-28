package guibench

import (
	"math"
	"strings"
	"testing"
)

// axTreeFixture is a synthetic AX dump matching axDumpScript's shape: a Notes
// window holding a text area whose AXValue is the note body, plus a button.
const axTreeFixture = `<ax app="Notes">` +
	`<node role="AXWindow" title="Notes" identifier="" value="">` +
	`<node role="AXTextArea" title="Body" identifier="AXContentList" value="Buy milk" />` +
	`<node role="AXButton" title="Done" identifier="" value="" />` +
	`</node>` +
	`</ax>`

func TestMetricAccessibilityMatch(t *testing.T) {
	tests := []struct {
		name     string
		result   string
		expected string
		options  map[string]any
		want     float64
		wantErr  bool
	}{
		// Tree: node found and its value matches.
		{
			name:    "tree role+value match",
			result:  axTreeFixture,
			options: map[string]any{"role": "AXTextArea", "value": "Buy milk"},
			want:    1,
		},
		{
			name:    "tree title+value match",
			result:  axTreeFixture,
			options: map[string]any{"title": "Body", "value": "Buy milk"},
			want:    1,
		},
		{
			name:    "tree identifier selects",
			result:  axTreeFixture,
			options: map[string]any{"identifier": "AXContentList", "value": "Buy milk"},
			want:    1,
		},
		// Tree: node found but value mismatches.
		{
			name:    "tree value mismatch",
			result:  axTreeFixture,
			options: map[string]any{"role": "AXTextArea", "value": "Buy eggs"},
			want:    0,
		},
		// Tree: node not found.
		{
			name:    "tree role not found",
			result:  axTreeFixture,
			options: map[string]any{"role": "AXSlider", "value": "x"},
			want:    0,
		},
		{
			name:    "tree title not found",
			result:  axTreeFixture,
			options: map[string]any{"title": "Nope"},
			want:    0,
		},
		// Tree: node existence only (no value target).
		{
			name:    "tree existence only",
			result:  axTreeFixture,
			options: map[string]any{"role": "AXButton", "title": "Done"},
			want:    1,
		},
		// Tree: value target carried via expected (not the option).
		{
			name:     "tree value via expected",
			result:   axTreeFixture,
			expected: "Buy milk",
			options:  map[string]any{"role": "AXTextArea"},
			want:     1,
		},
		// Tree: fuzzy (exact=false) folds case + whitespace.
		{
			name:    "tree fuzzy value",
			result:  axTreeFixture,
			options: map[string]any{"role": "AXTextArea", "value": "  buy   MILK ", "exact": false},
			want:    1,
		},
		// Tree: contains is substring.
		{
			name:    "tree contains value",
			result:  axTreeFixture,
			options: map[string]any{"role": "AXTextArea", "value": "milk", "contains": true},
			want:    1,
		},
		{
			name:    "tree exact rejects substring",
			result:  axTreeFixture,
			options: map[string]any{"role": "AXTextArea", "value": "milk"},
			want:    0,
		},
		// Tree: malformed XML with a selector is an error, not a low score.
		{
			name:    "malformed xml with selector",
			result:  "<ax app=\"Notes\"><node role=", // truncated
			options: map[string]any{"role": "AXTextArea"},
			wantErr: true,
		},
		// Tree: empty dump (front window had nothing) scores 0, not error.
		{
			name:    "empty dump no node",
			result:  "",
			options: map[string]any{"role": "AXTextArea"},
			want:    0,
		},
		// Scalar: no selector, result is the attribute value itself.
		{
			name:     "scalar exact match",
			result:   "Dark",
			expected: "Dark",
			want:     1,
		},
		{
			name:     "scalar exact mismatch",
			result:   "Light",
			expected: "Dark",
			want:     0,
		},
		{
			name:    "scalar value option",
			result:  "  Dark \n",
			options: map[string]any{"value": "Dark"},
			want:    1,
		},
		{
			name:    "scalar contains",
			result:  "https://example.com/page",
			options: map[string]any{"value": "example.com", "contains": true},
			want:    1,
		},
		{
			name:    "scalar fuzzy",
			result:  "  HELLO  world ",
			options: map[string]any{"text": "hello world", "exact": false},
			want:    1,
		},
		// Bad option types are malformed calls.
		{
			name:    "bad role type",
			result:  axTreeFixture,
			options: map[string]any{"role": 7},
			wantErr: true,
		},
		{
			name:    "bad exact type",
			result:  axTreeFixture,
			options: map[string]any{"role": "AXTextArea", "exact": "yes"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := metricAccessibilityMatch(tt.result, tt.expected, tt.options)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got score %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAccessibilityMatchRegistered confirms the metric is reachable through the
// public registry under its name, the same way the other metrics register.
func TestAccessibilityMatchRegistered(t *testing.T) {
	m, ok := Metrics()["accessibility_match"]
	if !ok {
		t.Fatal("accessibility_match not registered")
	}
	got, err := m(axTreeFixture, "", map[string]any{"role": "AXTextArea", "value": "Buy milk"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 1 {
		t.Fatalf("score = %v, want 1", got)
	}
}

// axStateFixture carries the design-048 ElementNode state flags and description:
// a disabled, non-settable Save button and an enabled, settable text field.
const axStateFixture = `<ax app="Editor">` +
	`<node role="AXWindow" title="Editor" identifier="" value="" description="" enabled="true" settable="false">` +
	`<node role="AXTextField" title="Name" identifier="" value="draft" description="document name" enabled="true" settable="true" />` +
	`<node role="AXButton" title="Save" identifier="" value="" description="save the document" enabled="false" settable="false" />` +
	`</node>` +
	`</ax>`

// TestAccessibilityMatchStateSelectors exercises the design-048 metric-side
// selectors: enabled, settable, and description, plus the tri-state "flag
// absent never matches" rule.
func TestAccessibilityMatchStateSelectors(t *testing.T) {
	tests := []struct {
		name    string
		result  string
		options map[string]any
		want    float64
	}{
		{
			name:    "settable field exists",
			result:  axStateFixture,
			options: map[string]any{"role": "AXTextField", "settable": true},
			want:    1,
		},
		{
			name:    "disabled save button exists",
			result:  axStateFixture,
			options: map[string]any{"role": "AXButton", "enabled": false},
			want:    1,
		},
		{
			name:    "save button is not enabled (no match)",
			result:  axStateFixture,
			options: map[string]any{"role": "AXButton", "enabled": true},
			want:    0,
		},
		{
			name:    "non-settable button by settable=false",
			result:  axStateFixture,
			options: map[string]any{"title": "Save", "settable": false},
			want:    1,
		},
		{
			name:    "description selects the field, value matches",
			result:  axStateFixture,
			options: map[string]any{"description": "document name", "value": "draft"},
			want:    1,
		},
		{
			name:    "enabled+settable field, value check",
			result:  axStateFixture,
			options: map[string]any{"enabled": true, "settable": true, "value": "draft"},
			want:    1,
		},
		{
			name:    "flag absent never matches a non-nil constraint",
			result:  axTreeFixture, // this fixture omits enabled/settable
			options: map[string]any{"role": "AXButton", "enabled": true},
			want:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := metricAccessibilityMatch(tt.result, "", tt.options)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAccessibilityMatchBadBoolOption rejects a non-bool enabled/settable.
func TestAccessibilityMatchBadBoolOption(t *testing.T) {
	if _, err := metricAccessibilityMatch(axStateFixture, "", map[string]any{"enabled": "yes"}); err == nil {
		t.Fatal("want error for non-bool enabled option")
	}
}

// TestParseAXTreeStateFields confirms the parser reads the design-048 superset
// fields (description, enabled, settable) off the dump.
func TestParseAXTreeStateFields(t *testing.T) {
	nodes, err := parseAXTree(axStateFixture)
	if err != nil {
		t.Fatalf("parseAXTree: %v", err)
	}
	var field *axNode
	for i := range nodes {
		if nodes[i].Role == "AXTextField" {
			field = &nodes[i]
		}
	}
	if field == nil {
		t.Fatal("AXTextField not parsed")
	}
	if field.Description != "document name" || field.Enabled != "true" || field.Settable != "true" {
		t.Errorf("field state = desc=%q enabled=%q settable=%q", field.Description, field.Enabled, field.Settable)
	}
}

func TestParseAXTree(t *testing.T) {
	nodes, err := parseAXTree(axTreeFixture)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Window, text area, button: flattened in document order.
	if len(nodes) != 3 {
		t.Fatalf("flattened nodes = %d, want 3", len(nodes))
	}
	if nodes[0].Role != "AXWindow" || nodes[1].Role != "AXTextArea" || nodes[2].Role != "AXButton" {
		t.Fatalf("unexpected roles: %v %v %v", nodes[0].Role, nodes[1].Role, nodes[2].Role)
	}
	if nodes[1].Value != "Buy milk" || nodes[1].Identifier != "AXContentList" {
		t.Fatalf("text area attrs wrong: %+v", nodes[1])
	}
}

// TestAXDumpGetter exercises the Dump branch of the accessibility getter end to
// end through a FakeProbe: the getter emits the JXA dump program and returns its
// XML, which the metric then selects over. Live AX reads need operator hardware.
func TestAXDumpGetter(t *testing.T) {
	spec := GetterSpec{Kind: "accessibility", App: "Notes", Dump: true}
	if err := spec.validate(); err != nil {
		t.Fatalf("validate dump spec: %v", err)
	}
	want := axDumpScript("Notes")
	probe := execSpyProbe(func(args []string) (int, string, string, error) {
		if len(args) != 5 || args[0] != "osascript" || args[1] != "-l" || args[2] != "JavaScript" || args[3] != "-e" {
			t.Fatalf("dump getter emitted %v, want `osascript -l JavaScript -e <script>`", args)
		}
		if args[4] != want {
			t.Fatalf("dump getter script mismatch")
		}
		return 0, axTreeFixture + "\n", "", nil
	})
	got, err := spec.Get(probe, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != axTreeFixture {
		t.Fatalf("dump getter result = %q, want fixture", got)
	}
	score, err := metricAccessibilityMatch(got, "", map[string]any{"role": "AXTextArea", "value": "Buy milk"})
	if err != nil || score != 1 {
		t.Fatalf("score over dump = %v, err = %v, want 1", score, err)
	}
}

// TestAXDumpScriptQuoting asserts the dump program JSON-quotes the app name so a
// value with a quote cannot break out of the JavaScript string literal.
func TestAXDumpScriptQuoting(t *testing.T) {
	got := axDumpScript(`No"tes`)
	if strings.Contains(got, `"No"tes"`) {
		t.Fatalf("app name not escaped: %q", got)
	}
	if !strings.Contains(got, `No\"tes`) {
		t.Fatalf("expected escaped quote in %q", got)
	}
}

// TestAXDumpSpecValidateScalarStillNeedsAttr confirms a non-dump accessibility
// spec still requires Attr (the Dump relaxation must not weaken scalar specs).
func TestAXDumpSpecValidateScalarStillNeedsAttr(t *testing.T) {
	if err := (GetterSpec{Kind: "accessibility", App: "Notes"}).validate(); err == nil {
		t.Fatal("scalar accessibility spec without attr should fail validation")
	}
	if err := (GetterSpec{Kind: "accessibility", App: "Notes", Attr: "value"}).validate(); err != nil {
		t.Fatalf("scalar accessibility spec with attr should validate: %v", err)
	}
}
