package main

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"

	"rsc.io/script"
)

func TestUIScriptEngineCommands(t *testing.T) {
	// Verify all expected commands are registered.
	cfg := uiscriptConfig{}
	engine := newUIScriptEngine(cfg)

	wantCmds := []string{
		"screenshot", "ocr", "ocr-click", "ocr-wait", "ocr-gone",
		"type", "key", "click", "wait", "detect-page", "detect-screen",
		"echo", "cat", "env", "sleep", "stdout", "stderr", "stop", "help",
	}
	for _, name := range wantCmds {
		if _, ok := engine.Cmds[name]; !ok {
			t.Errorf("missing command: %s", name)
		}
	}
}

func TestUIScriptEngineConditions(t *testing.T) {
	cfg := uiscriptConfig{}
	engine := newUIScriptEngine(cfg)

	wantConds := []string{"screen", "page", "text-visible"}
	for _, name := range wantConds {
		if _, ok := engine.Conds[name]; !ok {
			t.Errorf("missing condition: %s", name)
		}
	}
}

func TestUIScriptWaitCommand(t *testing.T) {
	cfg := uiscriptConfig{}
	engine := newUIScriptEngine(cfg)

	state, err := script.NewState(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}

	// Execute a simple script with just wait and echo.
	src := `echo hello
wait 10ms
echo done
`
	var log bytes.Buffer
	err = engine.Execute(state, "test.uiscript",
		bufio.NewReader(strings.NewReader(src)), &log)
	if err != nil {
		t.Fatalf("execute: %v\nlog:\n%s", err, log.String())
	}
}

func TestUIScriptEmbeddedScripts(t *testing.T) {
	// Verify we can load each built-in script.
	entries, err := builtinUIScripts.ReadDir("uiscripts")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded uiscripts found")
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".uiscript") {
			continue
		}
		data, err := builtinUIScripts.ReadFile("uiscripts/" + e.Name())
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

func TestLoadUIScriptData(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"builtin by name", "setup-assistant", false},
		{"builtin with ext", "setup-assistant.uiscript", false},
		{"nonexistent", "does-not-exist", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := loadUIScriptData(tt.input)
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
