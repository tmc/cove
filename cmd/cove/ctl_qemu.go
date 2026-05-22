package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	ocrx "github.com/tmc/apple/x/vzkit/ocr"
	"github.com/tmc/cove/internal/rfb"
	"github.com/tmc/cove/internal/vmconfig"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type windowsQEMUCTLStatus struct {
	Backend           string     `json:"backend"`
	State             string     `json:"state"`
	VMDir             string     `json:"vmDir"`
	MonitorSockPath   string     `json:"monitorSockPath"`
	GUI               string     `json:"gui,omitempty"`
	ScreenshotBackend string     `json:"screenshotBackend,omitempty"`
	TextBackend       string     `json:"textBackend,omitempty"`
	VNCEndpoint       string     `json:"vncEndpoint,omitempty"`
	VNCURL            string     `json:"vncURL,omitempty"`
	VNCAuth           string     `json:"vncAuth,omitempty"`
	GuestSession      string     `json:"guestSession,omitempty"`
	GuestUsername     string     `json:"guestUsername,omitempty"`
	GuestPassword     string     `json:"guestPassword,omitempty"`
	AgentEndpoint     string     `json:"agentEndpoint,omitempty"`
	AgentHealth       string     `json:"agentHealth,omitempty"`
	UserAgentEndpoint string     `json:"userAgentEndpoint,omitempty"`
	UserAgentHealth   string     `json:"userAgentHealth,omitempty"`
	CovePID           int        `json:"covePid,omitempty"`
	QEMUPID           int        `json:"qemuPid,omitempty"`
	StartedAt         *time.Time `json:"startedAt,omitempty"`
	ExitedAt          *time.Time `json:"exitedAt,omitempty"`
	ExitError         string     `json:"exitError,omitempty"`
}

type windowsQEMUAgentStatus struct {
	Backend           string `json:"backend"`
	AgentEndpoint     string `json:"agentEndpoint,omitempty"`
	AgentHealth       string `json:"agentHealth,omitempty"`
	UserAgentEndpoint string `json:"userAgentEndpoint,omitempty"`
	UserAgentHealth   string `json:"userAgentHealth,omitempty"`
}

func ctlMaybeHandleWindowsQEMU(vmDir, cmdType string, args []string, timeout, wait time.Duration, raw bool, outputFile string) (bool, error) {
	if !windowsQEMUCTLVM(vmDir) {
		return false, nil
	}
	switch cmdType {
	case "status":
		return true, ctlWindowsQEMUStatus(vmDir, raw)
	case "screenshot":
		return true, ctlWindowsQEMUScreenshot(vmDir, args, raw, outputFile)
	case "ocr":
		return true, ctlWindowsQEMUOCR(vmDir, args)
	case "key":
		return true, ctlWindowsQEMUKey(vmDir, args, raw)
	case "mouse":
		return true, ctlWindowsQEMUMouse(vmDir, args, raw)
	case "text":
		return true, ctlWindowsQEMUText(vmDir, args, raw)
	case "click-text":
		return true, ctlWindowsQEMUClickText(vmDir, args, raw)
	case "gui":
		return true, ctlWindowsQEMUGUI(vmDir, args, raw)
	case "vnc":
		return true, ctlWindowsQEMUVNC(vmDir, args, raw)
	case "stop":
		return true, ctlWindowsQEMUStop(vmDir, timeout)
	case "request-stop":
		return true, ctlWindowsQEMURequestStop(vmDir)
	case "agent-ping":
		return true, ctlWindowsQEMUAgentPing(vmDir, wait, timeout, raw)
	case "agent-status":
		return true, ctlWindowsQEMUAgentStatus(vmDir, raw)
	case "agent-exec", "agent-exec-stream":
		return true, ctlWindowsQEMUAgentExec(vmDir, args, wait, timeout, raw)
	case "agent-user-exec", "agent-user-exec-stream":
		return true, ctlWindowsQEMUUserAgentExec(vmDir, args, wait, timeout, raw)
	case "clipboard-push":
		return true, ctlWindowsQEMUClipboardPush(vmDir, args, wait, timeout, raw)
	case "clipboard-pull":
		return true, ctlWindowsQEMUClipboardPull(vmDir, wait, timeout, raw)
	case "clipboard-sync-to-guest":
		return true, ctlWindowsQEMUClipboardSyncToGuest(vmDir, wait, timeout, raw)
	case "clipboard-sync-from-guest":
		return true, ctlWindowsQEMUClipboardSyncFromGuest(vmDir, wait, timeout, raw)
	case "agent-shutdown":
		force := len(args) > 0 && args[0] == "force"
		return true, ctlWindowsQEMUAgentShutdown(vmDir, force, timeout)
	default:
		return true, fmt.Errorf("ctl %s is not supported for qemu windows VMs; supported commands: status, screenshot, ocr, key, mouse, text, click-text, gui, vnc, stop, request-stop, agent-ping, agent-status, exec, agent-exec, agent-exec-stream, agent-user-exec, clipboard-push, clipboard-pull, clipboard-sync-to-guest, clipboard-sync-from-guest, agent-shutdown", cmdType)
	}
}

func ctlWindowsQEMUScreenshot(vmDir string, args []string, raw bool, outputFile string) error {
	format, err := parseCtlScreenshotArgs(args, &outputFile)
	if err != nil {
		return err
	}
	img, err := captureWindowsQEMUImage(vmDir)
	if err != nil {
		return err
	}
	data, err := encodeWindowsQEMUImage(img, format)
	if err != nil {
		return err
	}
	resp := &controlpb.ControlResponse{
		Success: true,
		Data:    base64.StdEncoding.EncodeToString(data),
	}
	return ctlPrintResponse(resp, "screenshot", raw, outputFile)
}

