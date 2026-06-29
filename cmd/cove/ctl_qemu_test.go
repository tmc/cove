package main

import (
	"encoding/json"
	"image"
	"image/color"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestReadWindowsQEMUCTLStatus(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	qemuDir := filepath.Join(dir, "qemu")
	if err := os.Mkdir(qemuDir, 0755); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)
	process := windowsQEMUProcessMetadata{
		State:           "running",
		CovePID:         123,
		QEMUPID:         456,
		StartedAt:       startedAt,
		MonitorSockPath: filepath.Join(qemuDir, "monitor.sock"),
	}
	data, err := json.Marshal(process)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qemuDir, "process.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qemuDir, "metadata.json"), []byte(`{"vncEndpoint":"127.0.0.1:5907","vncURL":"vnc://127.0.0.1:5907","guestUsername":"cove","guestPassword":"Cove123!"}`), 0644); err != nil {
		t.Fatal(err)
	}

	got := readWindowsQEMUCTLStatus(dir)
	if got.Backend != "qemu-hvf" || got.State != "running" || got.QEMUPID != 456 {
		t.Fatalf("status = %#v", got)
	}
	if got.MonitorSockPath != filepath.Join(qemuDir, "monitor.sock") {
		t.Fatalf("monitor = %q", got.MonitorSockPath)
	}
	if got.VNCEndpoint != "127.0.0.1:5907" || got.VNCURL != "vnc://127.0.0.1:5907" {
		t.Fatalf("vnc = %q/%q", got.VNCEndpoint, got.VNCURL)
	}
	if got.GUI != "qemu-vnc-external" || got.VNCAuth != "none" {
		t.Fatalf("gui/vnc auth = %q/%q", got.GUI, got.VNCAuth)
	}
	if got.ScreenshotBackend != "rfb" || got.TextBackend != "rfb" {
		t.Fatalf("backends = %q/%q, want rfb/rfb", got.ScreenshotBackend, got.TextBackend)
	}
	if got.GuestUsername != "cove" || got.GuestPassword != "Cove123!" {
		t.Fatalf("guest credentials = %q/%q", got.GuestUsername, got.GuestPassword)
	}
}

func TestReadWindowsQEMUCTLStatusUsesReachableMonitor(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "qemu-ctl-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	qemuDir := filepath.Join(dir, "qemu")
	if err := os.Mkdir(qemuDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qemuDir, "process.json"), []byte(`{"state":"stopped"}`), 0644); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(qemuDir, "monitor.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	got := readWindowsQEMUCTLStatus(dir)
	if got.State != "running" {
		t.Fatalf("state = %q, want running", got.State)
	}
}

