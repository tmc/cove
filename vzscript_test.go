package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
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

func TestRunVZScriptContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runVZScriptContext(ctx, []byte("echo hello\n"), "cancel.vzscript", vzscriptConfig{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runVZScriptContext() = %v, want context.Canceled", err)
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

func TestVZScriptEnvFlag(t *testing.T) {
	var flags envFlag
	if err := flags.Set("COVE_HOMEBREW_ACCEPT_CLT=1"); err != nil {
		t.Fatal(err)
	}
	if err := flags.Set("EMPTY="); err != nil {
		t.Fatal(err)
	}
	if got := flags.String(); got != "COVE_HOMEBREW_ACCEPT_CLT=1,EMPTY=" {
		t.Fatalf("String() = %q", got)
	}
	got := envListToMap(flags)
	if got["COVE_HOMEBREW_ACCEPT_CLT"] != "1" || got["EMPTY"] != "" {
		t.Fatalf("envListToMap = %#v", got)
	}
	if err := flags.Set("not-an-assignment"); err == nil {
		t.Fatal("Set without '=' succeeded")
	}
}

func TestGuestExecInTerminalFallsBackWhenAppleEventsDenied(t *testing.T) {
	oldDetect := detectGuestOSHook
	oldUser := guestConsoleUserHook
	oldAE := guestTerminalAppleEventsAllowedHook
	t.Cleanup(func() {
		detectGuestOSHook = oldDetect
		guestConsoleUserHook = oldUser
		guestTerminalAppleEventsAllowedHook = oldAE
	})
	detectGuestOSHook = func(guestTerminalAgent) (string, error) { return guestOSDarwin, nil }
	guestConsoleUserHook = func(vzscriptConfig) (string, error) { return "tester", nil }
	guestTerminalAppleEventsAllowedHook = func(vzscriptConfig, string) (bool, error) { return false, nil }

	_, err := guestExecInTerminal(vzscriptConfig{}, "/tmp/script.sh")
	if err == nil || !strings.Contains(err.Error(), "Terminal Apple Events permission is not already granted") {
		t.Fatalf("guestExecInTerminal error = %v", err)
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

func TestXcodeVZScriptHostCopyFallback(t *testing.T) {
	data, err := builtinScripts.ReadFile("vzscripts/xcode.vzscript")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)

	for _, app := range []string{
		"/Applications/Xcode.app",
		"/Applications/Xcode-rc.app",
		"/Applications/Xcode-beta.app",
	} {
		line := "? host-cp " + app + " " + app
		if !strings.Contains(script, line) {
			t.Fatalf("xcode recipe missing optional host copy %q", line)
		}
	}

	for _, text := range []string{
		"error: no Xcode found in guest /Applications",
		"cove vzscript run xcode-mas",
		"cove ctl agent-cp /Applications/Xcode.app /Applications/Xcode.app",
	} {
		if !strings.Contains(script, text) {
			t.Fatalf("xcode recipe missing fallback text %q", text)
		}
	}

	if err := executeVZScriptSyntaxOnly(t, "xcode.vzscript", data); err != nil {
		t.Fatal(err)
	}
}

func TestHomebrewVZScriptRefusesImplicitCLT(t *testing.T) {
	data, err := builtinScripts.ReadFile("vzscripts/homebrew.vzscript")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, want := range []string{
		"if ! /usr/bin/xcode-select -p >/dev/null 2>&1; then",
		`[ "${COVE_HOMEBREW_ACCEPT_CLT:-}" != "1" ]`,
		"cove vzscript run -env COVE_HOMEBREW_ACCEPT_CLT=1 homebrew",
		"cove vzscript run xcode-cli homebrew",
		"exit 2",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("homebrew recipe missing %q", want)
		}
	}
	if strings.Index(script, "xcode-select -p") > strings.Index(script, "install.sh") {
		t.Fatal("homebrew recipe checks CLT after invoking install.sh")
	}
	if err := executeVZScriptSyntaxOnly(t, "homebrew.vzscript", data); err != nil {
		t.Fatal(err)
	}
}

func TestXcodeCLIVZScriptRequiresDeveloperTools(t *testing.T) {
	data, err := builtinScripts.ReadFile("vzscripts/xcode-cli.vzscript")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	if !strings.Contains(script, "# requires: developer-tools") {
		t.Fatalf("xcode-cli recipe does not require developer-tools:\n%s", script)
	}
	if err := executeVZScriptSyntaxOnly(t, "xcode-cli.vzscript", data); err != nil {
		t.Fatal(err)
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

func TestVZScriptListRejectsInvalidOS(t *testing.T) {
	err := vzscriptList([]string{"-os", "windows"})
	if err == nil {
		t.Fatal("expected invalid guest OS error")
	}
	if got, want := err.Error(), `invalid guest OS "windows" (use darwin or linux)`; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestVZScriptListLinuxRecipes(t *testing.T) {
	var buf bytes.Buffer
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = vzscriptListWithGuestOS("linux")
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"agentkit-linux-base", "agentkit-linux-claude-ready", "cirrus-migrate-doctor", "kvm-test", "nixos-base"} {
		if !strings.Contains(out, want) {
			t.Fatalf("linux list missing %q:\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if got, want := len(lines)-1, 5; got != want {
		t.Fatalf("linux recipe count = %d, want %d:\n%s", got, want, out)
	}
}

func TestPrintVZScriptUsageIncludesListOSFlag(t *testing.T) {
	var buf bytes.Buffer
	printVzscriptUsage(&buf)
	if !strings.Contains(buf.String(), "list [-os darwin|linux]") {
		t.Fatalf("usage missing list OS flag:\n%s", buf.String())
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

func TestRunVZScriptWithDepsGuestOSRefusal(t *testing.T) {
	err := runVZScriptWithDeps([]string{"homebrew"}, vzscriptConfig{guestOS: "linux"})
	if err == nil {
		t.Fatal("expected guest OS refusal")
	}
	want := "vzscript: recipe 'homebrew' is for darwin guests only; this VM is Linux"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
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

// TestVZScriptListFilterMatrix exercises the guest-os filter predicate of
// vzscriptListWithGuestOS across darwin, linux, and the unfiltered case.
// The linux/invalid cases are covered by sibling tests; this fills the
// darwin and "no filter" arms of the matrix.
func TestVZScriptListFilterMatrix(t *testing.T) {
	tests := []struct {
		name     string
		filter   string
		mustHave []string
		mustOmit []string
	}{
		{
			name:     "darwin filter includes darwin recipes",
			filter:   "darwin",
			mustHave: []string{"homebrew", "sip-enable"},
			mustOmit: []string{"agentkit-linux-base", "nixos-base"},
		},
		{
			name:     "no filter includes both platforms",
			filter:   "",
			mustHave: []string{"homebrew", "agentkit-linux-base"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			old := os.Stdout
			os.Stdout = w
			runErr := vzscriptListWithGuestOS(tt.filter)
			w.Close()
			os.Stdout = old
			if runErr != nil {
				t.Fatal(runErr)
			}
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(r); err != nil {
				t.Fatal(err)
			}
			out := buf.String()
			for _, want := range tt.mustHave {
				if !strings.Contains(out, want) {
					t.Errorf("missing recipe %q in output:\n%s", want, out)
				}
			}
			for _, omit := range tt.mustOmit {
				if strings.Contains(out, omit) {
					t.Errorf("unexpected recipe %q in darwin-filtered output:\n%s", omit, out)
				}
			}
		})
	}
}

// TestVZScriptShowErrors covers the show command error paths: missing
// recipe argument and unknown recipe name.
func TestVZScriptShowErrors(t *testing.T) {
	if err := vzscriptShow(nil); err == nil {
		t.Error("vzscriptShow(nil) = nil, want error for missing recipe name")
	}
	if err := vzscriptShow([]string{"definitely-not-a-recipe"}); err == nil {
		t.Error("vzscriptShow(unknown) = nil, want error for unknown recipe")
	}
}

func TestVZScriptShowHelp(t *testing.T) {
	out, err := captureVZScriptStdout(t, func() error { return vzscriptShow([]string{"-h"}) })
	if err != nil {
		t.Fatalf("vzscriptShow(-h): %v", err)
	}
	for _, want := range []string{"Usage: cove vzscript show", "homebrew", "./custom.vzscript"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help output missing %q:\n%s", want, out)
		}
	}
}

func TestVZScriptRunLocalOnlyMissingVMDoesNotCreateDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = ""
	vmDir = ""
	missing := "missing-vzscript-local-vm"
	recipe := filepath.Join(t.TempDir(), "local.vzscript")
	if err := os.WriteFile(recipe, []byte("echo local-only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vzscriptRun([]string{"-vm", missing, recipe}); err != nil {
		t.Fatalf("vzscriptRun local-only: %v", err)
	}
	dir := filepath.Join(vmconfig.BaseDir(), missing)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("missing VM dir stat = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vzscript.log")); !os.IsNotExist(err) {
		t.Fatalf("vzscript.log stat = %v, want not exist", err)
	}
}

func TestVZScriptRunMissingRecipeGlobalVMDoesNotCreateDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "missing-vzscript-global-vm"
	vmDir = ""
	err := vzscriptRun([]string{"definitely-missing-vzscript-recipe"})
	if err == nil {
		t.Fatal("vzscriptRun missing recipe succeeded; want error")
	}
	dir := filepath.Join(vmconfig.BaseDir(), vmName)
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("missing VM dir stat = %v, want not exist", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "vzscript.log")); !os.IsNotExist(statErr) {
		t.Fatalf("vzscript.log stat = %v, want not exist", statErr)
	}
}

func TestVZScriptRunGuestCommandMissingDefaultDoesNotCreateDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = ""
	vmDir = ""
	recipe := filepath.Join(t.TempDir(), "guest.vzscript")
	if err := os.WriteFile(recipe, []byte("guest-exec echo hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := vzscriptRun([]string{"-timeout", "1s", recipe})
	if err == nil {
		t.Fatal("vzscriptRun guest command on missing default succeeded; want error")
	}
	if !strings.Contains(err.Error(), "vm is not running") && !strings.Contains(err.Error(), "connect to control socket") {
		t.Fatalf("err = %v, want missing control socket diagnostic", err)
	}
	dir := filepath.Join(vmconfig.BaseDir(), "default")
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("default VM dir stat = %v, want not exist", statErr)
	}
}

func TestVZScriptUsageMentionsListVMAndSubcommandHelp(t *testing.T) {
	var buf bytes.Buffer
	printVzscriptUsage(&buf)
	for _, want := range []string{"list [-os darwin|linux] [-vm <name>]", "cove vzscript list -h", "cove vzscript run -h"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("usage missing %q:\n%s", want, buf.String())
		}
	}
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
