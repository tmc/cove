package main

import (
	"os"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/iosbundle"
	"github.com/tmc/cove/internal/vmrun"
)

func TestIOSBackendRejectsUnsupportedInput(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config func() *iosbundle.Config
		serial string
		want   string
	}{
		{name: "missing config", config: func() *iosbundle.Config { return nil }, want: "missing ios configuration"},
		{name: "missing rom", config: func() *iosbundle.Config { c := iosbundle.DefaultConfig(); return &c }, want: "prepared boot rom"},
		{name: "nvram", config: func() *iosbundle.Config { c := iosbundle.DefaultConfig(); c.BootArgs = "debug=1"; return &c }, want: "prepared nvram"},
		{name: "serial file", config: func() *iosbundle.Config { c := iosbundle.DefaultConfig(); c.ROM = "avpbooter.rom"; return &c }, serial: "serial.log", want: "stdout or none"},
		{name: "missing identity", config: func() *iosbundle.Config { c := iosbundle.DefaultConfig(); c.ROM = "avpbooter.rom"; return &c }, want: "read hardware model"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			got, err := buildIOSVMConfiguration(vmrun.RunConfig{SerialOutput: tt.serial}, vmrun.HostConfig{VMDir: dir}, tt.config())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want error containing %q", err, tt.want)
			}
			if got.ID != 0 {
				t.Fatal("returned configuration after failure")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("created state on failed open: %v", entries)
			}
		})
	}
}
