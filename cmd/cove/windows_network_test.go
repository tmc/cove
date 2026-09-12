package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmrun"
)

func windowsNetworkTestEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"COVE_QEMU_AGENT_HOST_PORT", "COVE_QEMU_AGENT_GUEST_PORT", "COVE_QEMU_USER_AGENT_HOST_PORT", "COVE_QEMU_USER_AGENT_GUEST_PORT", "COVE_QEMU_AGENT_FORWARD", "COVE_QEMU_USER_AGENT_FORWARD", "COVE_QEMU_SMB_DIR"} {
		t.Setenv(name, "")
	}
	saved := windowsNetworkFlags
	windowsNetworkFlags = windowsNetworkOverrides{}
	t.Cleanup(func() { windowsNetworkFlags = saved })
}

// These fixtures exercise configuration and serialization, not QEMU or Windows.
func TestWindowsNetworkInstallRestart(t *testing.T) {
	windowsNetworkTestEnv(t)
	dir := t.TempDir()
	tool := filepath.Join(dir, "fake-qemu")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho mock-qemu\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"COVE_QEMU_SYSTEM_AARCH64", "COVE_QEMU_IMG", "COVE_QEMU_EFI_CODE", "COVE_QEMU_EFI_VARS_TEMPLATE"} {
		t.Setenv(name, tool)
	}
	savedVNC, savedShare := vncAddress, windowsSharedDirFlag
	vncAddress, windowsSharedDirFlag = "", ""
	t.Cleanup(func() { vncAddress, windowsSharedDirFlag = savedVNC, savedShare })
	t.Setenv("COVE_QEMU_AGENT_HOST_PORT", "32024")
	t.Setenv("COVE_QEMU_AGENT_GUEST_PORT", "2024")
	t.Setenv("COVE_QEMU_USER_AGENT_HOST_PORT", "32025")
	t.Setenv("COVE_QEMU_USER_AGENT_GUEST_PORT", "2025")
	rc, hc := vmrun.RunConfig{NetworkMode: "nat", CPUCount: 2, MemoryGB: 4}, vmrun.HostConfig{VMDir: dir}
	install, err := windowsQEMUConfigFromRun(rc, hc, true)
	if err != nil {
		t.Fatal(err)
	}
	prov, err := windowsQEMUProvisionConfigFromFlags("mock-agent.exe", install.AgentGuestPort, install.UserAgentGuestPort)
	if err != nil {
		t.Fatal(err)
	}
	if prov.AgentTCPPort != 2024 || prov.AgentUserTCPPort != 2025 {
		t.Fatalf("provisioned ports = %d/%d", prov.AgentTCPPort, prov.AgentUserTCPPort)
	}
	args, err := windowsQEMUNetworkArgs(install)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeWindowsQEMUMetadata(filepath.Join(dir, "qemu", "metadata.json"), install, args); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"COVE_QEMU_AGENT_HOST_PORT", "COVE_QEMU_AGENT_GUEST_PORT", "COVE_QEMU_USER_AGENT_HOST_PORT", "COVE_QEMU_USER_AGENT_GUEST_PORT"} {
		t.Setenv(name, "")
	}
	restart, err := windowsQEMUConfigFromRun(rc, hc, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := windowsQEMUNetworkArgs(restart)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != strings.Join(args, " ") {
		t.Fatalf("restart network = %v, installed %v", got, args)
	}
	t.Setenv("COVE_QEMU_AGENT_HOST_PORT", "33024")
	t.Setenv("COVE_QEMU_AGENT_GUEST_PORT", "3024")
	windowsNetworkFlags = windowsNetworkOverrides{AgentHostPort: "34024", AgentGuestPort: "4024"}
	restart, err = windowsQEMUConfigFromRun(rc, hc, false)
	if err != nil {
		t.Fatal(err)
	}
	if restart.AgentHostPort != 34024 || restart.AgentGuestPort != 4024 || restart.UserAgentHostPort != 32025 || restart.UserAgentGuestPort != 2025 {
		t.Fatalf("override ports = %+v", restart.NetworkSettings)
	}
	windowsNetworkFlags = windowsNetworkOverrides{}
	restart, err = windowsQEMUConfigFromRun(rc, hc, false)
	if err != nil {
		t.Fatal(err)
	}
	if restart.AgentHostPort != 33024 || restart.AgentGuestPort != 3024 {
		t.Fatalf("environment ports = %+v", restart.NetworkSettings)
	}
}