func captureWindowsQEMUImage(vmDir string) (image.Image, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COVE_QEMU_SCREENSHOT_BACKEND"))) {
	case "", "auto":
		if img, err := captureWindowsQEMURFBImage(vmDir); err == nil {
			return img, nil
		}
		return captureWindowsQEMUMonitorImage(vmDir)
	case "rfb", "vnc":
		return captureWindowsQEMURFBImage(vmDir)
	case "monitor", "screendump":
		return captureWindowsQEMUMonitorImage(vmDir)
	default:
		return nil, fmt.Errorf("invalid COVE_QEMU_SCREENSHOT_BACKEND %q (must be auto, rfb, or monitor)", os.Getenv("COVE_QEMU_SCREENSHOT_BACKEND"))
	}
}

func captureWindowsQEMUMonitorImage(vmDir string) (image.Image, error) {
	q := newWindowsQEMUAutomation(vmDir)
	img, _, err := q.captureImage()
	return img, err
}

func captureWindowsQEMURFBImage(vmDir string) (image.Image, error) {
	status := readWindowsQEMUCTLStatus(vmDir)
	if status.VNCEndpoint == "" {
		return nil, fmt.Errorf("qemu vnc endpoint is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := rfb.Dial(ctx, status.VNCEndpoint)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	img, err := c.ReadUpdate(ctx)
	if err != nil {
		return nil, err
	}
	return img, nil
}

func encodeWindowsQEMUImage(img image.Image, format string) ([]byte, error) {
	var buf bytes.Buffer
	switch strings.ToLower(format) {
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encode png: %w", err)
		}
	case "jpeg", "jpg", "":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 60}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported screenshot format %q", format)
	}
	return buf.Bytes(), nil
}

func ctlWindowsQEMUOCR(vmDir string, args []string) error {
	region, err := parseOCROptions(args)
	if err != nil {
		return err
	}
	img, err := captureWindowsQEMUImage(vmDir)
	if err != nil {
		return err
	}
	return ctlOCRImage(img, region)
}

func ctlWindowsQEMUKey(vmDir string, args []string, raw bool) error {
	if len(args) < 1 {
		return fmt.Errorf("key command requires keycode or key name (e.g., return, tab, space)")
	}
	if len(args) >= 2 && args[1] == "up" {
		return ctlPrintResponse(&controlpb.ControlResponse{Success: true}, "key", raw, "")
	}
	spec, err := qemuKeySpecFromCtl(args[0])
	if err != nil {
		return err
	}
	q := newWindowsQEMUAutomation(vmDir)
	if err := q.monitorCommand("sendkey " + spec); err != nil {
		return err
	}
	return ctlPrintResponse(&controlpb.ControlResponse{Success: true}, "key", raw, "")
}

func ctlWindowsQEMUText(vmDir string, args []string, raw bool) error {
	if len(args) < 1 {
		return fmt.Errorf("text command requires string")
	}
	text := strings.Join(args, " ")
	if err := typeWindowsQEMUText(vmDir, text); err != nil {
		return err
	}
	return ctlPrintResponse(&controlpb.ControlResponse{Success: true}, "text", raw, "")
}

func typeWindowsQEMUText(vmDir, text string) error {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COVE_QEMU_TEXT_BACKEND"))) {
	case "", "auto":
		if err := typeWindowsQEMURFBText(vmDir, text); err == nil {
			return nil
		}
		return typeWindowsQEMUMonitorText(vmDir, text)
	case "rfb", "vnc":
		return typeWindowsQEMURFBText(vmDir, text)
	case "monitor", "sendkey":
		return typeWindowsQEMUMonitorText(vmDir, text)
	default:
		return fmt.Errorf("invalid COVE_QEMU_TEXT_BACKEND %q (must be auto, rfb, or monitor)", os.Getenv("COVE_QEMU_TEXT_BACKEND"))
	}
}

func typeWindowsQEMUMonitorText(vmDir, text string) error {
	q := newWindowsQEMUAutomation(vmDir)
	return q.typeText(text)
}