func TestCtlCommandWindowsQEMUStatusBypassesControlSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName, vmDir = "", ""
	name := "qemu-win"
	dir := filepath.Join(vmconfig.BaseDir(), name+".covevm")
	if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "process.json"), []byte(`{"state":"stopped","qemuPid":987}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), []byte(`{"vncEndpoint":"127.0.0.1:5907","vncURL":"vnc://127.0.0.1:5907","guestUsername":"cove","guestPassword":"Cove123!"}`), 0644); err != nil {
		t.Fatal(err)
	}

	out := captureCtlQEMUStdout(t, func() error {
		return ctlCommand([]string{"-vm", name, "status"})
	})
	for _, want := range []string{"state:   stopped", "backend: qemu-hvf", "qemuPid: 987", "gui:     qemu-vnc-external", "vncURL:  vnc://127.0.0.1:5907", "vncAuth: none", "screens: rfb", "text:    rfb", "user:    cove", "pass:    Cove123!"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ctl qemu status output missing %q:\n%s", want, out)
		}
	}
}

func TestCtlCommandWindowsQEMUVNCStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName, vmDir = "", ""
	name := "qemu-win"
	dir := filepath.Join(vmconfig.BaseDir(), name+".covevm")
	if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), []byte(`{"vncEndpoint":"127.0.0.1:5907","vncURL":"vnc://127.0.0.1:5907","guestUsername":"cove","guestPassword":"Cove123!"}`), 0644); err != nil {
		t.Fatal(err)
	}

	out := captureCtlQEMUStdout(t, func() error {
		return ctlCommand([]string{"-vm", name, "vnc", "status"})
	})
	for _, want := range []string{"vnc:    127.0.0.1:5907", "vncURL: vnc://127.0.0.1:5907", "auth:   none; Windows credentials are for the guest login, not VNC"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ctl qemu vnc status output missing %q:\n%s", want, out)
		}
	}
}

func TestCtlCommandWindowsQEMUGUIStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName, vmDir = "", ""
	name := "qemu-win"
	dir := filepath.Join(vmconfig.BaseDir(), name+".covevm")
	if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), []byte(`{"vncEndpoint":"127.0.0.1:5907","vncURL":"vnc://127.0.0.1:5907","guestUsername":"cove","guestPassword":"Cove123!"}`), 0644); err != nil {
		t.Fatal(err)
	}

	out := captureCtlQEMUStdout(t, func() error {
		return ctlCommand([]string{"-vm", name, "gui", "status"})
	})
	for _, want := range []string{"backend: qemu-hvf", "gui:     qemu-vnc-external", "vncURL:  vnc://127.0.0.1:5907", "vncAuth: none; Windows credentials below are for the guest login", "screens: rfb", "text:    rfb", "user:    cove", "pass:    Cove123!"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ctl qemu gui status output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteWindowsQEMUGUIDiagnoseScreenshot(t *testing.T) {
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	path, err := writeWindowsQEMUGUIDiagnoseScreenshot(dir, img)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, filepath.Join(dir, "qemu", "screenshots", "gui-diagnose-")) {
		t.Fatalf("diagnose screenshot path = %q", path)
	}
	if filepath.Ext(path) != ".jpg" {
		t.Fatalf("diagnose screenshot extension = %q, want .jpg", filepath.Ext(path))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatal("diagnose screenshot is empty")
	}
}

func TestQEMUResolvedBackend(t *testing.T) {
	status := windowsQEMUCTLStatus{VNCURL: "vnc://127.0.0.1:5907"}
	if got := qemuResolvedBackend("COVE_QEMU_SCREENSHOT_BACKEND", status, "screendump"); got != "rfb" {
		t.Fatalf("auto with vnc = %q, want rfb", got)
	}
	t.Setenv("COVE_QEMU_SCREENSHOT_BACKEND", "monitor")
	if got := qemuResolvedBackend("COVE_QEMU_SCREENSHOT_BACKEND", status, "screendump"); got != "screendump" {
		t.Fatalf("monitor = %q, want screendump", got)
	}
	t.Setenv("COVE_QEMU_SCREENSHOT_BACKEND", "bad")
	if got := qemuResolvedBackend("COVE_QEMU_SCREENSHOT_BACKEND", status, "screendump"); got != "invalid:bad" {
		t.Fatalf("bad = %q, want invalid:bad", got)
	}
}

func TestQEMUGUIDisplayModeReportsCoveViewer(t *testing.T) {
	dir := t.TempDir()
	if err := writeWindowsQEMUViewerPID(dir, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	status := windowsQEMUCTLStatus{
		VMDir:  dir,
		VNCURL: "vnc://127.0.0.1:5907",
	}
	if got := qemuGUIDisplayMode(status); got != "qemu-vnc-cove" {
		t.Fatalf("qemuGUIDisplayMode() = %q, want qemu-vnc-cove", got)
	}
}

func TestQEMUDisplayInputMode(t *testing.T) {
	t.Run("default is responder", func(t *testing.T) {
		t.Setenv("COVE_QEMU_LEGACY_MONITORS", "")
		if got := qemuDisplayInputMode(); got != "responder" {
			t.Fatalf("qemuDisplayInputMode() = %q, want responder", got)
		}
	})
	t.Run("legacy env selects global monitor", func(t *testing.T) {
		t.Setenv("COVE_QEMU_LEGACY_MONITORS", "1")
		if got := qemuDisplayInputMode(); got != "global-monitor" {
			t.Fatalf("qemuDisplayInputMode() = %q, want global-monitor", got)
		}
	})
}

func TestWindowsQEMUViewerInputModeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got := windowsQEMUViewerInputMode(dir); got != "responder" {
		t.Fatalf("default input mode = %q, want responder", got)
	}
	if err := writeWindowsQEMUViewerInputMode(dir, "global-monitor"); err != nil {
		t.Fatal(err)
	}
	if got := windowsQEMUViewerInputMode(dir); got != "global-monitor" {
		t.Fatalf("input mode after write = %q, want global-monitor", got)
	}
}

func TestReadWindowsQEMUCTLStatusReportsViewerInputMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), []byte(`{"vncURL":"vnc://127.0.0.1:5907","vncEndpoint":"127.0.0.1:5907"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeWindowsQEMUViewerPID(dir, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err := writeWindowsQEMUViewerInputMode(dir, "responder"); err != nil {
		t.Fatal(err)
	}
	status := readWindowsQEMUCTLStatus(dir)
	if status.GUI != "qemu-vnc-cove" {
		t.Fatalf("status.GUI = %q, want qemu-vnc-cove", status.GUI)
	}
	if status.DisplayInputMode != "responder" {
		t.Fatalf("status.DisplayInputMode = %q, want responder", status.DisplayInputMode)
	}
}

func TestWindowsQEMUViewerSettled(t *testing.T) {
	t.Run("live process settles", func(t *testing.T) {
		if !windowsQEMUViewerSettled(os.Getpid(), 100*time.Millisecond) {
			t.Fatal("live process reported as not settled")
		}
	})
	t.Run("dead process does not settle", func(t *testing.T) {
		// A PID that is almost certainly not a live process.
		if windowsQEMUViewerSettled(1<<30, 100*time.Millisecond) {
			t.Fatal("dead process reported as settled")
		}
	})
}

func TestWindowsQEMUViewerLogTail(t *testing.T) {
	dir := t.TempDir()
	if got := windowsQEMUViewerLogTail(dir); got != "" {
		t.Fatalf("log tail with no log = %q, want empty", got)
	}
	if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	log := "qemu-display: starting\nqemu-display: connect qemu display rfb: dial tcp: refused\n"
	if err := os.WriteFile(filepath.Join(dir, "qemu", "viewer.log"), []byte(log), 0644); err != nil {
		t.Fatal(err)
	}
	if got := windowsQEMUViewerLogTail(dir); got != "qemu-display: connect qemu display rfb: dial tcp: refused" {
		t.Fatalf("log tail = %q", got)
	}
}

func TestQEMUGUIOpenMode(t *testing.T) {
	t.Run("default uses Cove viewer with VNC endpoint", func(t *testing.T) {
		t.Setenv("COVE_QEMU_GUI_VIEWER", "")
		got := qemuGUIOpenMode(windowsQEMUCTLStatus{VNCEndpoint: "127.0.0.1:5907"})
		if got != "cove" {
			t.Fatalf("qemuGUIOpenMode() = %q, want cove", got)
		}
	})
	t.Run("external env overrides VNC endpoint", func(t *testing.T) {
		t.Setenv("COVE_QEMU_GUI_VIEWER", "external")
		got := qemuGUIOpenMode(windowsQEMUCTLStatus{VNCEndpoint: "127.0.0.1:5907"})
		if got != "external" {
			t.Fatalf("qemuGUIOpenMode() = %q, want external", got)
		}
	})
	t.Run("no VNC endpoint uses external error path", func(t *testing.T) {
		t.Setenv("COVE_QEMU_GUI_VIEWER", "")
		got := qemuGUIOpenMode(windowsQEMUCTLStatus{})
		if got != "external" {
			t.Fatalf("qemuGUIOpenMode() = %q, want external", got)
		}
	})
}

func TestCtlCommandWindowsQEMUGUICloseExplainsExternalVNC(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName, vmDir = "", ""
	name := "qemu-win"
	dir := filepath.Join(vmconfig.BaseDir(), name+".covevm")
	if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), []byte(`{"vncURL":"vnc://127.0.0.1:5907"}`), 0644); err != nil {
		t.Fatal(err)
	}

	err := ctlCommand([]string{"-vm", name, "gui", "close"})
	if err == nil {
		t.Fatal("ctl gui close succeeded")
	}
	if !strings.Contains(err.Error(), "external VNC viewer window") {
		t.Fatalf("ctl gui close error = %v", err)
	}
}

