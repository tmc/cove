package main

import (
	"strings"
	"testing"
)

func TestParseUpFlagsLinuxRosettaDefault(t *testing.T) {
	cfg, err := parseUpFlags(commandTestEnv(), []string{"-linux", "-headless"})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if !cfg.rosetta {
		t.Fatal("rosetta = false, want true")
	}
}

func TestParseUpFlagsLinuxRosettaFalse(t *testing.T) {
	cfg, err := parseUpFlags(commandTestEnv(), []string{"-linux", "-headless", "-rosetta=false"})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if cfg.rosetta {
		t.Fatal("rosetta = true, want false")
	}
}

func TestParseUpFlagsDiskSync(t *testing.T) {
	cfg, err := parseUpFlags(commandTestEnv(), []string{"-linux", "-headless", "-disk-sync=none"})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if cfg.diskSync != "none" {
		t.Fatalf("diskSync = %q, want none", cfg.diskSync)
	}
}

func TestParseUpFlagsDiskFormat(t *testing.T) {
	cfg, err := parseUpFlags(commandTestEnv(), []string{"-linux", "-headless", "-disk-format=asif"})
	if err != nil {
		t.Fatalf("parseUpFlags: %v", err)
	}
	if cfg.diskImageFormat != "asif" {
		t.Fatalf("diskImageFormat = %q, want asif", cfg.diskImageFormat)
	}
}

func TestParseUpFlagsRejectsBadDiskFormat(t *testing.T) {
	_, err := parseUpFlags(commandTestEnv(), []string{"-linux", "-headless", "-disk-format=qcow2"})
	if err == nil {
		t.Fatal("parseUpFlags returned nil, want invalid disk format")
	}
	if !strings.Contains(err.Error(), "invalid disk image format") {
		t.Fatalf("error = %q, want invalid disk image format", err)
	}
}

func TestParseUpFlagsRejectsRawASIFConflict(t *testing.T) {
	old := rawDisk
	defer func() { rawDisk = old }()

	rawDisk = true
	_, err := parseUpFlags(commandTestEnv(), []string{"-linux", "-headless", "-disk-format=asif"})
	if err == nil {
		t.Fatal("parseUpFlags returned nil, want raw/asif conflict")
	}
	if !strings.Contains(err.Error(), "-raw-disk requires -disk-format raw") {
		t.Fatalf("error = %q, want raw/asif conflict", err)
	}
}