func typeWindowsQEMURFBText(vmDir, text string) error {
	status := readWindowsQEMUCTLStatus(vmDir)
	if status.VNCEndpoint == "" {
		return fmt.Errorf("qemu vnc endpoint is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := rfb.Dial(ctx, status.VNCEndpoint)
	if err != nil {
		return fmt.Errorf("connect qemu vnc for text input: %w", err)
	}
	defer c.Close()
	return c.TypeText(text)
}

func ctlWindowsQEMUMouse(vmDir string, args []string, raw bool) error {
	if len(args) < 3 {
		return fmt.Errorf("mouse command requires: x y action")
	}
	x, err := strconv.ParseFloat(args[0], 64)
	if err != nil {
		return fmt.Errorf("parse mouse x: %w", err)
	}
	y, err := strconv.ParseFloat(args[1], 64)
	if err != nil {
		return fmt.Errorf("parse mouse y: %w", err)
	}
	action := strings.ToLower(args[2])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status := readWindowsQEMUCTLStatus(vmDir)
	if status.VNCEndpoint == "" {
		return fmt.Errorf("qemu vnc endpoint is unavailable; restart with -vnc :5901 to use mouse input")
	}
	c, err := rfb.Dial(ctx, status.VNCEndpoint)
	if err != nil {
		return fmt.Errorf("connect qemu vnc for mouse input: %w", err)
	}
	defer c.Close()
	p := qemuRFBMousePoint(c.Size(), x, y)
	switch action {
	case "move":
		err = c.Pointer(p.X, p.Y, 0)
	case "down":
		err = c.Pointer(p.X, p.Y, 1)
	case "up":
		err = c.Pointer(p.X, p.Y, 0)
	case "click":
		if err = c.Pointer(p.X, p.Y, 0); err == nil {
			time.Sleep(20 * time.Millisecond)
			err = c.Pointer(p.X, p.Y, 1)
		}
		if err == nil {
			time.Sleep(50 * time.Millisecond)
			err = c.Pointer(p.X, p.Y, 0)
		}
	default:
		return fmt.Errorf("unknown mouse action: %s", action)
	}
	if err != nil {
		return err
	}
	return ctlPrintResponse(&controlpb.ControlResponse{Success: true}, "mouse", raw, "")
}

func qemuRFBMousePoint(size image.Point, x, y float64) image.Point {
	if x >= 0 && x <= 1 && y >= 0 && y <= 1 {
		return image.Point{
			X: int(x * float64(size.X)),
			Y: int(y * float64(size.Y)),
		}
	}
	return image.Point{X: int(x), Y: int(y)}
}

func ctlWindowsQEMUClickText(vmDir string, args []string, raw bool) error {
	text, region, timeout, err := parseClickTextOptions(args)
	if err != nil {
		return err
	}
	if readWindowsQEMUCTLStatus(vmDir).VNCEndpoint == "" {
		return ctlWindowsQEMUClickTextKeyboard(vmDir, text, region, timeout, raw)
	}
	opts := ocrx.SearchOptions{}
	if region != "" {
		var err error
		opts, err = ocrx.ParseSearchOptions(region)
		if err != nil {
			return err
		}
	}
	ocr := ocrx.NewService(false)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, err := captureWindowsQEMUImage(vmDir)
		if err == nil {
			x, y, found := ocr.FindTextWithOptions(img, text, opts)
			if found {
				if err := clickWindowsQEMURFBPixel(vmDir, int(x), int(y)); err != nil {
					return err
				}
				return ctlPrintResponse(&controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("clicked %q", text)}, "click-text", raw, "")
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout: text %q not found", text)
}

func ctlWindowsQEMUClickTextKeyboard(vmDir, text, region string, timeout time.Duration, raw bool) error {
	key, ok := qemuDefaultActionForText(text)
	if !ok {
		return fmt.Errorf("qemu windows click-text for arbitrary text %q requires a VNC endpoint; restart with -vnc :5901", text)
	}
	opts := ocrx.SearchOptions{}
	if region != "" {
		var err error
		opts, err = ocrx.ParseSearchOptions(region)
		if err != nil {
			return err
		}
	}
	q := newWindowsQEMUAutomation(vmDir)
	q.ocr = ocrx.NewService(false)
	if err := q.waitForText(text, timeout, opts); err != nil {
		return err
	}
	if err := q.sendKey(key); err != nil {
		return err
	}
	return ctlPrintResponse(&controlpb.ControlResponse{Success: true}, "click-text", raw, "")
}

func clickWindowsQEMURFBPixel(vmDir string, x, y int) error {
	status := readWindowsQEMUCTLStatus(vmDir)
	if status.VNCEndpoint == "" {
		return fmt.Errorf("qemu vnc endpoint is unavailable; restart with -vnc :5901 to use mouse input")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := rfb.Dial(ctx, status.VNCEndpoint)
	if err != nil {
		return fmt.Errorf("connect qemu vnc for mouse input: %w", err)
	}
	defer c.Close()
	if err := c.Pointer(x, y, 0); err != nil {
		return err
	}
	time.Sleep(20 * time.Millisecond)
	if err := c.Pointer(x, y, 1); err != nil {
		return err
	}
	time.Sleep(50 * time.Millisecond)
	return c.Pointer(x, y, 0)
}

func qemuKeySpecFromCtl(spec string) (string, error) {
	if key, ok := qemuKeyCodeNames[strings.TrimSpace(spec)]; ok {
		return qemuKeySpec(key)
	}
	return qemuKeySpec(spec)
}

var qemuKeyCodeNames = map[string]string{
	"36":  "return",
	"48":  "tab",
	"49":  "space",
	"53":  "escape",
	"51":  "delete",
	"126": "up",
	"125": "down",
	"123": "left",
	"124": "right",
}

func newWindowsQEMUAutomation(vmDir string) *qemuVZScriptAutomation {
	return &qemuVZScriptAutomation{
		monitor:     qemuMonitorPathForVMDir(vmDir),
		artifactDir: filepath.Join(vmDir, "qemu", "screenshots"),
	}
}

func windowsQEMUCTLVM(vmDir string) bool {
	if strings.TrimSpace(vmDir) == "" {
		return false
	}
	if vmconfig.DetectOSType(vmDir) != "Windows" {
		return false
	}
	if _, err := os.Stat(filepath.Join(vmDir, "windows.qcow2")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(vmDir, "qemu", "metadata.json")); err == nil {
		return true
	}
	return false
}

func ctlWindowsQEMUStatus(vmDir string, raw bool) error {
	return writeWindowsQEMUStatus(os.Stdout, vmDir, raw)
}

func writeWindowsQEMUStatus(w io.Writer, vmDir string, raw bool) error {
	status := readWindowsQEMUCTLStatus(vmDir)
	if raw {
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal qemu status: %w", err)
		}
		fmt.Fprintln(w, string(data))
		return nil
	}
	fmt.Fprintf(w, "state:   %s\n", status.State)
	fmt.Fprintf(w, "backend: %s\n", status.Backend)
	fmt.Fprintf(w, "vmDir:   %s\n", status.VMDir)
	fmt.Fprintf(w, "monitor: %s\n", status.MonitorSockPath)
	if status.AgentEndpoint != "" {
		fmt.Fprintf(w, "agent:   %s\n", status.AgentEndpoint)
		if status.AgentHealth != "" {
			fmt.Fprintf(w, "agentHealth: %s\n", status.AgentHealth)
		}
	}
	if status.UserAgentEndpoint != "" {
		fmt.Fprintf(w, "userAgent: %s\n", status.UserAgentEndpoint)
		if status.UserAgentHealth != "" {
			fmt.Fprintf(w, "userAgentHealth: %s\n", status.UserAgentHealth)
		}
	}
	if hint := qemuAgentStatusHint(status); hint != "" {
		fmt.Fprintf(w, "hint:    %s\n", hint)
	}
	if status.VNCEndpoint != "" {
		fmt.Fprintf(w, "gui:     %s\n", status.GUI)
		fmt.Fprintf(w, "vnc:     %s\n", status.VNCEndpoint)
		fmt.Fprintf(w, "vncURL:  %s\n", status.VNCURL)
		fmt.Fprintf(w, "vncAuth: %s\n", status.VNCAuth)
	}
	if status.ScreenshotBackend != "" {
		fmt.Fprintf(w, "screens: %s\n", status.ScreenshotBackend)
	}
	if status.TextBackend != "" {
		fmt.Fprintf(w, "text:    %s\n", status.TextBackend)
	}
	if status.GuestSession != "" {
		fmt.Fprintf(w, "session: %s\n", status.GuestSession)
	}
	if status.GuestUsername != "" {
		fmt.Fprintf(w, "user:    %s\n", status.GuestUsername)
	}
	if status.GuestPassword != "" {
		fmt.Fprintf(w, "pass:    %s\n", status.GuestPassword)
	}
	if status.CovePID != 0 {
		fmt.Fprintf(w, "covePid: %d\n", status.CovePID)
	}
	if status.QEMUPID != 0 {
		fmt.Fprintf(w, "qemuPid: %d\n", status.QEMUPID)
	}
	if status.ExitError != "" {
		fmt.Fprintf(w, "error:   %s\n", status.ExitError)
	}
	return nil
}

func ctlWindowsQEMUAgentStatus(vmDir string, raw bool) error {
	status := readWindowsQEMUCTLStatus(vmDir)
	report := windowsQEMUAgentStatus{
		Backend:           "qemu-hvf",
		AgentEndpoint:     status.AgentEndpoint,
		AgentHealth:       status.AgentHealth,
		UserAgentEndpoint: status.UserAgentEndpoint,
		UserAgentHealth:   status.UserAgentHealth,
	}
	if raw {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal qemu agent status: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}
	if report.AgentEndpoint != "" {
		fmt.Printf("daemon agent: %s\n", report.AgentHealth)
		fmt.Printf("daemon endpoint: %s\n", report.AgentEndpoint)
	} else {
		fmt.Println("daemon agent: unavailable")
	}
	if report.UserAgentEndpoint != "" {
		fmt.Printf("user agent: %s\n", report.UserAgentHealth)
		fmt.Printf("user endpoint: %s\n", report.UserAgentEndpoint)
	} else {
		fmt.Println("user agent: unavailable")
	}
	if hint := qemuAgentStatusHint(status); hint != "" {
		fmt.Printf("hint: %s\n", hint)
	}
	return nil
}

func readWindowsQEMUCTLStatus(vmDir string) windowsQEMUCTLStatus {
	status := windowsQEMUCTLStatus{
		Backend:           "qemu-hvf",
		State:             detectVMState(vmDir),
		VMDir:             vmDir,
		MonitorSockPath:   qemuMonitorPathForVMDir(vmDir),
		AgentEndpoint:     qemuAgentAddressForVMDir(vmDir),
		UserAgentEndpoint: qemuUserAgentAddressForVMDir(vmDir),
	}
	metadata := qemuMetadataForVMDir(vmDir)
	status.VNCEndpoint = metadata.VNCEndpoint
	status.VNCURL = metadata.VNCURL
	status.GuestUsername = metadata.GuestUsername
	status.GuestPassword = metadata.GuestPassword
	process := readWindowsQEMUProcessForCTL(vmDir)
	if process.State != "" {
		status.State = strings.TrimSpace(process.State)
	}
	if windowsQEMUMonitorReachable(status.MonitorSockPath) {
		status.State = "running"
	}
	if status.State == "" {
		status.State = "stopped"
	}
	status.CovePID = process.CovePID
	status.QEMUPID = process.QEMUPID
	if !process.StartedAt.IsZero() {
		startedAt := process.StartedAt
		status.StartedAt = &startedAt
	}
	status.ExitedAt = process.ExitedAt
	status.ExitError = process.ExitError
	status.AgentHealth = qemuAgentHealth(status.AgentEndpoint)
	status.UserAgentHealth = qemuUserAgentHealth(status.UserAgentEndpoint)
	status.GUI = qemuGUIDisplayMode(status)
	status.ScreenshotBackend = qemuResolvedBackend("COVE_QEMU_SCREENSHOT_BACKEND", status, "screendump")
	status.TextBackend = qemuResolvedBackend("COVE_QEMU_TEXT_BACKEND", status, "sendkey")
	status.VNCAuth = qemuVNCAuth(status)
	status.GuestSession = qemuGuestSession(status)
	return status
}

func qemuGUIDisplayMode(status windowsQEMUCTLStatus) string {
	if windowsQEMUViewerRunning(status.VMDir) {
		return "qemu-vnc-cove"
	}
	if status.VNCURL != "" {
		return "qemu-vnc-external"
	}
	return "qemu-cocoa-or-headless"
}

func qemuVNCAuth(status windowsQEMUCTLStatus) string {
	if status.VNCURL == "" {
		return ""
	}
	return "none"
}

func qemuResolvedBackend(envName string, status windowsQEMUCTLStatus, monitorName string) string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envName))) {
	case "rfb", "vnc":
		return "rfb"
	case "monitor", "screendump", "sendkey":
		return monitorName
	case "", "auto":
		if status.VNCURL != "" {
			return "rfb"
		}
		return monitorName
	default:
		return "invalid:" + os.Getenv(envName)
	}
}