func TestCtlWindowsQEMUCloseCoveViewerStalePID(t *testing.T) {
	dir := t.TempDir()
	if err := writeWindowsQEMUViewerPID(dir, 99999999); err != nil {
		t.Fatal(err)
	}
	err := ctlWindowsQEMUCloseCoveViewer(dir)
	if err == nil {
		t.Fatal("ctlWindowsQEMUCloseCoveViewer succeeded")
	}
	if !strings.Contains(err.Error(), "close cove qemu display viewer") {
		t.Fatalf("ctlWindowsQEMUCloseCoveViewer error = %v", err)
	}
	if _, statErr := os.Stat(windowsQEMUViewerPIDPath(dir)); !os.IsNotExist(statErr) {
		t.Fatalf("viewer pid file still exists: %v", statErr)
	}
}

func TestCaptureWindowsQEMUImageRejectsInvalidBackend(t *testing.T) {
	t.Setenv("COVE_QEMU_SCREENSHOT_BACKEND", "bogus")
	_, err := captureWindowsQEMUImage(t.TempDir())
	if err == nil {
		t.Fatal("captureWindowsQEMUImage succeeded")
	}
	if !strings.Contains(err.Error(), "invalid COVE_QEMU_SCREENSHOT_BACKEND") {
		t.Fatalf("captureWindowsQEMUImage error = %v", err)
	}
}

