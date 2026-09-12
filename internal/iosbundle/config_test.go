package iosbundle

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

func ExampleDefaultConfig() {
	c := DefaultConfig()
	fmt.Println(c.Profile, c.Variant)
	fmt.Println(c.Validate())
	// Output:
	// vresearch101 regular
	// <nil>
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
		want   bool
	}{
		{"default", func(c *Config) {}, true},
		{"less", func(c *Config) { c.Variant = "less"; c.NoBinpack = true }, true},
		{"runtime without daemon", func(c *Config) { c.NoVphoned = true }, true},
		{"fractional scale", func(c *Config) { c.Display.Scale = 2.5 }, true},
		{"schema", func(c *Config) { c.SchemaVersion = 2 }, false},
		{"profile", func(c *Config) { c.Profile = "mac" }, false},
		{"variant", func(c *Config) { c.Variant = "typo" }, false},
		{"binpack", func(c *Config) { c.NoBinpack = true }, false},
		{"geometry", func(c *Config) { c.Display.Width = 0 }, false},
		{"nonfinite scale", func(c *Config) { c.Display.Scale = math.Inf(1) }, false},
		{"multicast", func(c *Config) { c.MAC = "01:00:00:00:00:01" }, false},
		{"unicast", func(c *Config) { c.MAC = "02:00:00:00:00:01" }, true},
		{"bridge", func(c *Config) { c.Network = "bridged:en0" }, true},
		{"missing bridge", func(c *Config) { c.Network = "bridged:" }, false},
		{"bridge carriage return", func(c *Config) { c.Network = "bridged:en0\r" }, false},
		{"bridge nul", func(c *Config) { c.Network = "bridged:en0\x00" }, false},
		{"bridge colon", func(c *Config) { c.Network = "bridged:en0:extra" }, false},
		{"relative rom", func(c *Config) { c.ROM = "firmware/boot.bin" }, true},
		{"absolute rom", func(c *Config) { c.ROM = "/tmp/boot.bin" }, false},
		{"escaping rom", func(c *Config) { c.ROM = "../boot.bin" }, false},
		{"unclean rom", func(c *Config) { c.ROM = "firmware/../boot.bin" }, false},
		{"directory rom", func(c *Config) { c.ROM = "." }, false},
		{"windows rom", func(c *Config) { c.ROM = "C:/boot.bin" }, false},
		{"backslash rom", func(c *Config) { c.ROM = "firmware\\boot.bin" }, false},
		{"nul rom", func(c *Config) { c.ROM = "boot\x00.bin" }, false},
		{"escaping sep rom", func(c *Config) { c.SEPROM = "../sep.bin" }, false},
		{"bad digest", func(c *Config) { c.FirmwareDigest = "abc" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultConfig()
			tt.change(&c)
			if err := c.Validate(); (err == nil) != tt.want {
				t.Fatalf("Validate() = %v, want valid %v", err, tt.want)
			}
		})
	}
}

func TestConfigRoundTrip(t *testing.T) {
	c := DefaultConfig()
	c.NoVphoned = true
	c.ROM = "firmware/AVPBooter.bin"
	c.SEPROM = "firmware/SEP.bin"
	c.BootArgs = "-v"
	c.Display.Scale = 2.5
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != c {
		t.Fatalf("round trip = %+v, want %+v", got, c)
	}
}
