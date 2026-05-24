package main

import (
	"strings"
	"testing"
)

func TestNestedVirtualizationUnsupportedMessage(t *testing.T) {
	if !strings.Contains(nestedVirtualizationUnsupportedError, "Run without --nested") {
		t.Fatalf("nested error is not actionable: %q", nestedVirtualizationUnsupportedError)
	}
}

func TestApplyNestedLinuxDefaultsBumpsImplicitCPU(t *testing.T) {
	oldNested, oldExplicit, oldCPU := linuxNested, cpuExplicit, cpuCount
	defer func() {
		linuxNested, cpuExplicit, cpuCount = oldNested, oldExplicit, oldCPU
	}()

	linuxNested = true
	cpuExplicit = false
	cpuCount = 2
	applyNestedLinuxDefaults()
	if cpuCount != 4 {
		t.Fatalf("cpuCount = %d, want 4", cpuCount)
	}

	cpuExplicit = true
	cpuCount = 2
	applyNestedLinuxDefaults()
	if cpuCount != 2 {
		t.Fatalf("explicit cpuCount = %d, want 2", cpuCount)
	}
}

func TestParseUpFlagsNestedImpliesLinuxAndBumpsCPU(t *testing.T) {
	cfg, err := parseUpFlags(commandTestEnv(), []string{"-nested", "-headless"})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if !cfg.linux || !cfg.nested {
		t.Fatalf("linux=%v nested=%v, want both true", cfg.linux, cfg.nested)
	}
	if cfg.cpuCount != 4 {
		t.Fatalf("cpuCount = %d, want 4", cfg.cpuCount)
	}

	cfg, err = parseUpFlags(commandTestEnv(), []string{"-nested", "-cpu", "2", "-headless"})
	if err != nil {
		t.Fatalf("parseUpFlags explicit cpu: %v", err)
	}
	if cfg.cpuCount != 2 {
		t.Fatalf("explicit cpuCount = %d, want 2", cfg.cpuCount)
	}
}

func TestParseUpFlagsNVMeImpliesLinux(t *testing.T) {
	cfg, err := parseUpFlags(commandTestEnv(), []string{"-nvme", "-headless"})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if !cfg.linux || !cfg.nvme {
		t.Fatalf("linux=%v nvme=%v, want both true", cfg.linux, cfg.nvme)
	}
}
