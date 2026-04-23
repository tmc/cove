package main

import "testing"

func TestParseServeConfigDefaults(t *testing.T) {
	cfg, err := parseServeConfig(nil)
	if err != nil {
		t.Fatalf("parseServeConfig(): %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:7777" {
		t.Fatalf("HTTPAddr = %q, want default", cfg.HTTPAddr)
	}
	if cfg.ListenURL != "" || cfg.TokenFile != "" || cfg.VMList != "" || cfg.PerVMAuth || cfg.MCPMode {
		t.Fatalf("parseServeConfig() defaults = %#v", cfg)
	}
}

func TestParseServeConfigFlags(t *testing.T) {
	cfg, err := parseServeConfig([]string{
		"-http", ":8888",
		"-listen", "tcp://127.0.0.1:9999",
		"-token-file", "/tmp/token",
		"-per-vm-auth",
		"-vms", "dev,ci",
		"-mcp",
	})
	if err != nil {
		t.Fatalf("parseServeConfig(): %v", err)
	}
	want := ServeConfig{
		HTTPAddr:  ":8888",
		ListenURL: "tcp://127.0.0.1:9999",
		TokenFile: "/tmp/token",
		VMList:    "dev,ci",
		PerVMAuth: true,
		MCPMode:   true,
	}
	if cfg != want {
		t.Fatalf("parseServeConfig() = %#v, want %#v", cfg, want)
	}
}
