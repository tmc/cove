package main

import "testing"

func TestParseTemplateValue(t *testing.T) {
	tests := []struct {
		in   string
		want any
	}{
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"false", false},
		{"False", false},
		{"hello", "hello"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := parseTemplateValue(tt.in); got != tt.want {
			t.Errorf("parseTemplateValue(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestTemplateVarFlagSet(t *testing.T) {
	var f templateVarFlag
	if err := f.Set("name=value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if f["name"] != "value" {
		t.Errorf("name = %v, want value", f["name"])
	}
	if err := f.Set("flag=true"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if f["flag"] != true {
		t.Errorf("flag = %v, want true", f["flag"])
	}
	if err := f.Set("  spaced  =val"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if f["spaced"] != "val" {
		t.Errorf("spaced = %v, want val", f["spaced"])
	}

	if err := f.Set("noequals"); err == nil {
		t.Error("Set(noequals) want error")
	}
	if err := f.Set("=value"); err == nil {
		t.Error("Set(=value) want error")
	}
	if err := f.Set("   =value"); err == nil {
		t.Error("Set(   =value) want error")
	}
}

func TestTemplateVarFlagString(t *testing.T) {
	var nilFlag *templateVarFlag
	if got := nilFlag.String(); got != "" {
		t.Errorf("nil String = %q, want empty", got)
	}

	empty := templateVarFlag{}
	if got := empty.String(); got != "" {
		t.Errorf("empty String = %q, want empty", got)
	}

	f := templateVarFlag{"b": "two", "a": "one"}
	if got := f.String(); got != "a=one,b=two" {
		t.Errorf("String = %q, want a=one,b=two", got)
	}
}
