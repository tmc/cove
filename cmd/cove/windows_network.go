package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type windowsNetworkOverrides struct {
	NetworkSet         bool
	AgentHostPort      string
	AgentGuestPort     string
	UserAgentHostPort  string
	UserAgentGuestPort string
	AgentForward       string
	UserAgentForward   string
}

var windowsNetworkFlags windowsNetworkOverrides

func init() { registerWindowsNetworkFlags(flag.CommandLine, &windowsNetworkFlags) }

func registerWindowsNetworkFlags(fs *flag.FlagSet, v *windowsNetworkOverrides) {
	fs.StringVar(&v.AgentHostPort, "windows-agent-host-port", v.AgentHostPort, "Windows agent host TCP port (0 allocates a free port)")
	fs.StringVar(&v.AgentGuestPort, "windows-agent-guest-port", v.AgentGuestPort, "Windows agent guest TCP port (default 1024)")
	fs.StringVar(&v.UserAgentHostPort, "windows-user-agent-host-port", v.UserAgentHostPort, "Windows user agent host TCP port (0 allocates a free port)")
	fs.StringVar(&v.UserAgentGuestPort, "windows-user-agent-guest-port", v.UserAgentGuestPort, "Windows user agent guest TCP port (default 1025)")
	fs.StringVar(&v.AgentForward, "windows-agent-forward", v.AgentForward, "enable Windows agent TCP forwarding: true or false")
	fs.StringVar(&v.UserAgentForward, "windows-user-agent-forward", v.UserAgentForward, "enable Windows user agent TCP forwarding: true or false")
}

type windowsNetworkSettings struct {
	Version            int    `json:"version"`
	NetworkMode        string `json:"networkMode"`
	AgentForward       bool   `json:"agentForward"`
	UserAgentForward   bool   `json:"userAgentForward"`
	AgentHostPort      int    `json:"agentHostPort"`
	AgentGuestPort     int    `json:"agentGuestPort"`
	UserAgentHostPort  int    `json:"userAgentHostPort"`
	UserAgentGuestPort int    `json:"userAgentGuestPort"`
}

func defaultWindowsNetworkSettings() windowsNetworkSettings {
	return windowsNetworkSettings{Version: 1, NetworkMode: "nat", AgentForward: true, UserAgentForward: true, AgentGuestPort: 1024, UserAgentGuestPort: 1025}
}

func loadWindowsNetworkSettings(dir string) (windowsNetworkSettings, error) {
	s := defaultWindowsNetworkSettings()
	s.NetworkMode = ""
	path := filepath.Join(dir, "qemu", "network.json")
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &s); err != nil {
			return s, fmt.Errorf("read Windows network settings %s: %w", path, err)
		}
		if s.Version != 1 {
			return s, fmt.Errorf("unsupported Windows network settings version %d in %s", s.Version, path)
		}
		return s, nil
	}
	if !os.IsNotExist(err) {
		return s, fmt.Errorf("read Windows network settings: %w", err)
	}
	path = filepath.Join(dir, "qemu", "metadata.json")
	data, err = os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("read legacy Windows network settings: %w", err)
	}
	var m windowsQEMUMetadata
	if err := json.Unmarshal(data, &m); err != nil {
		return s, fmt.Errorf("read legacy Windows network settings %s: %w", path, err)
	}
	if m.NetworkMode != "" {
		s.NetworkMode = m.NetworkMode
	} else if len(m.Args) != 0 {
		s.NetworkMode = "none"
		for _, arg := range m.Args {
			if arg == "-netdev" {
				s.NetworkMode = "nat"
			}
		}
	}
	if m.AgentGuestPort != 0 {
		s.AgentGuestPort = m.AgentGuestPort
	}
	if m.UserAgentGuestPort != 0 {
		s.UserAgentGuestPort = m.UserAgentGuestPort
	}
	s.AgentHostPort, s.UserAgentHostPort = m.AgentHostPort, m.UserAgentHostPort
	if len(m.Args) != 0 && s.NetworkMode != "none" {
		s.AgentForward, s.UserAgentForward = m.AgentHostPort != 0, m.UserAgentHostPort != 0
	}
	return s, nil
}

