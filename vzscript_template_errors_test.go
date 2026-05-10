package main

import (
	"strings"
	"testing"
)

func TestRenderVZScriptTemplateParseError(t *testing.T) {
	_, err := renderVZScriptTemplate([]byte("{{ unclosed"), "bad.tmpl", nil)
	if err == nil || !strings.Contains(err.Error(), "parse template") {
		t.Fatalf("err = %v, want parse template", err)
	}
}

func TestRenderVZScriptTemplateExecuteError(t *testing.T) {
	_, err := renderVZScriptTemplate([]byte(`{{ index .Slice 999 }}`), "bad.tmpl", map[string]any{"Slice": []int{1}})
	if err == nil || !strings.Contains(err.Error(), "execute template") {
		t.Fatalf("err = %v, want execute template", err)
	}
}
