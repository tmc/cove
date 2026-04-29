package main

import "testing"

func TestParseUpFlagsLinuxRosettaDefault(t *testing.T) {
	cfg, err := parseUpFlags([]string{"-linux", "-headless"})
	if err != nil {
		t.Fatalf("parseUpFlags() error = %v", err)
	}
	if !cfg.rosetta {
		t.Fatal("rosetta = false, want true")
	}
}

func TestParseUpFlagsLinuxRosettaFalse(t *testing.T) {
	cfg, err := parseUpFlags([]string{"-linux", "-headless", "-rosetta=false"})
	if err != nil {
		t.Fatalf("parseUpFlags() error = %v", err)
	}
	if cfg.rosetta {
		t.Fatal("rosetta = true, want false")
	}
}
