package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestUsageIsNormalUserFirst(t *testing.T) {
	stderr, restore := captureStderr(t)
	usage()
	restore()
	out := stderr.String()
	for _, want := range []string{"first-run", "doctor host", "up -user <name>", "support bundle", "help advanced"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "softreset") || strings.Contains(out, "GDB debug stub") {
		t.Fatalf("normal usage includes advanced commands:\n%s", out)
	}
}

func TestHelpAdvancedIncludesFullInventory(t *testing.T) {
	stderr, restore := captureStderr(t)
	usageAdvanced()
	restore()
	out := stderr.String()
	for _, want := range []string{"softreset", "run -gdb :1234", "first-run", "help advanced"} {
		if !strings.Contains(out, want) {
			t.Fatalf("advanced usage missing %q:\n%s", want, out)
		}
	}
}

func TestFirstRunHelp(t *testing.T) {
	var buf bytes.Buffer
	printFirstRunUsage(&buf)
	for _, want := range []string{"cove doctor host", "cove up -user <name>", "prompts for the guest account password", "cove support bundle"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("first-run help missing %q:\n%s", want, buf.String())
		}
	}
}

func TestFirstRunCommand(t *testing.T) {
	out, err := captureStdoutResult(t, func() error {
		handled, code := handleEarlyCLI([]string{"first-run"})
		if !handled || code != 0 {
			t.Fatalf("handleEarlyCLI(first-run) = %v, %d", handled, code)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cove doctor host") || !strings.Contains(out, "cove up -user <name>") {
		t.Fatalf("first-run command output:\n%s", out)
	}
}

func TestSupportBundleAliasHelp(t *testing.T) {
	stderr, restore := captureStderr(t)
	handled, code := handleEarlyCLI([]string{"support-bundle", "-h"})
	restore()
	if !handled || code != 0 {
		t.Fatalf("handleEarlyCLI(support-bundle -h) = %v, %d", handled, code)
	}
	for _, want := range []string{"Usage: cove support bundle", "-vm NAME", "-out PATH"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("support-bundle help missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestUnknownCommandGuidance(t *testing.T) {
	var buf bytes.Buffer
	writeUnknownCommand(&buf, "xyzzy")
	for _, want := range []string{`unknown command "xyzzy"`, "cove first-run", "cove help advanced"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("unknown command output missing %q:\n%s", want, buf.String())
		}
	}

	buf.Reset()
	writeUnknownCommand(&buf, "runn")
	for _, want := range []string{`Did you mean "run"?`, "cove run -h"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("unknown command suggestion missing %q:\n%s", want, buf.String())
		}
	}
}

func TestVMNotFoundHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := requireExistingVMDir("status", "missing")
	if err == nil {
		t.Fatal("requireExistingVMDir succeeded, want error")
	}
	for _, want := range []string{`no VM named "missing"`, "cove list", "cove up -user <name>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("missing VM error lacks %q:\n%s", want, err)
		}
	}
}

func TestDeleteVMNotFoundHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := DeleteVMWithOptions("missing", DeleteVMOptions{})
	if !errors.Is(err, ErrVMNotFound) {
		t.Fatalf("DeleteVMWithOptions error = %v, want ErrVMNotFound", err)
	}
	for _, want := range []string{"vm not found: missing", "cove list", "cove up -user <name>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("delete missing error lacks %q:\n%s", want, err)
		}
	}
}
