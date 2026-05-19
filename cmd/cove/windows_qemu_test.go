package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		EFICodePath:         filepath.Join(dir, "code.fd"),
		EFIVarsPath:         filepath.Join(dir, "vars.fd"),
		DiskPath:            filepath.Join(dir, "windows.qcow2"),
		DiskFormat:          "qcow2",
		ISOPath:             iso,
		CPUCount:            4,
		MemoryGB:            3,
		NetworkMode:         "nat",
		Headless:            true,
		DisplayDevice:       "ramfb",
		Nodefaults:          true,
		SerialOutput:        "none",
		MonitorSockPath:     filepath.Join(dir, "monitor.sock"),
		AutounattendISOPath: autounattendISO,
		VirtioISOPath:       virtioISO,
		AgentHostAddress:    "127.0.0.1",
		AgentHostPort:       32102,
		AgentGuestPort:      1024,
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
		"nvme,drive=hd0,serial=covewindows001,bootindex=2",
		"user,id=net0",
		"hostfwd=tcp:127.0.0.1:32102-:1024",
		"virtio-net-pci,netdev=net0",
		"unix:" + cfg.MonitorSockPath + ",server=on,wait=off",
		"none",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("QEMU args missing %q in:\n%s", want, joined)
		}
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
		QEMUPath:            "/bin/echo",
		EFICodePath:         filepath.Join(dir, "code.fd"),
		EFIVarsTemplatePath: filepath.Join(dir, "vars-template.fd"),
		EFIVarsPath:         filepath.Join(dir, "vars.fd"),
		DiskPath:            filepath.Join(dir, "windows.qcow2"),
		DiskFormat:          "qcow2",
		ISOPath:             filepath.Join(dir, "windows.iso"),
		DisplayDevice:       "ramfb",
		SerialOutput:        filepath.Join(dir, "serial.log"),
		MonitorSockPath:     filepath.Join(dir, "monitor.sock"),
		AutounattendISOPath: filepath.Join(dir, "autounattend.iso"),
		VirtioISOPath:       filepath.Join(dir, "virtio.iso"),
		AgentExecutablePath: filepath.Join(dir, "vz-agent.exe"),
		AgentHostAddress:    "127.0.0.1",
		AgentHostPort:       32102,
		AgentGuestPort:      1024,
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
	if got.Backend != "qemu-hvf" || got.DiskFormat != "qcow2" || got.Display != "ramfb" {
		t.Fatalf("metadata = %#v", got)
	}
	if got.AutounattendISOPath != cfg.AutounattendISOPath || got.VirtioISOPath != cfg.VirtioISOPath {
		t.Fatalf("metadata ISO paths = %q, %q", got.AutounattendISOPath, got.VirtioISOPath)
	}
	if got.AgentExecutablePath != cfg.AgentExecutablePath || got.AgentHostAddress != "127.0.0.1" || got.AgentHostPort != 32102 || got.AgentGuestPort != 1024 {
		t.Fatalf("metadata agent endpoint = %#v", got)
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
