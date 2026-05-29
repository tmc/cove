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

func TestServeConfigAllowlist(t *testing.T) {
	cfg := ServeConfig{VMList: " dev, ,ci,,builder "}
	got := cfg.Allowlist()
	want := []string{"dev", "ci", "builder"}
	if len(got) != len(want) {
		t.Fatalf("Allowlist() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Allowlist() = %#v, want %#v", got, want)
		}
	}
}

func TestServeConfigListenAddr(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ServeConfig
		want    string
		wantErr bool
	}{
		{name: "http", cfg: ServeConfig{HTTPAddr: ":7777"}, want: ":7777"},
		{name: "tcp listen url", cfg: ServeConfig{HTTPAddr: ":7777", ListenURL: "tcp://127.0.0.1:8888"}, want: "127.0.0.1:8888"},
		{name: "unix listen url", cfg: ServeConfig{ListenURL: "unix:///tmp/cove.sock"}, want: "/tmp/cove.sock"},
		{name: "bad listen url", cfg: ServeConfig{ListenURL: "http://127.0.0.1:8888"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cfg.ListenAddr()
			if tt.wantErr {
				if err == nil {
					t.Fatal("ListenAddr() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ListenAddr(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("ListenAddr() = %q, want %q", got, tt.want)
			}
		})
	}
}