func TestWindowsNetworkDisabledRestart(t *testing.T) {
	windowsNetworkTestEnv(t)
	for _, network := range []string{"none", "nat"} {
		t.Run(network, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "qemu"), 0700); err != nil {
				t.Fatal(err)
			}
			s := defaultWindowsNetworkSettings()
			s.NetworkMode = network
			s.AgentForward = false
			s.AgentGuestPort, s.UserAgentGuestPort = 2024, 2025
			if err := saveWindowsNetworkSettings(dir, s); err != nil {
				t.Fatal(err)
			}
			saved, err := loadWindowsNetworkSettings(dir)
			if err != nil {
				t.Fatal(err)
			}
			got, err := resolveWindowsNetworkSettings(saved, "nat", windowsNetworkOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			if got != s {
				t.Fatalf("restored %+v, want %+v", got, s)
			}
			var cfg windowsQEMUConfig
			if err := applyWindowsNetworkSettings(&cfg, got); err != nil {
				t.Fatal(err)
			}
			if cfg.AgentHostPort != 0 || cfg.UserAgentHostPort != 0 || cfg.AgentGuestPort != 2024 || cfg.UserAgentGuestPort != 2025 {
				t.Fatalf("disabled configuration = %+v", cfg)
			}
			got, err = resolveWindowsNetworkSettings(saved, "nat", windowsNetworkOverrides{NetworkSet: true, AgentForward: "true"})
			if err != nil {
				t.Fatal(err)
			}
			cfg = windowsQEMUConfig{}
			if err := applyWindowsNetworkSettings(&cfg, got); err != nil {
				t.Fatal(err)
			}
			if cfg.NetworkMode != "nat" || cfg.AgentHostPort == 0 || cfg.UserAgentHostPort == 0 || cfg.AgentGuestPort != 2024 || cfg.UserAgentGuestPort != 2025 {
				t.Fatalf("enabled configuration = %+v", cfg)
			}
		})
	}
}

func TestWindowsNetworkInvalidSettings(t *testing.T) {
	windowsNetworkTestEnv(t)
	for _, tt := range []struct {
		name      string
		overrides windowsNetworkOverrides
		want      string
	}{
		{"negative", windowsNetworkOverrides{AgentHostPort: "-1"}, "0 to 65535"},
		{"high", windowsNetworkOverrides{UserAgentHostPort: "65536"}, "0 to 65535"},
		{"guest zero", windowsNetworkOverrides{AgentGuestPort: "0"}, "1 to 65535"},
		{"text", windowsNetworkOverrides{UserAgentGuestPort: "no"}, "1 to 65535"},
		{"duplicate host", windowsNetworkOverrides{AgentHostPort: "3000", UserAgentHostPort: "3000"}, "different host ports"},
		{"duplicate guest", windowsNetworkOverrides{AgentGuestPort: "1025"}, "different guest ports"},
		{"boolean", windowsNetworkOverrides{AgentForward: "perhaps"}, "true or false"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveWindowsNetworkSettings(defaultWindowsNetworkSettings(), "nat", tt.overrides)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
	t.Setenv("COVE_QEMU_AGENT_GUEST_PORT", "65536")
	if _, err := resolveWindowsNetworkSettings(defaultWindowsNetworkSettings(), "nat", windowsNetworkOverrides{}); err == nil {
		t.Fatal("accepted invalid environment port")
	}
}

func TestWindowsNetworkLegacySettings(t *testing.T) {
	windowsNetworkTestEnv(t)
	for _, tt := range []struct {
		name, data  string
		network     string
		host, guest int
		forward     bool
	}{
		{"missing", "", "nat", 0, 1024, true},
		{"old empty", "{}", "nat", 0, 1024, true},
		{"ports", `{"agentHostPort":32024,"agentGuestPort":2024,"args":["-netdev","user,id=net0"]}`, "nat", 32024, 2024, true},
		{"disabled", `{"args":["-netdev","user,id=net0"]}`, "nat", 0, 1024, false},
		{"none", `{"args":["-machine","virt"]}`, "none", 0, 1024, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "qemu"), 0700); err != nil {
				t.Fatal(err)
			}
			if tt.data != "" {
				if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), []byte(tt.data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := loadWindowsNetworkSettings(dir)
			if err != nil {
				t.Fatal(err)
			}
			s, err = resolveWindowsNetworkSettings(s, "nat", windowsNetworkOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			if s.NetworkMode != tt.network || s.AgentHostPort != tt.host || s.AgentGuestPort != tt.guest || s.AgentForward != tt.forward {
				t.Fatalf("legacy = %+v", s)
			}
		})
	}
	for _, file := range []string{"network.json", "metadata.json"} {
		t.Run(file, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "qemu"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "qemu", file), []byte("{broken"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadWindowsNetworkSettings(dir); err == nil {
				t.Fatal("accepted malformed settings")
			}
		})
	}
}

