package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmrun"
)

func TestParseWindowsBackend(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want windowsBackend
	}{
		{name: "default", in: "", want: windowsBackendVZ},
		{name: "vz", in: "vz", want: windowsBackendVZ},
		{name: "virtualization", in: "virtualization", want: windowsBackendVZ},
		{name: "qemu", in: "qemu", want: windowsBackendQEMU},
		{name: "trim case", in: " QEMU ", want: windowsBackendQEMU},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWindowsBackend(tt.in)
			if err != nil {
				t.Fatalf("parseWindowsBackend(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("parseWindowsBackend(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
	if _, err := parseWindowsBackend("hvf"); err == nil {
		t.Fatalf("parseWindowsBackend(hvf) succeeded")
	}
}

func TestWindowsQEMUArgsInstallShape(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "windows.iso")
	if err := os.WriteFile(iso, []byte("iso"), 0644); err != nil {
		t.Fatal(err)
	}
	virtioISO := filepath.Join(dir, "virtio.iso")
	if err := os.WriteFile(virtioISO, []byte("virtio"), 0644); err != nil {
		t.Fatal(err)
	}
	autounattendISO := filepath.Join(dir, "autounattend.iso")
	if err := os.WriteFile(autounattendISO, []byte("autounattend"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := windowsQEMUConfig{
		EFICodePath:          filepath.Join(dir, "code.fd"),
		EFIVarsPath:          filepath.Join(dir, "vars.fd"),
		DiskPath:             filepath.Join(dir, "windows.qcow2"),
		DiskFormat:           "qcow2",
		ISOPath:              iso,
		CPUCount:             4,
		MemoryGB:             3,
		NetworkMode:          "nat",
		Headless:             true,
		DisplayDevice:        "ramfb",
		Nodefaults:           true,
		EnableClipboard:      true,
		SerialOutput:         "none",
		MonitorSockPath:      filepath.Join(dir, "monitor.sock"),
		VNCHost:              "127.0.0.1",
		VNCPort:              5907,
		AutounattendISOPath:  autounattendISO,
		VirtioISOPath:        virtioISO,
		SpiceGuestToolsPath:  filepath.Join(dir, "spice-guest-tools.exe"),
		AgentHostAddress:     "127.0.0.1",
		AgentHostPort:        32102,
		AgentGuestPort:       1024,
		UserAgentHostAddress: "127.0.0.1",
		UserAgentHostPort:    32103,
		UserAgentGuestPort:   1025,
	}
	args, err := windowsQEMUArgs(cfg)
	if err != nil {
		t.Fatalf("windowsQEMUArgs: %v", err)
	}
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"virt,accel=hvf,highmem=off",
		"-nodefaults",
		"if=pflash,format=raw,readonly=on,unit=0,file.locking=off,file=" + cfg.EFICodePath,
		"if=pflash,format=raw,unit=1,file=" + cfg.EFIVarsPath,
		"host",
		"ramfb",
		"qemu-xhci,id=xhci",
		"usb-storage,drive=cd0,bootindex=1",
		"usb-storage,drive=virtio0",
		"usb-storage,drive=oemdrv0",
		"usb-tablet,bus=xhci.0",
		"nvme,drive=hd0,serial=covewindows001,bootindex=2",
		"virtio-serial-pci,id=virtio-serial0,max_ports=16",
		"qemu-vdagent,id=vdagent0,name=vdagent,clipboard=on,mouse=off",
		"virtserialport,bus=virtio-serial0.0,chardev=vdagent0,name=com.redhat.spice.0",
		"user,id=net0",
		"hostfwd=tcp:127.0.0.1:32102-:1024",
		"hostfwd=tcp:127.0.0.1:32103-:1025",
		"virtio-net-pci,netdev=net0",
		"unix:" + cfg.MonitorSockPath + ",server=on,wait=off",
		"127.0.0.1:7",
		"none",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("QEMU args missing %q in:\n%s", want, joined)
		}
	}
}

func TestWindowsQEMUConfigDefaultsSerialToLog(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "qemu-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	code := filepath.Join(dir, "code.fd")
	if err := os.WriteFile(code, []byte("code"), 0644); err != nil {
		t.Fatal(err)
	}
	vars := filepath.Join(dir, "vars.fd")
	if err := os.WriteFile(vars, []byte("vars"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COVE_QEMU_SYSTEM_AARCH64", tool)
	t.Setenv("COVE_QEMU_IMG", tool)
	t.Setenv("COVE_QEMU_EFI_CODE", code)
	t.Setenv("COVE_QEMU_EFI_VARS_TEMPLATE", vars)

	vmDir := filepath.Join(dir, "vm.covevm")
	cfg, err := windowsQEMUConfigFromRun(vmrun.RunConfig{
		CPUCount:     2,
		MemoryGB:     4,
		NetworkMode:  "nat",
		SerialOutput: "stdout",
	}, vmrun.HostConfig{VMDir: vmDir}, false)
	if err != nil {
		t.Fatalf("windowsQEMUConfigFromRun: %v", err)
	}
	want := filepath.Join(vmDir, "qemu", "serial.log")
	if cfg.SerialOutput != want {
		t.Fatalf("SerialOutput = %q, want %q", cfg.SerialOutput, want)
	}
	if cfg.SerialLogPath != want {
		t.Fatalf("SerialLogPath = %q, want %q", cfg.SerialLogPath, want)
	}
}

func TestRemoveWindowsQEMUMonitorSocket(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "qemu-sock-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "monitor.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if err := removeWindowsQEMUMonitorSocket(sock); err != nil {
		t.Fatalf("remove socket: %v", err)
	}
	if _, err := os.Lstat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket still exists: %v", err)
	}

	regular := filepath.Join(dir, "regular")
	if err := os.WriteFile(regular, []byte("not a socket"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := removeWindowsQEMUMonitorSocket(regular); err == nil {
		t.Fatalf("removed regular file")
	}
}

func TestWindowsQEMUSendBootKeys(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "qemu-key-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "monitor.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	got := make(chan string, 2)
	go func() {
		for i := 0; i < 2; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, len("sendkey spc\n"))
			n, _ := conn.Read(buf)
			conn.Close()
			got <- string(buf[:n])
		}
	}()

	windowsQEMUSendBootKeys(sock, windowsQEMUBootKeyConfig{
		Delay:    0,
		Count:    2,
		Interval: time.Millisecond,
	})
	for i := 0; i < 2; i++ {
		select {
		case msg := <-got:
			if msg != "sendkey spc\n" {
				t.Fatalf("monitor command = %q", msg)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for monitor command %d", i)
		}
	}
}

func TestWriteWindowsQEMUMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	cfg := windowsQEMUConfig{
		QEMUPath:             "/bin/echo",
		EFICodePath:          filepath.Join(dir, "code.fd"),
		EFIVarsTemplatePath:  filepath.Join(dir, "vars-template.fd"),
		EFIVarsPath:          filepath.Join(dir, "vars.fd"),
		DiskPath:             filepath.Join(dir, "windows.qcow2"),
		DiskFormat:           "qcow2",
		ISOPath:              filepath.Join(dir, "windows.iso"),
		DisplayDevice:        "ramfb",
		InputDevice:          "usb-mouse",
		EnableClipboard:      true,
		SerialOutput:         filepath.Join(dir, "serial.log"),
		MonitorSockPath:      filepath.Join(dir, "monitor.sock"),
		VNCHost:              "127.0.0.1",
		VNCPort:              5907,
		GuestUsername:        "cove",
		GuestPassword:        "Cove123!",
		AutounattendISOPath:  filepath.Join(dir, "autounattend.iso"),
		VirtioISOPath:        filepath.Join(dir, "virtio.iso"),
		SpiceGuestToolsPath:  filepath.Join(dir, "spice-guest-tools.exe"),
		AgentExecutablePath:  filepath.Join(dir, "vz-agent.exe"),
		AgentHostAddress:     "127.0.0.1",
		AgentHostPort:        32102,
		AgentGuestPort:       1024,
		UserAgentHostAddress: "127.0.0.1",
		UserAgentHostPort:    32103,
		UserAgentGuestPort:   1025,
	}
	args := []string{"-machine", "virt,accel=hvf"}
	if err := writeWindowsQEMUMetadata(path, cfg, args); err != nil {
		t.Fatalf("writeWindowsQEMUMetadata: %v", err)
	}
	var got windowsQEMUMetadata
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Backend != "qemu-hvf" || got.DiskFormat != "qcow2" || got.Display != "ramfb" || !got.Clipboard {
		t.Fatalf("metadata = %#v", got)
	}
	if got.InputDevice != "usb-mouse" {
		t.Fatalf("metadata input device = %q, want usb-mouse", got.InputDevice)
	}
	if got.VNCEndpoint != "127.0.0.1:5907" || got.VNCURL != "vnc://127.0.0.1:5907" {
		t.Fatalf("metadata vnc = %q/%q", got.VNCEndpoint, got.VNCURL)
	}
	if got.GuestUsername != "cove" || got.GuestPassword != "Cove123!" {
		t.Fatalf("metadata guest credentials = %q/%q", got.GuestUsername, got.GuestPassword)
	}
	if got.AutounattendISOPath != cfg.AutounattendISOPath || got.VirtioISOPath != cfg.VirtioISOPath {
		t.Fatalf("metadata ISO paths = %q, %q", got.AutounattendISOPath, got.VirtioISOPath)
	}
	if got.SpiceGuestToolsPath != cfg.SpiceGuestToolsPath {
		t.Fatalf("metadata SPICE guest tools path = %q, want %q", got.SpiceGuestToolsPath, cfg.SpiceGuestToolsPath)
	}
	if got.AgentExecutablePath != cfg.AgentExecutablePath || got.AgentHostAddress != "127.0.0.1" || got.AgentHostPort != 32102 || got.AgentGuestPort != 1024 {
		t.Fatalf("metadata agent endpoint = %#v", got)
	}
	if got.UserAgentHostAddress != "127.0.0.1" || got.UserAgentHostPort != 32103 || got.UserAgentGuestPort != 1025 {
		t.Fatalf("metadata user agent endpoint = %#v", got)
	}
	if strings.Join(got.Args, " ") != strings.Join(args, " ") {
		t.Fatalf("args = %v, want %v", got.Args, args)
	}
}

func TestWriteWindowsQEMUProcessMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "process.json")
	started := time.Date(2026, 5, 18, 22, 0, 0, 0, time.UTC)
	exited := started.Add(time.Minute)
	want := windowsQEMUProcessMetadata{
		State:           "stopped",
		CovePID:         123,
		QEMUPID:         456,
		StartedAt:       started,
		ExitedAt:        &exited,
		MonitorSockPath: filepath.Join(dir, "monitor.sock"),
	}
	if err := writeWindowsQEMUProcessMetadata(path, want); err != nil {
		t.Fatalf("writeWindowsQEMUProcessMetadata: %v", err)
	}
	var got windowsQEMUProcessMetadata
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.State != want.State || got.CovePID != want.CovePID || got.QEMUPID != want.QEMUPID || got.MonitorSockPath != want.MonitorSockPath {
		t.Fatalf("process metadata = %#v, want %#v", got, want)
	}
	if got.ExitedAt == nil || !got.StartedAt.Equal(started) || !got.ExitedAt.Equal(exited) {
		t.Fatalf("process metadata times = %s/%s, want %s/%s", got.StartedAt, got.ExitedAt, started, exited)
	}
}

func TestWriteWindowsQEMUProcessMetadataOmitsExitedAtWhenRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "process.json")
	if err := writeWindowsQEMUProcessMetadata(path, windowsQEMUProcessMetadata{
		State:     "running",
		CovePID:   123,
		QEMUPID:   456,
		StartedAt: time.Date(2026, 5, 18, 22, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("writeWindowsQEMUProcessMetadata: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "exitedAt") {
		t.Fatalf("running process metadata contains exitedAt:\n%s", data)
	}
}

func TestWindowsQEMUProcessPath(t *testing.T) {
	dir := t.TempDir()
	cfg := windowsQEMUConfig{MonitorSockPath: filepath.Join(dir, "qemu", "monitor.sock")}
	if got, want := windowsQEMUProcessPath(cfg), filepath.Join(dir, "qemu", "process.json"); got != want {
		t.Fatalf("windowsQEMUProcessPath = %q, want %q", got, want)
	}
}

func TestOpenWindowsQEMUVNCIfNeeded(t *testing.T) {
	oldOpen := windowsQEMUOpenURL
	t.Cleanup(func() { windowsQEMUOpenURL = oldOpen })
	var opened []string
	windowsQEMUOpenURL = func(url string) error {
		opened = append(opened, url)
		return nil
	}

	if err := openWindowsQEMUVNCIfNeeded(windowsQEMUConfig{VNCHost: "127.0.0.1", VNCPort: 5907}); err != nil {
		t.Fatalf("openWindowsQEMUVNCIfNeeded: %v", err)
	}
	if got, want := strings.Join(opened, ","), "vnc://127.0.0.1:5907"; got != want {
		t.Fatalf("opened = %q, want %q", got, want)
	}

	opened = nil
	if err := openWindowsQEMUVNCIfNeeded(windowsQEMUConfig{Headless: true, VNCHost: "127.0.0.1", VNCPort: 5907}); err != nil {
		t.Fatalf("openWindowsQEMUVNCIfNeeded headless: %v", err)
	}
	if len(opened) != 0 {
		t.Fatalf("headless opened %#v, want none", opened)
	}

	if err := openWindowsQEMUVNCIfNeeded(windowsQEMUConfig{}); err != nil {
		t.Fatalf("openWindowsQEMUVNCIfNeeded no vnc: %v", err)
	}
	if len(opened) != 0 {
		t.Fatalf("no-vnc opened %#v, want none", opened)
	}
}

func TestWindowsQEMUVNCHelp(t *testing.T) {
	tests := []struct {
		name string
		cfg  windowsQEMUConfig
		want string
	}{
		{
			name: "no vnc",
			cfg:  windowsQEMUConfig{},
			want: "pass -vnc :5901",
		},
		{
			name: "headed vnc",
			cfg:  windowsQEMUConfig{VNCHost: "127.0.0.1", VNCPort: 5907},
			want: "opens automatically for headed runs",
		},
		{
			name: "headless vnc",
			cfg:  windowsQEMUConfig{Headless: true, VNCHost: "127.0.0.1", VNCPort: 5907},
			want: "open vnc://127.0.0.1:5907",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := windowsQEMUVNCHelp(tt.cfg); !strings.Contains(got, tt.want) {
				t.Fatalf("windowsQEMUVNCHelp = %q, want substring %q", got, tt.want)
			}
		})
	}
}

func TestEnsureWindowsQEMUEFIVars(t *testing.T) {
	dir := t.TempDir()
	template := filepath.Join(dir, "template.fd")
	if err := os.WriteFile(template, []byte("vars-template"), 0644); err != nil {
		t.Fatal(err)
	}
	vars := filepath.Join(dir, "qemu", "efi_vars.fd")
	if err := os.MkdirAll(filepath.Dir(vars), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vars, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ensureWindowsQEMUEFIVars(vars, template); err != nil {
		t.Fatalf("ensureWindowsQEMUEFIVars: %v", err)
	}
	got, err := os.ReadFile(vars)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "vars-template") {
		t.Fatalf("vars file does not start with template data")
	}
	info, err := os.Stat(vars)
	if err != nil {
		t.Fatal(err)
	}
	const wantSize = 64 * 1024 * 1024
	if info.Size() != wantSize {
		t.Fatalf("vars size = %d, want %d", info.Size(), wantSize)
	}
}

func TestWindowsQEMUNetworkArgs(t *testing.T) {
	if _, err := windowsQEMUNetworkArgs(windowsQEMUConfig{NetworkMode: "nat"}); err != nil {
		t.Fatalf("nat network: %v", err)
	}
	withSMB, err := windowsQEMUNetworkArgs(windowsQEMUConfig{
		NetworkMode:        "nat",
		SMBSharedDirectory: "/Users/tmc/ml-explore",
	})
	if err != nil {
		t.Fatalf("smb network: %v", err)
	}
	if got, want := strings.Join(withSMB, " "), "smb=/Users/tmc/ml-explore"; !strings.Contains(got, want) {
		t.Fatalf("smb network args = %v, want %q", withSMB, want)
	}
	args, err := windowsQEMUNetworkArgs(windowsQEMUConfig{NetworkMode: "none"})
	if err != nil {
		t.Fatalf("none network: %v", err)
	}
	if len(args) != 0 {
		t.Fatalf("none network args = %v, want empty", args)
	}
	if _, err := windowsQEMUNetworkArgs(windowsQEMUConfig{NetworkMode: "bridged:en0"}); err == nil {
		t.Fatalf("bridged network succeeded")
	}
}

func TestWindowsQEMUAgentForwardConfig(t *testing.T) {
	t.Setenv("COVE_QEMU_AGENT_HOST_PORT", "32102")
	got, err := windowsQEMUAgentForwardConfig("nat")
	if err != nil {
		t.Fatalf("windowsQEMUAgentForwardConfig: %v", err)
	}
	if got.hostAddress != "127.0.0.1" || got.hostPort != 32102 || got.guestPort != 1024 {
		t.Fatalf("agent forward = %#v", got)
	}

	t.Setenv("COVE_QEMU_AGENT_FORWARD", "off")
	disabled, err := windowsQEMUAgentForwardConfig("nat")
	if err != nil {
		t.Fatalf("disabled agent forward: %v", err)
	}
	if disabled.hostPort != 0 {
		t.Fatalf("disabled agent forward = %#v, want zero", disabled)
	}
}

func TestWindowsQEMUUserAgentForwardConfig(t *testing.T) {
	t.Setenv("COVE_QEMU_USER_AGENT_HOST_PORT", "32103")
	got, err := windowsQEMUUserAgentForwardConfig("nat")
	if err != nil {
		t.Fatalf("windowsQEMUUserAgentForwardConfig: %v", err)
	}
	if got.hostAddress != "127.0.0.1" || got.hostPort != 32103 || got.guestPort != 1025 {
		t.Fatalf("user agent forward = %#v", got)
	}

	t.Setenv("COVE_QEMU_USER_AGENT_FORWARD", "off")
	disabled, err := windowsQEMUUserAgentForwardConfig("nat")
	if err != nil {
		t.Fatalf("disabled user agent forward: %v", err)
	}
	if disabled.hostPort != 0 {
		t.Fatalf("disabled user agent forward = %#v, want zero", disabled)
	}
}

func TestWindowsQEMUDisplayDeviceArgs(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want []string
	}{
		{name: "default", in: "", want: []string{"-device", "ramfb"}},
		{name: "ramfb", in: "ramfb", want: []string{"-device", "ramfb"}},
		{name: "virtio gpu", in: "virtio-gpu-pci", want: []string{"-device", "virtio-gpu-pci,xres=1280,yres=800"}},
		{name: "combined", in: "ramfb+virtio-gpu-pci", want: []string{"-device", "ramfb", "-device", "virtio-gpu-pci,xres=1280,yres=800"}},
		{name: "none", in: "none", want: nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := windowsQEMUDisplayDeviceArgs(tt.in)
			if err != nil {
				t.Fatalf("windowsQEMUDisplayDeviceArgs(%q): %v", tt.in, err)
			}
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Fatalf("windowsQEMUDisplayDeviceArgs(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
	if _, err := windowsQEMUDisplayDeviceArgs("virtio-ramfb"); err == nil {
		t.Fatalf("virtio-ramfb succeeded")
	}
}

func TestWindowsQEMUDisplayDeviceArgsUsesDisplaySize(t *testing.T) {
	t.Setenv("COVE_QEMU_DISPLAY_SIZE", "1440x900")
	got, err := windowsQEMUDisplayDeviceArgs("virtio-gpu-pci")
	if err != nil {
		t.Fatalf("windowsQEMUDisplayDeviceArgs: %v", err)
	}
	want := []string{"-device", "virtio-gpu-pci,xres=1440,yres=900"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("windowsQEMUDisplayDeviceArgs = %v, want %v", got, want)
	}
}

func TestWindowsQEMUDisplaySizeArgRejectsInvalid(t *testing.T) {
	for _, in := range []string{"", "wide", "320x200", "1280"} {
		t.Setenv("COVE_QEMU_DISPLAY_SIZE", in)
		if got := windowsQEMUDisplaySizeArg(); got != "xres=1280,yres=800" {
			t.Fatalf("display size %q = %q, want default", in, got)
		}
	}
}

func TestWindowsQEMUCocoaDisplayScalesToWindow(t *testing.T) {
	dir := t.TempDir()
	cfg := windowsQEMUConfig{
		CPUCount:        2,
		MemoryGB:        4,
		DiskPath:        filepath.Join(dir, "windows.qcow2"),
		DiskFormat:      "qcow2",
		EFICodePath:     filepath.Join(dir, "edk2-code.fd"),
		EFIVarsPath:     filepath.Join(dir, "efi-vars.fd"),
		MonitorSockPath: filepath.Join(dir, "monitor.sock"),
		SerialOutput:    filepath.Join(dir, "serial.log"),
		SerialLogPath:   filepath.Join(dir, "serial.log"),
		DisplayDevice:   "ramfb",
		InputDevice:     "usb-tablet",
		NetworkMode:     "nat",
		Nodefaults:      true,
	}
	args, err := windowsQEMUArgs(cfg)
	if err != nil {
		t.Fatalf("windowsQEMUArgs: %v", err)
	}
	if got := strings.Join(args, "\x00"); !strings.Contains(got, "-display\x00cocoa,zoom-to-fit=on,show-cursor=on") {
		t.Fatalf("windowsQEMUArgs missing cocoa zoom-to-fit display: %v", args)
	}
}

func TestWindowsQEMUInputDeviceArgs(t *testing.T) {
	for _, tt := range []struct {
		name string
		want string
	}{
		{"", "usb-tablet,bus=xhci.0"},
		{"usb-mouse", "usb-mouse,bus=xhci.0"},
		{"usb-tablet", "usb-tablet,bus=xhci.0"},
	} {
		args, err := windowsQEMUInputDeviceArgs(tt.name)
		if err != nil {
			t.Fatalf("windowsQEMUInputDeviceArgs(%q): %v", tt.name, err)
		}
		if got := strings.Join(args, " "); !strings.Contains(got, tt.want) {
			t.Fatalf("windowsQEMUInputDeviceArgs(%q) = %v, want %q", tt.name, args, tt.want)
		}
	}
	if _, err := windowsQEMUInputDeviceArgs("trackpad"); err == nil {
		t.Fatal("windowsQEMUInputDeviceArgs(trackpad) returned nil error")
	}
}

func TestWindowsQEMUInputDeviceFromEnv(t *testing.T) {
	t.Setenv("COVE_QEMU_INPUT_DEVICE", "")
	if got := windowsQEMUInputDeviceFromEnv(); got != "usb-tablet" {
		t.Fatalf("default input device = %q, want usb-tablet", got)
	}
	t.Setenv("COVE_QEMU_INPUT_DEVICE", "usb-mouse")
	if got := windowsQEMUInputDeviceFromEnv(); got != "usb-mouse" {
		t.Fatalf("input device from env = %q, want usb-mouse", got)
	}
}

func TestWindowsQEMUDisplayDeviceFromEnv(t *testing.T) {
	t.Setenv("COVE_QEMU_DISPLAY_DEVICE", "")
	if got := windowsQEMUDisplayDeviceFromEnv(); got != "ramfb+virtio-gpu-pci" {
		t.Fatalf("default display device = %q", got)
	}
	t.Setenv("COVE_QEMU_DISPLAY_DEVICE", "ramfb,virtio-gpu-pci")
	if got := windowsQEMUDisplayDeviceFromEnv(); got != "ramfb+virtio-gpu-pci" {
		t.Fatalf("comma display device = %q", got)
	}
}

func TestWindowsQEMUMachineArg(t *testing.T) {
	for _, tt := range []struct {
		name     string
		memoryGB uint64
		want     string
	}{
		{name: "low memory", memoryGB: 3, want: "virt,accel=hvf,highmem=off"},
		{name: "default memory", memoryGB: 4, want: "virt,accel=hvf"},
		{name: "large memory", memoryGB: 48, want: "virt,accel=hvf"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := windowsQEMUMachineArg(tt.memoryGB); got != tt.want {
				t.Fatalf("windowsQEMUMachineArg(%d) = %q, want %q", tt.memoryGB, got, tt.want)
			}
		})
	}
}
