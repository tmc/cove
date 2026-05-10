package vmrun

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func validRunConfig(os GuestOS) RunConfig {
	return RunConfig{
		OS:           os,
		CPUCount:     2,
		MemoryGB:     4,
		DiskPath:     "/var/vz/vms/test/disk.img",
		DiskSizeGB:   64,
		NetworkMode:  "nat",
		StartTimeout: 30 * time.Second,
	}
}

func validHostConfig() HostConfig {
	return HostConfig{
		VMDir:          "/var/vz/vms/test",
		VMName:         "test",
		RuntimeProfile: "full",
	}
}

func TestRunConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *RunConfig)
		wantErr string
	}{
		{"valid macos", func(c *RunConfig) { c.OS = GuestMacOS }, ""},
		{"valid linux", func(c *RunConfig) { c.OS = GuestLinux }, ""},
		{"valid windows", func(c *RunConfig) { c.OS = GuestWindows }, ""},
		{"missing OS", func(c *RunConfig) { c.OS = GuestUnknown }, "guest OS not set"},
		{"zero CPU", func(c *RunConfig) { c.CPUCount = 0 }, "cpu count"},
		{"zero memory", func(c *RunConfig) { c.MemoryGB = 0 }, "memory must"},
		{"gui and headless", func(c *RunConfig) { c.GUI = true; c.Headless = true }, "mutually exclusive"},
		{"shell with headless", func(c *RunConfig) {
			c.OS = GuestLinux
			c.LinuxShell = true
			c.Headless = true
		}, "-shell requires"},
		{"ipsw and iso both set", func(c *RunConfig) {
			c.OS = GuestMacOS
			c.IPSWPath = "/x.ipsw"
			c.ISOPath = "/x.iso"
		}, "-ipsw and -iso"},
		{"kernel on macos", func(c *RunConfig) {
			c.OS = GuestMacOS
			c.KernelPath = "/vmlinuz"
		}, "Linux-only"},
		{"ipsw on windows", func(c *RunConfig) {
			c.OS = GuestWindows
			c.IPSWPath = "/x.ipsw"
		}, "macOS-only"},
		{"empty usb path", func(c *RunConfig) { c.USB = []USBSpec{{Path: ""}} }, "empty path"},
		{"empty volume path", func(c *RunConfig) { c.Volumes = []VolumeMount{{Tag: "t"}} }, "empty host path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validRunConfig(GuestMacOS)
			tt.mutate(&c)
			err := c.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateNilReceiver(t *testing.T) {
	var c *RunConfig
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for nil receiver")
	}
}

func TestPlanRequiresVMDir(t *testing.T) {
	rc := validRunConfig(GuestLinux)
	hc := HostConfig{}
	if _, err := Plan(rc, hc); err == nil {
		t.Fatal("expected error for empty VMDir")
	}
}

func TestPlanMacOSAudio(t *testing.T) {
	rc := validRunConfig(GuestMacOS)
	plan, err := Plan(rc, validHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Audio.Enabled || !plan.Audio.HostInput || !plan.Audio.HostOutput {
		t.Fatalf("macOS plan must enable host input+output audio, got %+v", plan.Audio)
	}
}

func TestPlanLinuxAudio(t *testing.T) {
	rc := validRunConfig(GuestLinux)
	plan, err := Plan(rc, validHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Audio.Enabled || plan.Audio.HostInput || !plan.Audio.HostOutput {
		t.Fatalf("Linux plan must enable host output only, got %+v", plan.Audio)
	}
}

func TestPlanWindowsAudio(t *testing.T) {
	rc := validRunConfig(GuestWindows)
	plan, err := Plan(rc, validHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Audio.Enabled {
		t.Fatalf("Windows plan must not enable audio, got %+v", plan.Audio)
	}
}

func TestPlanRosettaIsLinuxOnly(t *testing.T) {
	for _, os := range []GuestOS{GuestMacOS, GuestLinux, GuestWindows} {
		rc := validRunConfig(os)
		rc.EnableRosetta = true
		plan, err := Plan(rc, validHostConfig())
		if err != nil {
			t.Fatalf("os=%v: %v", os, err)
		}
		if plan.Rosetta != (os == GuestLinux) {
			t.Fatalf("os=%v: Rosetta=%v, want %v", os, plan.Rosetta, os == GuestLinux)
		}
	}
}

func TestPlanStorageOrder(t *testing.T) {
	rc := validRunConfig(GuestLinux)
	rc.ISOPath = "/iso/install.iso"
	rc.USB = []USBSpec{{Path: "/usb/a.img"}, {Path: "/usb/b.img", ReadOnly: true}}
	rc.BlockDevices = []BlockSpec{{Path: "/dev/disk5", Cache: "writethrough"}}

	plan, err := Plan(rc, validHostConfig())
	if err != nil {
		t.Fatal(err)
	}

	wantKinds := []StorageKind{StorageRoot, StorageISO, StorageUSB, StorageUSB, StorageBlock}
	if len(plan.Storage) != len(wantKinds) {
		t.Fatalf("storage count = %d, want %d (%+v)", len(plan.Storage), len(wantKinds), plan.Storage)
	}
	for i, k := range wantKinds {
		if plan.Storage[i].Kind != k {
			t.Fatalf("storage[%d].Kind = %v, want %v", i, plan.Storage[i].Kind, k)
		}
	}
	if !plan.Storage[3].ReadOnly {
		t.Fatal("USB[1] should be read-only")
	}
	if plan.Storage[4].Cache != "writethrough" {
		t.Fatalf("block cache = %q, want writethrough", plan.Storage[4].Cache)
	}
}

func TestPlanUSBCarriesIdentitySlot(t *testing.T) {
	rc := validRunConfig(GuestLinux)
	rc.USB = []USBSpec{
		{Path: "/usb/a.img"},
		{Path: "/usb/b.img", ReadOnly: true},
	}
	plan, err := Plan(rc, validHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.USB) != 2 {
		t.Fatalf("USB count = %d, want 2 (%+v)", len(plan.USB), plan.USB)
	}
	want := []USBPlan{
		{Path: "/usb/a.img"},
		{Path: "/usb/b.img", ReadOnly: true},
	}
	for i, w := range want {
		if plan.USB[i] != w {
			t.Errorf("USB[%d] = %+v, want %+v", i, plan.USB[i], w)
		}
	}
	if plan.USB[0].UUID != "" || plan.USB[1].UUID != "" {
		t.Fatal("Plan must not invent a UUID; package main fills the slot")
	}
}

func TestPlanSerialNormalize(t *testing.T) {
	cases := map[string]string{
		"":         "stdout",
		"stdout":   "stdout",
		"STDOUT":   "stdout",
		"  none ":  "none",
		"off":      "none",
		"/tmp/log": "/tmp/log",
	}
	for in, want := range cases {
		rc := validRunConfig(GuestLinux)
		rc.SerialOutput = in
		plan, err := Plan(rc, validHostConfig())
		if err != nil {
			t.Fatalf("input %q: %v", in, err)
		}
		if plan.SerialDest != want {
			t.Errorf("normalizeSerial(%q) = %q, want %q", in, plan.SerialDest, want)
		}
	}
}

func TestPlanCopiesSlices(t *testing.T) {
	rc := validRunConfig(GuestLinux)
	rc.Displays = []DisplaySpec{{Width: 1024, Height: 768}}
	plan, err := Plan(rc, validHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	rc.Displays[0].Width = 9999
	if plan.Display[0].Width == 9999 {
		t.Fatal("Plan must copy Displays, not alias caller's slice")
	}
}

func TestRunConfigJSONRoundTrip(t *testing.T) {
	rc := validRunConfig(GuestMacOS)
	rc.IPSWPath = "/ipsw/r.ipsw"
	rc.GUI = true
	rc.USB = []USBSpec{{Path: "/u.img", ReadOnly: true}}
	rc.Volumes = []VolumeMount{{HostPath: "/h", Tag: "shared", ReadOnly: false}}
	rc.StartTimeout = 90 * time.Second

	data, err := json.Marshal(rc)
	if err != nil {
		t.Fatal(err)
	}
	var got RunConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rc, got) {
		t.Fatalf("round-trip mismatch:\n have %+v\n want %+v", got, rc)
	}
}

func TestResolveISO(t *testing.T) {
	rc := validRunConfig(GuestLinux)
	if rc.ISOPath != "" {
		t.Fatalf("test fixture leaks ISOPath = %q", rc.ISOPath)
	}
	rc.ResolveISO("/cache/ubuntu.iso")
	if rc.ISOPath != "/cache/ubuntu.iso" {
		t.Fatalf("ResolveISO did not set ISOPath, got %q", rc.ISOPath)
	}
	rc.ResolveISO("/cache/other.iso")
	if rc.ISOPath != "/cache/other.iso" {
		t.Fatalf("ResolveISO did not overwrite ISOPath, got %q", rc.ISOPath)
	}

	var nilRC *RunConfig
	nilRC.ResolveISO("/x") // must not panic
}

func TestGuestOSString(t *testing.T) {
	cases := map[GuestOS]string{
		GuestUnknown: "unknown",
		GuestMacOS:   "macos",
		GuestLinux:   "linux",
		GuestWindows: "windows",
	}
	for os, want := range cases {
		if got := os.String(); got != want {
			t.Errorf("GuestOS(%d).String() = %q, want %q", os, got, want)
		}
	}
}

func TestStorageKindString(t *testing.T) {
	for _, tt := range []struct {
		in   StorageKind
		want string
	}{
		{StorageRoot, "root"},
		{StorageISO, "iso"},
		{StorageUSB, "usb"},
		{StorageBlock, "block"},
		{StorageKind(99), "unknown"},
	} {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("StorageKind(%d).String() = %q, want %q", tt.in, got, tt.want)
		}
	}
}