func qemuGuestSession(status windowsQEMUCTLStatus) string {
	if status.UserAgentHealth == "connected" {
		return "logged-in user agent connected; Windows may not show a login prompt"
	}
	return ""
}

func qemuAgentStatusHint(status windowsQEMUCTLStatus) string {
	if status.AgentEndpoint == "" && status.UserAgentEndpoint == "" {
		return ""
	}
	if strings.HasPrefix(status.AgentHealth, "unresponsive:") || strings.HasPrefix(status.UserAgentHealth, "unresponsive:") {
		return "if Windows is running, the guest firewall may be blocking QEMU host-forwarded agent ports"
	}
	return ""
}

func readWindowsQEMUProcessForCTL(vmDir string) windowsQEMUProcessMetadata {
	data, err := os.ReadFile(filepath.Join(vmDir, "qemu", "process.json"))
	if err != nil {
		return windowsQEMUProcessMetadata{}
	}
	var process windowsQEMUProcessMetadata
	if err := json.Unmarshal(data, &process); err != nil {
		return windowsQEMUProcessMetadata{}
	}
	return process
}

func windowsQEMUMonitorReachable(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func qemuAgentHealth(address string) string {
	if strings.TrimSpace(address) == "" {
		return ""
	}
	version, err := qemuAgentPing(address, 750*time.Millisecond)
	if err != nil {
		return "unresponsive: " + err.Error()
	}
	if strings.TrimSpace(version) == "" {
		return "connected"
	}
	return "connected: " + strings.TrimSpace(version)
}

func qemuUserAgentHealth(address string) string {
	if strings.TrimSpace(address) == "" {
		return ""
	}
	_, _, exitCode, err := qemuUserAgentExec(address, []string{"cmd.exe", "/c", "echo ok"}, nil, 750*time.Millisecond)
	if err != nil {
		return "unresponsive: " + err.Error()
	}
	if exitCode != 0 {
		return fmt.Sprintf("unresponsive: probe exited with code %d", exitCode)
	}
	return "connected"
}

func ctlWindowsQEMUGUI(vmDir string, args []string, raw bool) error {
	if len(args) == 0 {
		return fmt.Errorf("gui requires action: status, open, close, or diagnose")
	}
	status := readWindowsQEMUCTLStatus(vmDir)
	switch args[0] {
	case "status":
		return printWindowsQEMUGUIStatus(status, raw)
	case "open":
		return ctlWindowsQEMUOpenGUI(vmDir, status)
	case "close":
		if windowsQEMUViewerRunning(vmDir) {
			return ctlWindowsQEMUCloseCoveViewer(vmDir)
		}
		return fmt.Errorf("qemu gui close cannot close an external VNC viewer window; close the VNC viewer window directly")
	case "diagnose":
		return ctlWindowsQEMUGUIDiagnose(vmDir, status, raw)
	default:
		return fmt.Errorf("unknown gui action: %s (use status, open, close, or diagnose)", args[0])
	}
}

func printWindowsQEMUGUIStatus(status windowsQEMUCTLStatus, raw bool) error {
	if raw {
		data, err := json.MarshalIndent(status, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal qemu gui status: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("backend: qemu-hvf\n")
	fmt.Printf("state:   %s\n", status.State)
	if status.VNCURL != "" {
		fmt.Printf("gui:     %s\n", status.GUI)
		fmt.Printf("vncURL:  %s\n", status.VNCURL)
		fmt.Printf("vncAuth: %s; Windows credentials below are for the guest login\n", status.VNCAuth)
	} else {
		fmt.Printf("gui:     qemu-cocoa-or-headless\n")
		fmt.Println("hint:    restart with -vnc :5901 to make gui open available")
	}
	if status.GuestSession != "" {
		fmt.Printf("session: %s\n", status.GuestSession)
	}
	if status.ScreenshotBackend != "" {
		fmt.Printf("screens: %s\n", status.ScreenshotBackend)
	}
	if status.TextBackend != "" {
		fmt.Printf("text:    %s\n", status.TextBackend)
	}
	if status.GuestUsername != "" {
		fmt.Printf("user:    %s\n", status.GuestUsername)
	}
	if status.GuestPassword != "" {
		fmt.Printf("pass:    %s\n", status.GuestPassword)
	}
	return nil
}

func ctlWindowsQEMUGUIDiagnose(vmDir string, status windowsQEMUCTLStatus, raw bool) error {
	img, err := captureWindowsQEMUImage(vmDir)
	if err != nil {
		return fmt.Errorf("capture qemu gui screenshot: %w", err)
	}
	path, err := writeWindowsQEMUGUIDiagnoseScreenshot(vmDir, img)
	if err != nil {
		return err
	}
	ocr := ocrx.NewService(false)
	screen := DetectScreenStateOCR(img, ocr).String()
	if raw {
		data, err := json.MarshalIndent(struct {
			Status     windowsQEMUCTLStatus `json:"status"`
			Screenshot string               `json:"screenshot"`
			Screen     string               `json:"screen"`
		}{
			Status:     status,
			Screenshot: path,
			Screen:     screen,
		}, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal qemu gui diagnose: %w", err)
		}
		fmt.Println(string(data))
		return nil
	}
	if err := printWindowsQEMUGUIStatus(status, false); err != nil {
		return err
	}
	fmt.Printf("screenshot: %s\n", path)
	fmt.Printf("screen:     %s\n", screen)
	if status.GuestSession != "" {
		fmt.Println("hint:       Windows is already logged in when the user agent is connected; a username prompt may not appear")
	}
	return nil
}

func writeWindowsQEMUGUIDiagnoseScreenshot(vmDir string, img image.Image) (string, error) {
	dir := filepath.Join(vmDir, "qemu", "screenshots")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create qemu screenshot directory: %w", err)
	}
	path := filepath.Join(dir, "gui-diagnose-"+time.Now().Format("20060102-150405")+".jpg")
	data, err := encodeWindowsQEMUImage(img, "jpeg")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("write qemu gui screenshot: %w", err)
	}
	return path, nil
}

func ctlWindowsQEMUOpenGUI(vmDir string, status windowsQEMUCTLStatus) error {
	switch qemuGUIOpenMode(status) {
	case "external":
		return ctlWindowsQEMUOpenVNC(status)
	case "cove":
		return ctlWindowsQEMUOpenCoveViewer(vmDir, status)
	default:
		return fmt.Errorf("unknown qemu gui open mode %q", qemuGUIOpenMode(status))
	}
}

func qemuGUIOpenMode(status windowsQEMUCTLStatus) string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COVE_QEMU_GUI_VIEWER"))) {
	case "external", "vnc", "system":
		return "external"
	}
	if status.VNCEndpoint != "" {
		return "cove"
	}
	return "external"
}

