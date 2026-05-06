package coved

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadConfigDefaultsAndOverrides(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.toml")
	cfg, err := LoadConfig(missing)
	if err != nil {
		t.Fatalf("LoadConfig missing: %v", err)
	}
	if cfg.Daemon.MetricsAddr != "127.0.0.1:9876" || cfg.Daemon.UIAddr != "127.0.0.1:9877" {
		t.Fatalf("defaults = %+v", cfg.Daemon)
	}
	path := filepath.Join(t.TempDir(), "cove.toml")
	data := []byte(`
[daemon]
metrics_addr = "127.0.0.1:19876"
ui_addr = "127.0.0.1:19877"

[daemon.webhook]
url = "https://example.com/cove-events"
events = ["lifecycle.policy.stop", "image.gc.run"]
`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Daemon.MetricsAddr != "127.0.0.1:19876" || cfg.Daemon.UIAddr != "127.0.0.1:19877" {
		t.Fatalf("daemon config = %+v", cfg.Daemon)
	}
	if cfg.Daemon.Webhook.URL != "https://example.com/cove-events" {
		t.Fatalf("webhook url = %q", cfg.Daemon.Webhook.URL)
	}
	want := []string{"lifecycle.policy.stop", "image.gc.run"}
	if !reflect.DeepEqual(cfg.Daemon.Webhook.Events, want) {
		t.Fatalf("events = %#v, want %#v", cfg.Daemon.Webhook.Events, want)
	}
}
