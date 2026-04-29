package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/txtar"
	"rsc.io/script"
)

func TestVZScriptEngineCommands(t *testing.T) {
	cfg := vzscriptConfig{}
	engine := newVZScriptEngine(cfg)

	wantCmds := []string{
		// Guest commands.
		"guest-wait", "guest-ping", "guest-exec", "guest-shell",
		"guest-terminal", "guest-write", "guest-read", "guest-cp", "host-cp",
		// UI automation commands.
		"screenshot", "ocr", "ocr-click", "ocr-wait", "ocr-gone",
		"wait-menu-text", "click-menu-item", "reboot-to-recovery",
		"recovery-options", "startup-options", "recovery-continue",
		"label-push", "label-pop", "label-clear", "answer-visible", "wait-prompt-clear",
		"type", "type-keycodes", "key", "click", "wait", "detect-page", "detect-screen",
		// Standard commands.
		"echo", "cat", "cp", "env", "exists", "sleep", "stdout", "stderr",
		"stop", "help", "mkdir",
	}
	for _, name := range wantCmds {
		if _, ok := engine.Cmds[name]; !ok {
			t.Errorf("missing command: %s", name)
		}
	}
}

func TestVZScriptLabelsLogAndPop(t *testing.T) {
	var log bytes.Buffer
	cfg := vzscriptConfig{
		verbose:   true,
		logWriter: &log,
	}
	src := []byte("label-push install\nlabel-push \"quoted label\"\nlabel-push recovery\nlabel-pop\nlabel-clear\n")
	if err := runVZScript(src, "labels.vzscript", cfg); err != nil {
		t.Fatal(err)
	}
	got := log.String()
	want := []string{
		`label-push "install" -> "install"`,
		`label-push "quoted label" -> "install / quoted label"`,
		`label-push "recovery" -> "install / quoted label / recovery"`,
		`label-pop -> "install / quoted label"`,
		`label-clear`,
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Fatalf("log missing %q\n%s", w, got)
		}
	}
}

func TestParseAnswerVisibleSkipEmpty(t *testing.T) {
	got, err := parseAnswerVisibleArgs([]string{
		"-optional",
		"-skip-empty",
		"-delay", "500ms",
		"Authorized user", "",
		"Password", "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.pairs) != 1 {
		t.Fatalf("pairs = %d, want 1", len(got.pairs))
	}
	if got.pairs[0] != (promptAnswer{prompt: "Password", answer: "secret"}) {
		t.Fatalf("pair = %#v", got.pairs[0])
	}
	if got.delay != 500*time.Millisecond {
		t.Fatalf("delay = %v, want 500ms", got.delay)
	}

	got, err = parseAnswerVisibleArgs([]string{"-optional", "-skip-empty", "Password", ""})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.pairs) != 0 {
		t.Fatalf("pairs = %d, want 0", len(got.pairs))
	}
}

func TestVZScriptEngineConditions(t *testing.T) {
	cfg := vzscriptConfig{}
	engine := newVZScriptEngine(cfg)

	wantConds := []string{"screen", "page", "text-visible"}
	for _, name := range wantConds {
		if _, ok := engine.Conds[name]; !ok {
			t.Errorf("missing condition: %s", name)
		}
	}
}

func TestVZScriptWaitCommand(t *testing.T) {
	cfg := vzscriptConfig{}
	engine := newVZScriptEngine(cfg)

	state, err := script.NewState(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	src := `echo hello
wait 10ms
echo done
`
	var log bytes.Buffer
	err = engine.Execute(state, "test.vzscript",
		bufio.NewReader(strings.NewReader(src)), &log)
	if err != nil {
		t.Fatalf("execute: %v\nlog:\n%s", err, log.String())
	}
}

func TestVZScriptEmbeddedScripts(t *testing.T) {
	entries, err := fs.ReadDir(builtinScripts, "vzscripts")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded vzscripts found")
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".vzscript") {
			continue
		}
		data, err := builtinScripts.ReadFile("vzscripts/" + e.Name())
		if err != nil {
			t.Errorf("read %s: %v", e.Name(), err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", e.Name())
			continue
		}
		if err := executeVZScriptSyntaxOnly(t, e.Name(), data); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
	}
}

func TestLoadVZScriptData(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"builtin by name", "setup-assistant", false},
		{"builtin with ext", "setup-assistant.vzscript", false},
		{"builtin homebrew", "homebrew", false},
		{"builtin sip enable", "sip-enable", false},
		{"builtin sip disable", "sip-disable.vzscript", false},
		{"nonexistent", "does-not-exist", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := loadVZScriptData(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(data) == 0 {
				t.Error("empty data")
			}
		})
	}
}

func TestRunVZScriptTemplate(t *testing.T) {
	var log bytes.Buffer
	cfg := vzscriptConfig{
		verbose:      true,
		logWriter:    &log,
		template:     true,
		templateVars: map[string]any{"Word": "hello"},
	}
	if err := runVZScript([]byte("echo {{.Word}}\n"), "template.vzscript", cfg); err != nil {
		t.Fatal(err)
	}
	if got := log.String(); !strings.Contains(got, "hello") {
		t.Fatalf("log missing rendered output:\n%s", got)
	}
}