func ctlWindowsQEMUOpenCoveViewer(vmDir string, status windowsQEMUCTLStatus) error {
	if status.VNCEndpoint == "" {
		return fmt.Errorf("qemu vnc is not enabled; restart with -vnc :5901")
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find cove executable: %w", err)
	}
	name := vmconfig.NameForPath(vmDir)
	if name == "" {
		return fmt.Errorf("qemu display viewer requires a VM name")
	}
	cmd := exec.Command(exe, "qemu-display", "-vm", name)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := os.MkdirAll(filepath.Join(vmDir, "qemu"), 0755); err == nil {
		if log, logErr := os.OpenFile(filepath.Join(vmDir, "qemu", "viewer.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); logErr == nil {
			defer log.Close()
			cmd.Stdout = log
			cmd.Stderr = log
		}
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cove qemu display viewer: %w", err)
	}
	if err := writeWindowsQEMUViewerPID(vmDir, cmd.Process.Pid); err != nil {
		return err
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release cove qemu display viewer: %w", err)
	}
	fmt.Printf("opened Cove QEMU display viewer for %s\n", name)
	return nil
}

func windowsQEMUViewerPIDPath(vmDir string) string {
	return filepath.Join(vmDir, "qemu", "viewer.pid")
}

func writeWindowsQEMUViewerPID(vmDir string, pid int) error {
	if err := os.MkdirAll(filepath.Join(vmDir, "qemu"), 0755); err != nil {
		return fmt.Errorf("create qemu metadata directory: %w", err)
	}
	path := windowsQEMUViewerPIDPath(vmDir)
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0644); err != nil {
		return fmt.Errorf("write qemu display viewer pid: %w", err)
	}
	return nil
}