func TestCaptureWindowsQEMUImageForcedRFBRequiresEndpoint(t *testing.T) {
	t.Setenv("COVE_QEMU_SCREENSHOT_BACKEND", "rfb")
	_, err := captureWindowsQEMUImage(t.TempDir())
	if err == nil {
		t.Fatal("captureWindowsQEMUImage succeeded")
	}
	if !strings.Contains(err.Error(), "qemu vnc endpoint is unavailable") {
		t.Fatalf("captureWindowsQEMUImage error = %v", err)
	}
}

func TestCtlWindowsQEMUMouseRequiresVNC(t *testing.T) {
	err := ctlWindowsQEMUMouse(t.TempDir(), []string{"0.5", "0.5", "click"}, false)
	if err == nil {
		t.Fatal("ctlWindowsQEMUMouse succeeded")
	}
	if !strings.Contains(err.Error(), "qemu vnc endpoint is unavailable") {
		t.Fatalf("ctlWindowsQEMUMouse error = %v", err)
	}
}

func TestQEMURFBMousePoint(t *testing.T) {
	for _, tt := range []struct {
		name string
		size image.Point
		x    float64
		y    float64
		want image.Point
	}{
		{name: "normalized center", size: image.Point{X: 800, Y: 600}, x: 0.5, y: 0.5, want: image.Point{X: 400, Y: 300}},
		{name: "normalized edge", size: image.Point{X: 800, Y: 600}, x: 1, y: 1, want: image.Point{X: 800, Y: 600}},
		{name: "absolute", size: image.Point{X: 800, Y: 600}, x: 42, y: 24, want: image.Point{X: 42, Y: 24}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := qemuRFBMousePoint(tt.size, tt.x, tt.y); got != tt.want {
				t.Fatalf("qemuRFBMousePoint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQEMUAgentHealthReportsUnresponsive(t *testing.T) {
	got := qemuAgentHealth("127.0.0.1:1")
	if !strings.HasPrefix(got, "unresponsive:") {
		t.Fatalf("qemuAgentHealth = %q, want unresponsive", got)
	}
}

func TestQEMUAgentStatusHint(t *testing.T) {
	got := qemuAgentStatusHint(windowsQEMUCTLStatus{
		AgentEndpoint: "127.0.0.1:32102",
		AgentHealth:   "unresponsive: dial tcp 127.0.0.1:32102: connect: connection refused",
	})
	if !strings.Contains(got, "guest firewall") {
		t.Fatalf("qemuAgentStatusHint = %q, want firewall hint", got)
	}
	if got := qemuAgentStatusHint(windowsQEMUCTLStatus{AgentEndpoint: "127.0.0.1:32102", AgentHealth: "connected"}); got != "" {
		t.Fatalf("qemuAgentStatusHint connected = %q, want empty", got)
	}
}

func TestCtlCommandWindowsQEMUScreenshotBypassesControlSocket(t *testing.T) {
	testCtlCommandWindowsQEMUVisualBypassesControlSocket(t, "screenshot")
}

func TestCtlCommandWindowsQEMUOCRBypassesControlSocket(t *testing.T) {
	testCtlCommandWindowsQEMUVisualBypassesControlSocket(t, "ocr")
}

func TestCtlCommandWindowsQEMUKeyBypassesControlSocket(t *testing.T) {
	testCtlCommandWindowsQEMUVisualBypassesControlSocket(t, "key", "return")
}

func TestCtlCommandWindowsQEMUTextBypassesControlSocket(t *testing.T) {
	testCtlCommandWindowsQEMUVisualBypassesControlSocket(t, "text", "hello")
}

func TestTypeWindowsQEMUTextRejectsInvalidBackend(t *testing.T) {
	t.Setenv("COVE_QEMU_TEXT_BACKEND", "bogus")
	err := typeWindowsQEMUText(t.TempDir(), "hello")
	if err == nil {
		t.Fatal("typeWindowsQEMUText succeeded")
	}
	if !strings.Contains(err.Error(), "invalid COVE_QEMU_TEXT_BACKEND") {
		t.Fatalf("typeWindowsQEMUText error = %v", err)
	}
}

func TestTypeWindowsQEMUTextForcedRFBRequiresEndpoint(t *testing.T) {
	t.Setenv("COVE_QEMU_TEXT_BACKEND", "rfb")
	err := typeWindowsQEMUText(t.TempDir(), "hello")
	if err == nil {
		t.Fatal("typeWindowsQEMUText succeeded")
	}
	if !strings.Contains(err.Error(), "qemu vnc endpoint is unavailable") {
		t.Fatalf("typeWindowsQEMUText error = %v", err)
	}
}

func TestCtlCommandWindowsQEMUClickTextBypassesControlSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName, vmDir = "", ""
	name := "qemu-win"
	dir := filepath.Join(vmconfig.BaseDir(), name+".covevm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ctlCommand([]string{"-vm", name, "click-text", "-timeout", "1ms", "OK"})
	if err == nil {
		t.Fatal("ctlCommand succeeded, want missing qemu monitor")
	}
	if !strings.Contains(err.Error(), `timeout: text "OK" not found`) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "control socket") {
		t.Fatalf("error fell through to control socket: %v", err)
	}
}

func TestCtlCommandWindowsQEMUClickTextArbitraryRequiresVNC(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName, vmDir = "", ""
	name := "qemu-win"
	dir := filepath.Join(vmconfig.BaseDir(), name+".covevm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ctlCommand([]string{"-vm", name, "click-text", "Start"})
	if err == nil {
		t.Fatal("ctlCommand succeeded")
	}
	if !strings.Contains(err.Error(), "requires a VNC endpoint") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "control socket") {
		t.Fatalf("error fell through to control socket: %v", err)
	}
}

func testCtlCommandWindowsQEMUVisualBypassesControlSocket(t *testing.T, command string, args ...string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName, vmDir = "", ""
	name := "qemu-win"
	dir := filepath.Join(vmconfig.BaseDir(), name+".covevm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}

	ctlArgs := append([]string{"-vm", name, command}, args...)
	err := ctlCommand(ctlArgs)
	if err == nil {
		t.Fatal("ctlCommand succeeded, want missing qemu monitor")
	}
	if !strings.Contains(err.Error(), "dial qemu monitor") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "control socket") {
		t.Fatalf("error fell through to control socket: %v", err)
	}
}

func TestQEMUKeySpecFromCtl(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"return", "ret"},
		{"36", "ret"},
		{"48", "tab"},
		{"49", "spc"},
		{"a", "a"},
	}
	for _, tt := range tests {
		got, err := qemuKeySpecFromCtl(tt.in)
		if err != nil {
			t.Fatalf("qemuKeySpecFromCtl(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("qemuKeySpecFromCtl(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCtlWindowsQEMUAgentStatusNoEndpoints(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	out := captureCtlQEMUStdout(t, func() error {
		return ctlWindowsQEMUAgentStatus(dir, false)
	})
	for _, want := range []string{
		"daemon agent: unavailable",
		"user agent: unavailable",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("agent status output missing %q:\n%s", want, out)
		}
	}
}

func TestWindowsClipboardCommandsUseAgentHelper(t *testing.T) {
	set := strings.Join(windowsClipboardSetCommand("hello"), " ")
	if !strings.Contains(set, `C:\ProgramData\cove\vz-agent.exe`) || !strings.Contains(set, "-clipboard-set-base64") {
		t.Fatalf("set clipboard command = %q", set)
	}
	get := strings.Join(windowsClipboardGetCommand(), " ")
	if !strings.Contains(get, `C:\ProgramData\cove\vz-agent.exe`) || !strings.Contains(get, "-clipboard-get") {
		t.Fatalf("get clipboard command = %q", get)
	}
}

func captureCtlQEMUStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = fn()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("function returned error: %v", err)
	}
	data, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
