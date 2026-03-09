package main

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

	"rsc.io/script"
)

func TestVZScriptEngineCommands(t *testing.T) {
	cfg := vzscriptConfig{}
	engine := newVZScriptEngine(cfg)

	wantCmds := []string{
		// Guest commands.
		"guest-wait", "guest-ping", "guest-exec", "guest-shell",
		"guest-terminal", "guest-write", "guest-read", "guest-cp",
		// UI automation commands.
		"screenshot", "ocr", "ocr-click", "ocr-wait", "ocr-gone",
		"type", "key", "click", "wait", "detect-page", "detect-screen",
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
	entries, err := builtinScripts.ReadDir("vzscripts")
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
		}
		t.Logf("loaded %s (%d bytes)", e.Name(), len(data))
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