func resolveWindowsNetworkSettings(s windowsNetworkSettings, network string, v windowsNetworkOverrides) (windowsNetworkSettings, error) {
	if v.NetworkSet || s.NetworkMode == "" {
		s.NetworkMode = network
	}
	s.NetworkMode = strings.ToLower(strings.TrimSpace(s.NetworkMode))
	if s.NetworkMode == "" {
		s.NetworkMode = "nat"
	}
	if s.NetworkMode != "nat" && s.NetworkMode != "none" {
		return s, fmt.Errorf("Windows QEMU network must be nat or none, got %q", s.NetworkMode)
	}
	for _, p := range []struct {
		name, value, env string
		dst              *int
		min              int
	}{
		{"windows-agent-host-port", v.AgentHostPort, "COVE_QEMU_AGENT_HOST_PORT", &s.AgentHostPort, 0},
		{"windows-agent-guest-port", v.AgentGuestPort, "COVE_QEMU_AGENT_GUEST_PORT", &s.AgentGuestPort, 1},
		{"windows-user-agent-host-port", v.UserAgentHostPort, "COVE_QEMU_USER_AGENT_HOST_PORT", &s.UserAgentHostPort, 0},
		{"windows-user-agent-guest-port", v.UserAgentGuestPort, "COVE_QEMU_USER_AGENT_GUEST_PORT", &s.UserAgentGuestPort, 1},
	} {
		value, source := p.value, "-"+p.name
		if value == "" {
			value, source = os.Getenv(p.env), p.env
		}
		if value != "" {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return s, fmt.Errorf("%s must be a TCP port from %d to 65535", source, p.min)
			}
			*p.dst = n
		} else {
			source = "saved " + p.name
		}
		if *p.dst < p.min || *p.dst > 65535 {
			return s, fmt.Errorf("%s must be a TCP port from %d to 65535", source, p.min)
		}
	}
	for _, b := range []struct {
		name, value, env string
		dst              *bool
	}{
		{"windows-agent-forward", v.AgentForward, "COVE_QEMU_AGENT_FORWARD", &s.AgentForward},
		{"windows-user-agent-forward", v.UserAgentForward, "COVE_QEMU_USER_AGENT_FORWARD", &s.UserAgentForward},
	} {
		value, source := b.value, "-"+b.name
		if value == "" {
			value, source = os.Getenv(b.env), b.env
		}
		if value == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			*b.dst = true
		case "0", "false", "no", "off":
			*b.dst = false
		default:
			return s, fmt.Errorf("%s must be true or false", source)
		}
	}
	if s.AgentGuestPort == s.UserAgentGuestPort {
		return s, fmt.Errorf("Windows system and user agents must use different guest ports")
	}
	if s.NetworkMode == "nat" && s.AgentForward && s.UserAgentForward && s.AgentHostPort != 0 && s.AgentHostPort == s.UserAgentHostPort {
		return s, fmt.Errorf("Windows system and user agents must use different host ports")
	}
	return s, nil
}

func applyWindowsNetworkSettings(cfg *windowsQEMUConfig, s windowsNetworkSettings) error {
	cfg.AgentHostAddress, cfg.UserAgentHostAddress = "", ""
	cfg.AgentHostPort, cfg.UserAgentHostPort = 0, 0
	cfg.NetworkSettings = &s
	cfg.NetworkMode = s.NetworkMode
	cfg.AgentGuestPort, cfg.UserAgentGuestPort = s.AgentGuestPort, s.UserAgentGuestPort
	if s.NetworkMode == "none" || !s.AgentForward {
		return nil
	}
	cfg.AgentHostAddress = "127.0.0.1"
	cfg.AgentHostPort = s.AgentHostPort
	var err error
	if cfg.AgentHostPort == 0 {
		for {
			cfg.AgentHostPort, err = pickFreeLocalTCPPort()
			if err != nil {
				return err
			}
			if !s.UserAgentForward || cfg.AgentHostPort != s.UserAgentHostPort {
				break
			}
		}
	}
	if !s.UserAgentForward {
		return nil
	}
	cfg.UserAgentHostAddress = "127.0.0.1"
	cfg.UserAgentHostPort = s.UserAgentHostPort
	if cfg.UserAgentHostPort == 0 {
		for {
			cfg.UserAgentHostPort, err = pickFreeLocalTCPPort()
			if err != nil {
				return err
			}
			if cfg.UserAgentHostPort != cfg.AgentHostPort {
				break
			}
		}
	}
	if cfg.AgentHostPort == cfg.UserAgentHostPort {
		return fmt.Errorf("Windows agent host ports conflict; choose different host ports")
	}
	return nil
}

func saveWindowsNetworkSettings(dir string, s windowsNetworkSettings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := atomicWriteFile(filepath.Join(dir, "qemu", "network.json"), append(data, '\n'), 0600)
	if err != nil {
		if tmp != "" {
			os.Remove(tmp)
		}
		return fmt.Errorf("save Windows network settings: %w", err)
	}
	return nil
}