func TestWindowsNetworkUpFlags(t *testing.T) {
	windowsNetworkTestEnv(t)
	for _, alias := range []string{"network", "net"} {
		t.Run(alias, func(t *testing.T) {
			cfg, err := parseUpFlags(commandEnv{Stderr: io.Discard}, []string{"-windows", "-user", "testuser", "-password", "testpassword", "-" + alias, "nat", "-windows-agent-host-port", "32024", "-windows-agent-guest-port", "2024", "-windows-user-agent-host-port", "32025", "-windows-user-agent-guest-port", "2025", "-windows-agent-forward", "true", "-windows-user-agent-forward", "false"})
			if err != nil {
				t.Fatal(err)
			}
			opts := runtimeOptionsForUp(cfg)
			if !opts.WindowsNetwork.NetworkSet || opts.WindowsNetwork.AgentHostPort != "32024" || opts.WindowsNetwork.AgentGuestPort != "2024" || opts.WindowsNetwork.UserAgentHostPort != "32025" || opts.WindowsNetwork.UserAgentGuestPort != "2025" || opts.WindowsNetwork.AgentForward != "true" || opts.WindowsNetwork.UserAgentForward != "false" {
				t.Fatalf("up options = %+v", opts.WindowsNetwork)
			}
			saved := defaultWindowsNetworkSettings()
			saved.NetworkMode = "none"
			got, err := resolveWindowsNetworkSettings(saved, opts.NetworkMode, opts.WindowsNetwork)
			if err != nil {
				t.Fatal(err)
			}
			if got.NetworkMode != "nat" {
				t.Fatalf("network = %q", got.NetworkMode)
			}
		})
	}
	fs := flag.NewFlagSet("windows", flag.ContinueOnError)
	var overrides windowsNetworkOverrides
	registerWindowsNetworkFlags(fs, &overrides)
	if err := fs.Parse([]string{"-windows-agent-host-port=0"}); err != nil {
		t.Fatal(err)
	}
	if overrides.AgentHostPort != "0" {
		t.Fatalf("explicit zero = %q", overrides.AgentHostPort)
	}
}

func TestWindowsNetworkExplicitEphemeralPort(t *testing.T) {
	windowsNetworkTestEnv(t)
	saved := defaultWindowsNetworkSettings()
	saved.AgentHostPort = 32024
	saved.AgentGuestPort = 2024
	got, err := resolveWindowsNetworkSettings(saved, "nat", windowsNetworkOverrides{AgentHostPort: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentHostPort != 0 || got.AgentGuestPort != 2024 {
		t.Fatalf("resolved = %+v", got)
	}
	var cfg windowsQEMUConfig
	if err := applyWindowsNetworkSettings(&cfg, got); err != nil {
		t.Fatal(err)
	}
	if cfg.AgentHostPort == 0 || cfg.UserAgentHostPort == 0 || cfg.AgentHostPort == cfg.UserAgentHostPort {
		t.Fatalf("allocated ports = %d/%d", cfg.AgentHostPort, cfg.UserAgentHostPort)
	}
	if cfg.NetworkSettings.AgentHostPort != 0 {
		t.Fatalf("persisted ephemeral request changed to %d", cfg.NetworkSettings.AgentHostPort)
	}
}
