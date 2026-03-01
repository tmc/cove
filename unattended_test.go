package main

import "testing"

func TestLoadPresetCommands(t *testing.T) {
	commands, err := loadPresetCommands()
	if err != nil {
		t.Fatalf("loadPresetCommands: %v", err)
	}
	if len(commands) == 0 {
		t.Fatal("expected non-empty preset commands")
	}

	// Verify first command is a wait (both presets start with <wait 30s>)
	if commands[0].Type != "wait" {
		t.Errorf("first command type = %q, want %q", commands[0].Type, "wait")
	}

	// Verify presets contain expected OCR commands
	var hasWaitForText, hasKey bool
	for _, cmd := range commands {
		switch cmd.Type {
		case "waitForText":
			hasWaitForText = true
		case "key":
			hasKey = true
		}
	}
	if !hasWaitForText {
		t.Error("preset missing waitForText commands")
	}
	if !hasKey {
		t.Error("preset missing key commands")
	}
}

func TestLoadPresetCommands_ParsesAllPresets(t *testing.T) {
	// Read all preset files and verify they parse
	entries, err := presetFS.ReadDir("presets")
	if err != nil {
		t.Fatalf("readdir presets: %v", err)
	}

	for _, entry := range entries {
		t.Run(entry.Name(), func(t *testing.T) {
			data, err := presetFS.ReadFile("presets/" + entry.Name())
			if err != nil {
				t.Fatalf("read %s: %v", entry.Name(), err)
			}
			commands, err := ParseBootCommands(string(data))
			if err != nil {
				t.Fatalf("parse %s: %v", entry.Name(), err)
			}
			if len(commands) == 0 {
				t.Errorf("%s: no commands parsed", entry.Name())
			}
			t.Logf("%s: %d commands", entry.Name(), len(commands))
		})
	}
}
