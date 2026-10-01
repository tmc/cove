package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVZScriptPlanValidation(t *testing.T) {
	tests := []struct{ name, source, want string }{
		{"quoted", "guest-exec printf 'a path with spaces' 'Don''t'\n", ""},
		{"negation", "! guest-ping\n? guest-ping\n", ""},
		{"guarded-negation", "[!root] ! guest-exec 'some path'\n", ""},
		{"unknown", "not-a-command 'argument'\n", "syntax"},
		{"guarded-unknown", "[!root] not-a-command\n", "syntax"},
		{"unknown-condition", "[missing] guest-ping\n", "syntax"},
		{"malformed-quote", "guest-exec 'missing end\n", "syntax"},
		{"invalid-os", "# guest-os: toaster\nguest-ping\n", "metadata"},
		{"invalid-route", "# runs-on: administrator\nguest-ping\n", "metadata"},
		{"invalid-mount", "# mount: /source invalid\nguest-ping\n", "metadata"},
		{"missing-injection", "# inject: /target missing.txt\nguest-ping\n", "archive"},
		{"malformed-txtar", "guest-shell file.sh\n-- file.sh -\n", "archive"},
		{"duplicate-txtar", "guest-shell file.sh\n-- file.sh --\nx\n-- file.sh --\ny\n", "archive"},
		{"unsafe-txtar", "guest-ping\n-- ../unsafe --\nx\n", "archive"},
		{"template", "guest-exec '{{.Password}}'\n", "unresolved-template"},
		{"env", "env TOKEN=super-secret\nguest-exec $TOKEN\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := planVZScripts([]string{"test"}, "", func(string) ([]byte, error) { return []byte(tt.source), nil })
			if tt.want == "" {
				if len(p.Diagnostics) > 0 {
					t.Fatalf("unexpected diagnostics: %+v", p.Diagnostics)
				}
				return
			}
			for _, d := range p.Diagnostics {
				if d.Class == tt.want && d.Line > 0 && d.NextAction != "" {
					return
				}
			}
			t.Fatalf("diagnostics = %+v, want %s", p.Diagnostics, tt.want)
		})
	}
}

func TestVZScriptPlanDependencies(t *testing.T) {
	data := map[string]string{"a": "# requires: b,c\nguest-ping\n", "b": "# requires: d\nguest-ping\n", "c": "# requires: d\nguest-ping\n", "d": "guest-ping\n"}
	load := func(name string) ([]byte, error) {
		v, ok := data[name]
		if !ok {
			return nil, fmt.Errorf("missing %s", name)
		}
		return []byte(v), nil
	}
	p := planVZScripts([]string{"a"}, "", load)
	var order []string
	for _, r := range p.Recipes {
		order = append(order, r.Source)
	}
	if !reflect.DeepEqual(order, []string{"d", "b", "c", "a"}) || len(p.Diagnostics) > 0 {
		t.Fatalf("plan = %+v", p)
	}
	if !reflect.DeepEqual(p.Recipes[0].RequiredBy, []string{"b", "c"}) {
		t.Fatalf("inclusion reasons = %+v", p.Recipes[0])
	}
	data["d"] = "# requires: a\nguest-ping\n"
	if p := planVZScripts([]string{"a"}, "", load); len(p.Diagnostics) == 0 || p.Diagnostics[0].Class != "dependency" {
		t.Fatalf("cycle accepted: %+v", p)
	}
	delete(data, "d")
	if p := planVZScripts([]string{"a", "d", "d"}, "", load); len(p.Diagnostics) == 0 {
		t.Fatal("missing dependency accepted")
	}
}

func TestVZScriptPlanNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "must-not-exist")
	source := fmt.Sprintf("mkdir '%s'\nguest-exec /bin/touch '%s'\n[screen:desktop] ocr-click Continue\n[!root] guest-cp /missing /target\n", target, target)
	p := planVZScripts([]string{"test"}, "", func(string) ([]byte, error) { return []byte(source), nil })
	if len(p.Diagnostics) > 0 || len(p.Recipes) != 1 || !p.Recipes[0].NeedsUI {
		t.Fatalf("plan = %+v", p)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("planner mutated host: %v", err)
	}
}

func TestVZScriptPlanCLI(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "local.vzscript")
	if err := os.WriteFile(name, []byte("# guest-os: linux\n# runs-on: daemon\nenv SECRET=do-not-disclose\nguest-exec $SECRET\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"plan", "validate", "explain"} {
		var out bytes.Buffer
		if err := vzscriptPlanCommand(mode, []string{"-json", "-os", "linux", name}, &out); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "do-not-disclose") || strings.Contains(out.String(), "SECRET") {
			t.Fatalf("inputs disclosed: %s", out.String())
		}
		if !strings.Contains(out.String(), `"route": "daemon"`) {
			t.Fatalf("missing route: %s", out.String())
		}
	}
	var out bytes.Buffer
	if err := vzscriptPlanCommand("validate", []string{"-os", "windows", name}, &out); err == nil {
		t.Fatal("OS mismatch accepted")
	}
}

func TestVZScriptPlanBuiltinCatalog(t *testing.T) {
	p := planVZScripts([]string{"golang"}, "darwin", loadVZScriptData)
	if len(p.Diagnostics) > 0 || len(p.Recipes) == 0 {
		t.Fatalf("builtin plan = %+v", p)
	}
}
