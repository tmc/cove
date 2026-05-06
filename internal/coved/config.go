package coved

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Daemon DaemonConfig
}

type DaemonConfig struct {
	MetricsAddr string
	UIAddr      string
	Webhook     WebhookConfig
}

type WebhookConfig struct {
	URL    string
	Events []string
}

func DefaultConfig() Config {
	return Config{Daemon: DaemonConfig{
		MetricsAddr: "127.0.0.1:9876",
		UIAddr:      "127.0.0.1:9877",
	}}
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "cove.toml")
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		path = DefaultConfigPath()
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read cove config: %w", err)
	}
	defer f.Close()
	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch section {
		case "daemon":
			switch key {
			case "metrics_addr":
				cfg.Daemon.MetricsAddr = parseTOMLString(value)
			case "ui_addr":
				cfg.Daemon.UIAddr = parseTOMLString(value)
			}
		case "daemon.webhook":
			switch key {
			case "url":
				cfg.Daemon.Webhook.URL = parseTOMLString(value)
			case "events":
				cfg.Daemon.Webhook.Events = parseTOMLStringList(value)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return cfg, fmt.Errorf("read cove config: %w", err)
	}
	return cfg, nil
}

func parseTOMLString(value string) string {
	out, err := strconv.Unquote(strings.TrimSpace(value))
	if err == nil {
		return out
	}
	return strings.Trim(strings.TrimSpace(value), `"`)
}

func parseTOMLStringList(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(value, ",") {
		s := strings.TrimSpace(parseTOMLString(part))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
