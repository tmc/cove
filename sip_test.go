package main

import "testing"

func TestGenerateSIPBootCommands_DisableWithPasswordConfirmReboot(t *testing.T) {
	cmds := generateSIPBootCommands("disable", "", "secret", true, true)

	if !hasCommand(cmds, BootCommand{Type: "clickMenuItem", Args: "Utilities|Terminal"}) {
		t.Fatalf("expected clickMenuItem Utilities|Terminal command")
	}
	if !hasCommand(cmds, BootCommand{Type: "type", Args: "csrutil disable"}) {
		t.Fatalf("expected csrutil disable command")
	}

	yIdx := indexOfCommand(cmds, BootCommand{Type: "typeAndReturnIfText", Args: "Are you sure|y"})
	passIdx := indexOfCommand(cmds, BootCommand{Type: "typeAndReturnIfText", Args: "Enter password|secret"})
	if yIdx < 0 {
		t.Fatalf("expected confirm command y")
	}
	if passIdx < 0 {
		t.Fatalf("expected password command")
	}
	if yIdx <= passIdx {
		t.Fatalf("expected y to be sent after password (y=%d pass=%d)", yIdx, passIdx)
	}

	if !hasCommand(cmds, BootCommand{Type: "type", Args: "reboot"}) {
		t.Fatalf("expected reboot command")
	}
}

func TestGenerateSIPBootCommands_DisableWithoutConfirm(t *testing.T) {
	cmds := generateSIPBootCommands("disable", "", "secret", false, true)
	if hasCommand(cmds, BootCommand{Type: "typeAndReturnIfText", Args: "Are you sure|y"}) {
		t.Fatalf("did not expect confirm command when confirm=false")
	}
}

func hasCommand(cmds []BootCommand, want BootCommand) bool {
	return indexOfCommand(cmds, want) >= 0
}

func indexOfCommand(cmds []BootCommand, want BootCommand) int {
	for i, cmd := range cmds {
		if cmd.Type == want.Type && cmd.Args == want.Args {
			return i
		}
	}
	return -1
}