func TestRenderVZScriptTemplateFuncs(t *testing.T) {
	got, err := renderVZScriptTemplate(
		[]byte("type-keycodes {{quote .Command}}\n[text-visible:{{queryescape .Success}}] screenshot\n"),
		"funcs.vzscript.tmpl",
		map[string]any{
			"Command": "csrutil disable",
			"Success": "System Integrity Protection is off.",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "type-keycodes 'csrutil disable'\n[text-visible:System+Integrity+Protection+is+off.] screenshot\n"
	if string(got) != want {
		t.Fatalf("rendered template = %q, want %q", got, want)
	}
}

func TestGenerateSIPVZScript_Syntax(t *testing.T) {
	script, err := generateSIPVZScript("disable", "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(script)
	if err := executeVZScriptSyntaxOnly(t, "sip-disable.vzscript", data); err != nil {
		t.Fatal(err)
	}
}

func TestVZScriptSyntax_RebootToRecoveryFlow(t *testing.T) {
	data := []byte(`# normal runtime can reboot to Recovery before Recovery UI steps
reboot-to-recovery 2m
recovery-options 180s
recovery-continue 240s
wait-menu-text Utilities 180s
`)
	if err := executeVZScriptSyntaxOnly(t, "reboot-recovery.vzscript", data); err != nil {
		t.Fatal(err)
	}
}

func TestVZScriptSyntax_EncodedConditionSuffix(t *testing.T) {
	data := []byte(`# encoded condition suffixes with spaces and punctuation
[text-visible:Authorized+user] echo ok
[text-visible:%5By%2Fn%5D] echo ok
`)
	if err := executeVZScriptSyntaxOnly(t, "encoded-conds.vzscript", data); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeVisibleText(t *testing.T) {
	got := normalizeVisibleText("System Integrity Protection is\noff.\n")
	want := "system integrity protection is off."
	if got != want {
		t.Fatalf("normalizeVisibleText() = %q, want %q", got, want)
	}
}

func TestRunVZScriptWithDeps_LocalScripts(t *testing.T) {
	dir := t.TempDir()
	dep := filepath.Join(dir, "dep.vzscript")
	root := filepath.Join(dir, "root.vzscript")

	if err := os.WriteFile(dep, []byte("# dependency\necho dep-output\n"), 0644); err != nil {
		t.Fatalf("write dep: %v", err)
	}
	rootBody := fmt.Sprintf("# requires: %s\necho root-output\n", dep)
	if err := os.WriteFile(root, []byte(rootBody), 0644); err != nil {
		t.Fatalf("write root: %v", err)
	}

	var log bytes.Buffer
	cfg := vzscriptConfig{
		verbose:   true,
		logWriter: &log,
		streamOut: &log,
		streamErr: &log,
	}
	if err := runVZScriptWithDeps([]string{root}, cfg); err != nil {
		t.Fatalf("runVZScriptWithDeps: %v", err)
	}

	out := log.String()
	depAt := strings.Index(out, "dep-output")
	rootAt := strings.Index(out, "root-output")
	if depAt < 0 || rootAt < 0 {
		t.Fatalf("expected both dependency and root output in log:\n%s", out)
	}
	if depAt > rootAt {
		t.Fatalf("dependency ran after root:\n%s", out)
	}
}

func executeVZScriptSyntaxOnly(t *testing.T, name string, data []byte) error {
	t.Helper()

	ar := txtar.Parse(data)
	files := make(map[string]bool, len(ar.Files))
	for _, f := range ar.Files {
		files[f.Name] = true
	}

	state, err := script.NewState(context.Background(), t.TempDir(), nil)
	if err != nil {
		return err
	}
	var log bytes.Buffer
	engine := newVZScriptSyntaxEngine(files)
	if err := engine.Execute(state, name, bufio.NewReader(bytes.NewReader(ar.Comment)), &log); err != nil {
		return fmt.Errorf("syntax execute: %w\nlog:\n%s", err, log.String())
	}
	return nil
}

func newVZScriptSyntaxEngine(files map[string]bool) *script.Engine {
	base := newVZScriptEngine(vzscriptConfig{})

	cmds := make(map[string]script.Cmd, len(base.Cmds))
	for name, cmd := range base.Cmds {
		usage := *cmd.Usage()
		name := name
		cmds[name] = script.Command(usage, func(s *script.State, args ...string) (script.WaitFunc, error) {
			if err := validateVZScriptStubCommand(name, files, args); err != nil {
				return nil, err
			}
			return nil, nil
		})
	}

	conds := make(map[string]script.Cond, len(base.Conds))
	for name, cond := range base.Conds {
		usage := cond.Usage()
		if usage.Prefix {
			conds[name] = script.PrefixCondition(usage.Summary, func(*script.State, string) (bool, error) {
				return true, nil
			})
			continue
		}
		conds[name] = script.BoolCondition(usage.Summary, true)
	}

	return &script.Engine{Cmds: cmds, Conds: conds}
}

func validateVZScriptStubCommand(name string, files map[string]bool, args []string) error {
	switch name {
	case "guest-shell", "guest-terminal":
		if len(args) != 1 {
			return nil
		}
		return requireArchiveFile(files, args[0])
	case "guest-write":
		if len(args) != 2 {
			return nil
		}
		return requireArchiveFile(files, args[1])
	}
	return nil
}

func requireArchiveFile(files map[string]bool, path string) error {
	if filepath.IsAbs(path) {
		return nil
	}
	if files[path] {
		return nil
	}
	return fmt.Errorf("referenced archive file %q not found", path)
}