func windowsQEMUViewerRunning(vmDir string) bool {
	pid, ok := windowsQEMUViewerPID(vmDir)
	if !ok {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil {
		_ = os.Remove(windowsQEMUViewerPIDPath(vmDir))
		return false
	}
	return true
}

func windowsQEMUViewerPID(vmDir string) (int, bool) {
	data, err := os.ReadFile(windowsQEMUViewerPIDPath(vmDir))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func ctlWindowsQEMUCloseCoveViewer(vmDir string) error {
	pid, ok := windowsQEMUViewerPID(vmDir)
	if !ok {
		return fmt.Errorf("qemu cove display viewer is not running")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		_ = os.Remove(windowsQEMUViewerPIDPath(vmDir))
		return fmt.Errorf("close cove qemu display viewer: %w", err)
	}
	_ = os.Remove(windowsQEMUViewerPIDPath(vmDir))
	fmt.Println("closed Cove QEMU display viewer")
	return nil
}

func ctlWindowsQEMUVNC(vmDir string, args []string, raw bool) error {
	if len(args) == 0 {
		return fmt.Errorf("vnc requires action: status or open")
	}
	status := readWindowsQEMUCTLStatus(vmDir)
	switch args[0] {
	case "status":
		if raw {
			data, err := json.MarshalIndent(status, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal qemu vnc status: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}
		if status.VNCEndpoint == "" {
			fmt.Println("vnc: disabled")
			fmt.Println("hint: restart with -vnc :5901 to expose a local QEMU console")
			return nil
		}
		fmt.Printf("vnc:    %s\n", status.VNCEndpoint)
		fmt.Printf("vncURL: %s\n", status.VNCURL)
		fmt.Printf("auth:   %s; Windows credentials are for the guest login, not VNC\n", status.VNCAuth)
		return nil
	case "open":
		return ctlWindowsQEMUOpenVNC(status)
	default:
		return fmt.Errorf("unknown vnc action: %s (use status or open)", args[0])
	}
}

func ctlWindowsQEMUOpenVNC(status windowsQEMUCTLStatus) error {
	if status.VNCURL == "" {
		return fmt.Errorf("qemu vnc is not enabled; restart with -vnc :5901")
	}
	if err := exec.Command("open", status.VNCURL).Run(); err != nil {
		return fmt.Errorf("open %s: %w", status.VNCURL, err)
	}
	fmt.Printf("opened %s\n", status.VNCURL)
	return nil
}

func ctlWindowsQEMUStop(vmDir string, timeout time.Duration) error {
	monitor := qemuMonitorPathForVMDir(vmDir)
	if err := qemuMonitorCommand(monitor, "quit"); err != nil {
		return fmt.Errorf("qemu monitor unavailable; vm is not running or has exited: %w", err)
	}
	if err := waitWindowsQEMUCTLStopped(vmDir, timeout); err != nil {
		return err
	}
	fmt.Println("stopped")
	return nil
}

func ctlWindowsQEMURequestStop(vmDir string) error {
	if err := qemuMonitorCommand(qemuMonitorPathForVMDir(vmDir), "system_powerdown"); err != nil {
		return fmt.Errorf("qemu monitor unavailable; vm is not running or has exited: %w", err)
	}
	fmt.Println("stop requested (ACPI power button sent)")
	return nil
}

func ctlWindowsQEMUAgentPing(vmDir string, wait, timeout time.Duration, raw bool) error {
	version, err := waitWindowsQEMUAgentPing(qemuAgentAddressForVMDir(vmDir), wait, timeout)
	if err != nil {
		return err
	}
	resp := &controlpb.ControlResponse{Success: true, Data: "agent version: " + version}
	return ctlPrintResponse(resp, "agent-ping", raw, "")
}

func ctlWindowsQEMUAgentExec(vmDir string, args []string, wait, timeout time.Duration, raw bool) error {
	if len(args) == 0 {
		return fmt.Errorf("exec requires at least one argument")
	}
	address := qemuAgentAddressForVMDir(vmDir)
	if _, err := waitWindowsQEMUAgentPing(address, wait, timeout); err != nil {
		return err
	}
	stdout, stderr, exitCode, err := qemuAgentExecStream(vzscriptConfig{qemuAgentAddress: address}, args, timeout, nil, nil)
	if err != nil {
		return err
	}
	resp := &controlpb.ControlResponse{
		Success: exitCode == 0,
		Result: &controlpb.ControlResponse_AgentExecResult{
			AgentExecResult: &controlpb.AgentExecResponse{
				ExitCode: exitCode,
				Stdout:   stdout,
				Stderr:   stderr,
			},
		},
	}
	if raw {
		return ctlPrintResponse(resp, "agent-exec", true, "")
	}
	if stdout != "" {
		fmt.Print(stdout)
	}
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	if exitCode != 0 {
		return fmt.Errorf("command exited with code %d", exitCode)
	}
	return nil
}

func ctlWindowsQEMUUserAgentExec(vmDir string, args []string, wait, timeout time.Duration, raw bool) error {
	if len(args) == 0 {
		return fmt.Errorf("agent-user-exec requires at least one argument")
	}
	address := qemuUserAgentAddressForVMDir(vmDir)
	if err := waitWindowsQEMUUserAgentReady(address, wait, timeout); err != nil {
		return err
	}
	stdout, stderr, exitCode, err := qemuUserAgentExec(address, args, nil, timeout)
	if err != nil {
		return err
	}
	resp := &controlpb.ControlResponse{
		Success: exitCode == 0,
		Result: &controlpb.ControlResponse_AgentExecResult{
			AgentExecResult: &controlpb.AgentExecResponse{
				ExitCode: exitCode,
				Stdout:   stdout,
				Stderr:   stderr,
			},
		},
	}
	if raw {
		return ctlPrintResponse(resp, "agent-user-exec", true, "")
	}
	if stdout != "" {
		fmt.Print(stdout)
	}
	if stderr != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	if exitCode != 0 {
		return fmt.Errorf("command exited with code %d", exitCode)
	}
	return nil
}

func ctlWindowsQEMUClipboardPush(vmDir string, args []string, wait, timeout time.Duration, raw bool) error {
	text, err := clipboardTextFromArgsOrStdin(args)
	if err != nil {
		return err
	}
	if err := windowsQEMUSetGuestClipboard(vmDir, text, wait, timeout); err != nil {
		return err
	}
	resp := &controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("guest clipboard updated (%d bytes)", len(text))}
	if raw {
		return ctlPrintResponse(resp, "clipboard-push", true, "")
	}
	fmt.Println(resp.Data)
	return nil
}

func ctlWindowsQEMUClipboardPull(vmDir string, wait, timeout time.Duration, raw bool) error {
	text, err := windowsQEMUGetGuestClipboard(vmDir, wait, timeout)
	if err != nil {
		return err
	}
	if raw {
		return ctlPrintResponse(&controlpb.ControlResponse{Success: true, Data: text}, "clipboard-pull", true, "")
	}
	fmt.Print(text)
	return nil
}

func ctlWindowsQEMUClipboardSyncToGuest(vmDir string, wait, timeout time.Duration, raw bool) error {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return fmt.Errorf("pbpaste: %w", err)
	}
	text := string(out)
	if err := windowsQEMUSetGuestClipboard(vmDir, text, wait, timeout); err != nil {
		return err
	}
	resp := &controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("guest clipboard updated from host clipboard (%d bytes)", len(text))}
	if raw {
		return ctlPrintResponse(resp, "clipboard-sync-to-guest", true, "")
	}
	fmt.Println(resp.Data)
	return nil
}

