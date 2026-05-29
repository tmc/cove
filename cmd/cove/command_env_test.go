package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func commandTestEnv() commandEnv {
	return commandEnv{
		Stdin:  strings.NewReader(""),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
}

func TestNewCommandEnvCapturesGlobals(t *testing.T) {
	oldVMName, oldVMDir := vmName, vmDir
	oldVerbose, oldFleetName := verbose, fleetName
	oldHeadlessMode, oldGUIMode, oldLinuxMode := headlessMode, guiMode, linuxMode
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
		verbose, fleetName = oldVerbose, oldFleetName
		headlessMode, guiMode, linuxMode = oldHeadlessMode, oldGUIMode, oldLinuxMode
	})

	vmName = "demo"
	vmDir = "/tmp/demo"
	verbose = true
	fleetName = "lab"
	headlessMode = true
	guiMode = false
	linuxMode = true

	env := newCommandEnv()
	if env.Stdin == nil || env.Stdout == nil || env.Stderr == nil || env.Logger == nil {
		t.Fatalf("newCommandEnv returned nil process handles: %#v", env)
	}
	if env.VM.Name != "demo" || env.VM.Dir != "/tmp/demo" {
		t.Fatalf("env.VM = %#v, want demo /tmp/demo", env.VM)
	}
	if !env.Options.Verbose || env.Options.Fleet != "lab" || !env.Options.Headless || env.Options.GUI || !env.Options.Linux {
		t.Fatalf("env.Options = %#v", env.Options)
	}
}

func TestRunRegisteredCommandUsesEnvWriters(t *testing.T) {
	var out bytes.Buffer
	env := commandEnv{
		Stdout: &out,
		Stderr: &bytes.Buffer{},
		Logger: slog.Default(),
	}
	spec, ok := lookupCommand("version")
	if !ok {
		t.Fatal("version command not registered")
	}
	if code := runRegisteredCommand(env, spec, "version", nil); code != 0 {
		t.Fatalf("runRegisteredCommand version exit = %d", code)
	}
	if out.Len() == 0 {
		t.Fatal("version command wrote no output")
	}
}