func ctlWindowsQEMUClipboardSyncFromGuest(vmDir string, wait, timeout time.Duration, raw bool) error {
	text, err := windowsQEMUGetGuestClipboard(vmDir, wait, timeout)
	if err != nil {
		return err
	}
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pbcopy: %w", err)
	}
	resp := &controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("host clipboard updated from guest clipboard (%d bytes)", len(text))}
	if raw {
		return ctlPrintResponse(resp, "clipboard-sync-from-guest", true, "")
	}
	fmt.Println(resp.Data)
	return nil
}

func clipboardTextFromArgsOrStdin(args []string) (string, error) {
	if len(args) > 0 {
		return strings.Join(args, " "), nil
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return "", fmt.Errorf("stat stdin: %w", err)
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("clipboard-push requires text arguments or stdin")
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(data), nil
}

func windowsQEMUSetGuestClipboard(vmDir, text string, wait, timeout time.Duration) error {
	address := qemuUserAgentAddressForVMDir(vmDir)
	if err := waitWindowsQEMUUserAgentReady(address, wait, timeout); err != nil {
		return err
	}
	stdout, stderr, exitCode, err := qemuUserAgentExec(address, windowsClipboardSetCommand(text), nil, timeout)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return fmt.Errorf("set guest clipboard exited with code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout, stderr)
	}
	return nil
}

func windowsQEMUGetGuestClipboard(vmDir string, wait, timeout time.Duration) (string, error) {
	address := qemuUserAgentAddressForVMDir(vmDir)
	if err := waitWindowsQEMUUserAgentReady(address, wait, timeout); err != nil {
		return "", err
	}
	stdout, stderr, exitCode, err := qemuUserAgentExec(address, windowsClipboardGetCommand(), nil, timeout)
	if err != nil {
		return "", err
	}
	if exitCode != 0 {
		return "", fmt.Errorf("get guest clipboard exited with code %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout, stderr)
	}
	return stdout, nil
}

func windowsClipboardSetCommand(text string) []string {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	return []string{`C:\ProgramData\cove\vz-agent.exe`, "-clipboard-set-base64", encoded}
}

func windowsClipboardGetCommand() []string {
	return []string{`C:\ProgramData\cove\vz-agent.exe`, "-clipboard-get"}
}

func ctlWindowsQEMUAgentShutdown(vmDir string, force bool, timeout time.Duration) error {
	args := []string{"shutdown.exe", "/s", "/t", "0"}
	if force {
		args = append(args, "/f")
	}
	if err := ctlWindowsQEMUAgentExec(vmDir, args, 0, timeout, false); err != nil {
		return err
	}
	if err := waitWindowsQEMUCTLStopped(vmDir, timeout); err != nil {
		return err
	}
	fmt.Println("stopped")
	return nil
}

func waitWindowsQEMUAgentPing(address string, wait, timeout time.Duration) (string, error) {
	if strings.TrimSpace(address) == "" {
		return "", fmt.Errorf("qemu windows agent endpoint is unavailable")
	}
	if wait <= 0 {
		return qemuAgentPing(address, timeout)
	}
	deadline := time.Now().Add(wait)
	attempt := 0
	for {
		attempt++
		version, err := qemuAgentPing(address, timeout)
		if err == nil {
			return version, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("qemu windows agent not ready after %s: %w", wait, err)
		}
		if attempt == 1 {
			fmt.Fprintf(os.Stderr, "Connecting to QEMU Windows agent (waiting up to %s)...\n", wait)
		}
		time.Sleep(2 * time.Second)
	}
}

func waitWindowsQEMUUserAgentReady(address string, wait, timeout time.Duration) error {
	if strings.TrimSpace(address) == "" {
		return fmt.Errorf("qemu windows user agent endpoint is unavailable")
	}
	probe := []string{"cmd.exe", "/c", "echo ok"}
	if wait <= 0 {
		_, _, exitCode, err := qemuUserAgentExec(address, probe, nil, timeout)
		if err != nil {
			return err
		}
		if exitCode != 0 {
			return fmt.Errorf("qemu windows user agent probe exited with code %d", exitCode)
		}
		return nil
	}
	deadline := time.Now().Add(wait)
	attempt := 0
	for {
		attempt++
		_, _, exitCode, err := qemuUserAgentExec(address, probe, nil, timeout)
		if err == nil && exitCode == 0 {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("probe exited with code %d", exitCode)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("qemu windows user agent not ready after %s: %w", wait, err)
		}
		if attempt == 1 {
			fmt.Fprintf(os.Stderr, "Connecting to QEMU Windows user agent (waiting up to %s)...\n", wait)
		}
		time.Sleep(2 * time.Second)
	}
}

func waitWindowsQEMUCTLStopped(vmDir string, timeout time.Duration) error {
	process := readWindowsQEMUProcessForCTL(vmDir)
	if process.QEMUPID == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		if !processLive(process.QEMUPID) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for qemu pid %d to exit", process.QEMUPID)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
