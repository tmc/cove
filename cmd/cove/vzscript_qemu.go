package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ocrx "github.com/tmc/apple/x/vzkit/ocr"
	"rsc.io/script"
)

type qemuVZScriptAutomation struct {
	monitor     string
	artifactDir string
	ocr         *ocrx.Service
	verbose     bool
}

func newQEMUVZScriptAutomation(cfg vzscriptConfig) *qemuVZScriptAutomation {
	if strings.TrimSpace(cfg.qemuMonitorPath) == "" {
		return nil
	}
	artifactDir := cfg.qemuArtifactDir
	if artifactDir == "" {
		artifactDir = filepath.Join(filepath.Dir(cfg.qemuMonitorPath), "vzscript")
	}
	return &qemuVZScriptAutomation{
		monitor:     cfg.qemuMonitorPath,
		artifactDir: artifactDir,
		ocr:         ocrx.NewService(cfg.verbose),
		verbose:     cfg.verbose,
	}
}

func qemuMonitorPathForVMDir(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "qemu", "monitor.sock")
}

func qemuAgentAddressForVMDir(dir string) string {
	metadata := qemuMetadataForVMDir(dir)
	if metadata.AgentHostAddress == "" || metadata.AgentHostPort == 0 {
		return ""
	}
	return net.JoinHostPort(metadata.AgentHostAddress, strconv.Itoa(metadata.AgentHostPort))
}

func qemuUserAgentAddressForVMDir(dir string) string {
	metadata := qemuMetadataForVMDir(dir)
	if metadata.UserAgentHostAddress == "" || metadata.UserAgentHostPort == 0 {
		return ""
	}
	return net.JoinHostPort(metadata.UserAgentHostAddress, strconv.Itoa(metadata.UserAgentHostPort))
}

func qemuMetadataForVMDir(dir string) windowsQEMUMetadata {
	if dir == "" {
		return windowsQEMUMetadata{}
	}
	data, err := os.ReadFile(filepath.Join(dir, "qemu", "metadata.json"))
	if err != nil {
		return windowsQEMUMetadata{}
	}
	var metadata windowsQEMUMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return windowsQEMUMetadata{}
	}
	return metadata
}

func qemuMonitorCommand(sock, command string) error {
	return qemuMonitorCommands(sock, []string{command}, 0)
}

func qemuMonitorCommands(sock string, commands []string, delay time.Duration) error {
	conn, err := dialQEMUMonitor(sock, 5*time.Second)
	if err != nil {
		return fmt.Errorf("dial qemu monitor: %w", err)
	}
	defer conn.Close()
	for _, command := range commands {
		if _, err := fmt.Fprintln(conn, command); err != nil {
			return fmt.Errorf("write qemu monitor: %w", err)
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	return nil
}

func dialQEMUMonitor(sock string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		conn, err := net.DialTimeout("unix", sock, time.Second)
		if err == nil {
			return conn, nil
		}
		last = err
		if time.Now().Add(100 * time.Millisecond).After(deadline) {
			return nil, last
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (q *qemuVZScriptAutomation) monitorCommand(command string) error {
	if q.verbose {
		fmt.Fprintf(os.Stderr, "[qemu] %s\n", command)
	}
	return qemuMonitorCommand(q.monitor, command)
}

func (q *qemuVZScriptAutomation) monitorCommands(commands []string, delay time.Duration) error {
	for _, command := range commands {
		if q.verbose {
			fmt.Fprintf(os.Stderr, "[qemu] %s\n", command)
		}
	}
	return qemuMonitorCommands(q.monitor, commands, delay)
}

func (q *qemuVZScriptAutomation) captureImage() (image.Image, string, error) {
	if err := os.MkdirAll(q.artifactDir, 0755); err != nil {
		return nil, "", fmt.Errorf("create qemu vzscript artifact dir: %w", err)
	}
	path := filepath.Join(q.artifactDir, fmt.Sprintf("screen-%d.ppm", time.Now().UnixNano()))
	if err := q.monitorCommand("screendump " + path); err != nil {
		return nil, "", err
	}
	if err := waitForNonEmptyFile(path, 5*time.Second); err != nil {
		return nil, "", fmt.Errorf("qemu screendump: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("open qemu screendump: %w", err)
	}
	defer f.Close()
	img, err := decodePPM(f)
	if err != nil {
		return nil, "", fmt.Errorf("decode qemu screendump: %w", err)
	}
	return img, path, nil
}

func waitForNonEmptyFile(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err == nil && info.Size() > 0 {
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", path)
}

func (q *qemuVZScriptAutomation) screenshot(path string) (string, error) {
	_, src, err := q.captureImage()
	if err != nil {
		return "", err
	}
	if path == "" {
		return src, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", err
	}
	return path, nil
}

func (q *qemuVZScriptAutomation) allText() (string, error) {
	img, _, err := q.captureImage()
	if err != nil {
		return "", err
	}
	return q.ocr.AllText(img), nil
}

func (q *qemuVZScriptAutomation) waitForText(text string, timeout time.Duration, opts ocrx.SearchOptions) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, _, err := q.captureImage()
		if err == nil {
			_, _, found := q.ocr.FindTextWithOptions(img, text, opts)
			if found {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout: text %q not found", text)
}

func (q *qemuVZScriptAutomation) waitForTextGone(text string, timeout time.Duration, opts ocrx.SearchOptions) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, _, err := q.captureImage()
		if err == nil {
			_, _, found := q.ocr.FindTextWithOptions(img, text, opts)
			if !found {
				return nil
			}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timeout waiting for text %q to disappear", text)
}

func (q *qemuVZScriptAutomation) sendKey(spec string) error {
	key, err := qemuKeySpec(spec)
	if err != nil {
		return err
	}
	return q.monitorCommand("sendkey " + key)
}

func (q *qemuVZScriptAutomation) typeText(text string) error {
	commands := make([]string, 0, len(text))
	for _, r := range text {
		key, err := qemuKeyForRune(r)
		if err != nil {
			return err
		}
		commands = append(commands, "sendkey "+key)
	}
	return q.monitorCommands(commands, 60*time.Millisecond)
}

func (q *qemuVZScriptAutomation) runWindowsInstall(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	state := windowsSetupState{}
	for time.Now().Before(deadline) {
		text, err := q.allText()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		action := windowsSetupActionForTextState(text, state)
		if action == nil {
			time.Sleep(5 * time.Second)
			continue
		}
		if action.err != "" {
			return fmt.Errorf("%s", action.err)
		}
		if action.done {
			return nil
		}
		if q.verbose {
			fmt.Fprintf(os.Stderr, "[windows-install] %s\n", action.name)
		}
		for _, spec := range action.keys {
			if err := q.sendKey(spec); err != nil {
				return err
			}
			time.Sleep(250 * time.Millisecond)
		}
		if action.text != "" {
			if len(action.keys) > 0 {
				time.Sleep(2 * time.Second)
			}
			if err := q.typeText(action.text); err != nil {
				return err
			}
		}
		if len(action.postKeys) > 0 {
			time.Sleep(action.postKeyDelay)
			for _, spec := range action.postKeys {
				if err := q.sendKey(spec); err != nil {
					return err
				}
				time.Sleep(250 * time.Millisecond)
			}
		}
		if action.setLabConfigApplied {
			state.labConfigApplied = true
		}
		if action.setInstallStarted {
			state.installStarted = true
		}
		time.Sleep(action.delay)
	}
	return fmt.Errorf("windows install automation timed out after %s", timeout)
}

type windowsSetupState struct {
	labConfigApplied bool
	installStarted   bool
}

type windowsSetupAction struct {
	name                string
	keys                []string
	postKeys            []string
	text                string
	delay               time.Duration
	postKeyDelay        time.Duration
	done                bool
	err                 string
	setLabConfigApplied bool
	setInstallStarted   bool
}

func windowsSetupActionForText(text string) *windowsSetupAction {
	return windowsSetupActionForTextState(text, windowsSetupState{})
}

func windowsSetupActionForTextState(text string, state windowsSetupState) *windowsSetupAction {
	t := strings.ToLower(text)
	t = strings.Join(strings.Fields(t), " ")
	hasCDPrompt := strings.Contains(t, "press any key to boot from cd") || strings.Contains(t, "press any key to boot from dvd")
	hasShell := strings.Contains(t, "uefi interactive shell") || strings.Contains(t, "shell>")

	switch {
	case strings.Contains(t, "desktop") || strings.Contains(t, "recycle bin") || windowsDesktopText(t):
		return &windowsSetupAction{name: "desktop reached", done: true}
	case hasCDPrompt && !hasShell:
		if state.installStarted {
			return &windowsSetupAction{name: "ignore cd boot prompt after reboot", delay: 8 * time.Second}
		}
		return &windowsSetupAction{name: "boot from windows installer", keys: []string{"return"}, delay: 8 * time.Second}
	case hasShell:
		return &windowsSetupAction{
			name:         "start windows installer from uefi shell",
			text:         "fs0:\\efi\\boot\\bootaa64.efi\n",
			postKeys:     []string{"return"},
			postKeyDelay: time.Second,
			delay:        8 * time.Second,
		}
	case strings.Contains(t, "doesn't currently meet windows 11") || strings.Contains(t, "does not currently meet windows 11"):
		return &windowsSetupAction{name: "windows requirements blocked", err: "windows setup reached requirements page before LabConfig bypass took effect"}
	case strings.Contains(t, "unable to open link") && (strings.Contains(t, "activate windows") || strings.Contains(t, "product key")):
		return &windowsSetupAction{name: "dismiss setup link message", keys: []string{"escape", "shift+tab", "shift+tab", "return"}, delay: 3 * time.Second}
	case strings.Contains(t, "unable to open link"):
		return &windowsSetupAction{name: "dismiss setup link message", keys: []string{"escape"}, delay: time.Second}
	case strings.Contains(t, "select setup option"):
		return &windowsSetupAction{name: "select install setup option", keys: []string{"tab", "space", "tab", "tab", "tab", "return"}, delay: 5 * time.Second}
	case windowsSetupWaitText(t) || windowsInstallProgressText(t):
		return &windowsSetupAction{name: "wait for install progress", delay: 20 * time.Second, setInstallStarted: true}
	case strings.Contains(t, "select language settings"):
		return &windowsSetupAction{name: "accept language settings", keys: []string{"return"}, delay: 2 * time.Second}
	case strings.Contains(t, "enter your language and other preferences"):
		if !state.labConfigApplied {
			return &windowsSetupAction{
				name:                "apply windows 11 setup bypass",
				keys:                []string{"shift+f10"},
				text:                windowsLabConfigText(),
				delay:               6 * time.Second,
				setLabConfigApplied: true,
			}
		}
		return &windowsSetupAction{name: "accept language settings", keys: []string{"alt+n"}, delay: 2 * time.Second}
	case strings.Contains(t, "right country or") || strings.Contains(t, "right country or region"):
		return &windowsSetupAction{name: "accept oobe region", keys: []string{"return"}, delay: 3 * time.Second}
	case strings.Contains(t, "right keyboard layout") || strings.Contains(t, "keyboard layout or input method"):
		return &windowsSetupAction{name: "accept oobe keyboard", keys: []string{"return"}, delay: 3 * time.Second}
	case strings.Contains(t, "add a second keyboard") || strings.Contains(t, "second keyboard layout"):
		return &windowsSetupAction{name: "skip second keyboard", keys: []string{"return"}, delay: 3 * time.Second}
	case strings.Contains(t, "select keyboard settings") || strings.Contains(t, "keyboard layout"):
		return &windowsSetupAction{name: "accept keyboard settings", keys: []string{"return"}, delay: 2 * time.Second}
	case strings.Contains(t, "compatibility report") || strings.Contains(t, "upgrade option isn't available"):
		return &windowsSetupAction{name: "close compatibility report", keys: []string{"return"}, delay: 2 * time.Second}
	case strings.Contains(t, "which type of installation"):
		return &windowsSetupAction{name: "select custom install", keys: []string{"alt+c"}, delay: 3 * time.Second}
	case strings.Contains(t, "where do you want to install windows"):
		return &windowsSetupAction{name: "install to default disk", keys: []string{"return"}, delay: 10 * time.Second, setInstallStarted: true}
	case strings.Contains(t, "administrator:") && strings.Contains(t, "cmd.exe") && strings.Contains(t, "\\system32>"):
		return &windowsSetupAction{name: "run oobe network bypass", text: "OOBE\\BYPASSNRO\n", delay: 20 * time.Second}
	case strings.Contains(t, "install now") || strings.Contains(t, "install windows"):
		return &windowsSetupAction{name: "start installer", keys: []string{"return"}, delay: 5 * time.Second}
	case strings.Contains(t, "activate windows") || strings.Contains(t, "product key"):
		return &windowsSetupAction{name: "skip product key", keys: []string{"tab", "return"}, delay: 3 * time.Second}
	case strings.Contains(t, "applicable notices") || strings.Contains(t, "license terms"):
		return &windowsSetupAction{name: "accept license", keys: []string{"space", "return"}, delay: 3 * time.Second}
	case strings.Contains(t, "select image") || strings.Contains(t, "select the operating system") || strings.Contains(t, "operating system you want to install"):
		if !state.labConfigApplied {
			return &windowsSetupAction{
				name:                "apply windows 11 setup bypass",
				keys:                []string{"shift+f10"},
				text:                windowsLabConfigText(),
				delay:               6 * time.Second,
				setLabConfigApplied: true,
			}
		}
		return &windowsSetupAction{name: "select default edition", keys: []string{"return"}, delay: 3 * time.Second}
	case strings.Contains(t, "let's connect you to a network") || strings.Contains(t, "connect to a network"):
		return &windowsSetupAction{name: "open oobe command prompt", keys: []string{"shift+f10"}, delay: 4 * time.Second}
	case strings.Contains(t, "name your device"):
		return &windowsSetupAction{name: "enter device name", text: "COVE-WIN11\n", delay: 2 * time.Second}
	case strings.Contains(t, "who's going to use this device"):
		return &windowsSetupAction{name: "enter local user", text: "cove\n", delay: 2 * time.Second}
	case strings.Contains(t, "create a super memorable password") || strings.Contains(t, "create a password"):
		return &windowsSetupAction{name: "enter local password", text: "Cove123!\n", delay: 2 * time.Second}
	case strings.Contains(t, "confirm your password"):
		return &windowsSetupAction{name: "confirm local password", text: "Cove123!\n", delay: 2 * time.Second}
	case strings.Contains(t, "security questions"):
		return &windowsSetupAction{name: "enter security answer", text: "cove\n", delay: 2 * time.Second}
	case strings.Contains(t, "choose privacy settings") || strings.Contains(t, "privacy settings for your device"):
		return &windowsSetupAction{name: "accept privacy defaults", keys: []string{"return"}, delay: 5 * time.Second}
	}
	return nil
}

func windowsLabConfigText() string {
	return strings.Join([]string{
		"reg add HKLM\\SYSTEM\\Setup\\LabConfig /v BypassTPMCheck /t REG_DWORD /d 1 /f",
		"reg add HKLM\\SYSTEM\\Setup\\LabConfig /v BypassSecureBootCheck /t REG_DWORD /d 1 /f",
		"reg add HKLM\\SYSTEM\\Setup\\LabConfig /v BypassRAMCheck /t REG_DWORD /d 1 /f",
		"reg add HKLM\\SYSTEM\\Setup\\LabConfig /v BypassCPUCheck /t REG_DWORD /d 1 /f",
		"exit",
	}, "\n") + "\n"
}

func windowsInstallProgressText(text string) bool {
	if !strings.Contains(text, "installing windows") {
		return false
	}
	for _, marker := range []string{
		"copying windows files",
		"complete",
		"getting files ready",
		"installing features",
		"installing updates",
		"finishing up",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func windowsSetupWaitText(text string) bool {
	for _, marker := range []string{
		"getting ready",
		"working on updates",
		"just a moment",
		"please keep your computer on",
		"please keep your pc on",
		"your computer may restart",
		"getting things ready",
		"this might take a few minutes",
		"don't turn off your pc",
		"good things coming your way",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func windowsDesktopText(text string) bool {
	return strings.Contains(text, "search for apps") &&
		(strings.Contains(text, "pinned") || strings.Contains(text, "top apps"))
}

func qemuKeySpec(spec string) (string, error) {
	spec = strings.TrimSpace(strings.ToLower(spec))
	if spec == "" {
		return "", fmt.Errorf("empty key spec")
	}
	parts := strings.Split(spec, "+")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		key, err := qemuKeyName(part)
		if err != nil {
			return "", err
		}
		out = append(out, key)
	}
	return strings.Join(out, "-"), nil
}

func qemuKeyName(name string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "return", "enter":
		return "ret", nil
	case "space":
		return "spc", nil
	case "escape", "esc":
		return "esc", nil
	case "delete", "backspace":
		return "backspace", nil
	case "alt", "option":
		return "alt", nil
	case "ctrl", "control":
		return "ctrl", nil
	case "cmd", "command":
		return "meta_l", nil
	case "shift":
		return "shift", nil
	case "up", "down", "left", "right", "tab":
		return name, nil
	case "slash":
		return "slash", nil
	case "backslash":
		return "backslash", nil
	case "period", "dot":
		return "dot", nil
	case "comma":
		return "comma", nil
	case "semicolon":
		return "semicolon", nil
	case "quote", "apostrophe":
		return "apostrophe", nil
	case "minus", "dash", "hyphen":
		return "minus", nil
	case "equals", "equal":
		return "equal", nil
	case "grave", "backtick":
		return "grave_accent", nil
	}
	if len(name) == 1 && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= '0' && name[0] <= '9')) {
		return name, nil
	}
	if strings.HasPrefix(name, "f") {
		if n, err := strconv.Atoi(strings.TrimPrefix(name, "f")); err == nil && n >= 1 && n <= 12 {
			return name, nil
		}
	}
	return "", fmt.Errorf("unsupported qemu key %q", name)
}

func qemuKeyForRune(r rune) (string, error) {
	if r >= 'a' && r <= 'z' {
		return string(r), nil
	}
	if r >= 'A' && r <= 'Z' {
		return "shift-" + strings.ToLower(string(r)), nil
	}
	if r >= '0' && r <= '9' {
		return string(r), nil
	}
	switch r {
	case ' ':
		return "spc", nil
	case '\n', '\r':
		return "ret", nil
	case '\t':
		return "tab", nil
	case '\\':
		return "backslash", nil
	case '/':
		return "slash", nil
	case '.':
		return "dot", nil
	case ',':
		return "comma", nil
	case ';':
		return "semicolon", nil
	case '\'':
		return "apostrophe", nil
	case '-':
		return "minus", nil
	case '=':
		return "equal", nil
	case '`':
		return "grave_accent", nil
	case '!':
		return "shift-1", nil
	case '@':
		return "shift-2", nil
	case '#':
		return "shift-3", nil
	case '$':
		return "shift-4", nil
	case '%':
		return "shift-5", nil
	case '^':
		return "shift-6", nil
	case '&':
		return "shift-7", nil
	case '*':
		return "shift-8", nil
	case '(':
		return "shift-9", nil
	case ')':
		return "shift-0", nil
	case '_':
		return "shift-minus", nil
	case '+':
		return "shift-equal", nil
	case ':':
		return "shift-semicolon", nil
	case '"':
		return "shift-apostrophe", nil
	case '?':
		return "shift-slash", nil
	}
	return "", fmt.Errorf("unsupported qemu text character %q", r)
}

func qemuDefaultActionForText(text string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "next", "continue", "install", "install now", "agree", "accept", "ok", "yes":
		return "return", true
	case "cancel", "close":
		return "escape", true
	default:
		return "", false
	}
}

func vzQEMUMonitorCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "send a raw command to the QEMU HMP monitor",
			Args:    "command...",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			if len(args) == 0 {
				return nil, script.ErrUsage
			}
			q := newQEMUVZScriptAutomation(cfg)
			if q == nil {
				return nil, fmt.Errorf("qemu-monitor requires -qemu-monitor or a QEMU VM")
			}
			return nil, q.monitorCommand(strings.Join(args, " "))
		},
	)
}

func decodePPM(r io.Reader) (image.Image, error) {
	br := bufio.NewReader(r)
	magic, err := ppmToken(br)
	if err != nil {
		return nil, err
	}
	if magic != "P6" {
		return nil, fmt.Errorf("unsupported ppm magic %q", magic)
	}
	width, err := ppmInt(br, "width")
	if err != nil {
		return nil, err
	}
	height, err := ppmInt(br, "height")
	if err != nil {
		return nil, err
	}
	max, err := ppmInt(br, "max value")
	if err != nil {
		return nil, err
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid ppm size %dx%d", width, height)
	}
	if max <= 0 || max > 255 {
		return nil, fmt.Errorf("unsupported ppm max value %d", max)
	}
	pixels := make([]byte, width*height*3)
	if _, err := io.ReadFull(br, pixels); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 3
			r8, g8, b8 := pixels[i], pixels[i+1], pixels[i+2]
			if max != 255 {
				r8 = byte(int(r8) * 255 / max)
				g8 = byte(int(g8) * 255 / max)
				b8 = byte(int(b8) * 255 / max)
			}
			img.SetRGBA(x, y, color.RGBA{R: r8, G: g8, B: b8, A: 255})
		}
	}
	return img, nil
}

func ppmInt(br *bufio.Reader, name string) (int, error) {
	tok, err := ppmToken(br)
	if err != nil {
		return 0, fmt.Errorf("read ppm %s: %w", name, err)
	}
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, fmt.Errorf("parse ppm %s %q: %w", name, tok, err)
	}
	return n, nil
}

func ppmToken(br *bufio.Reader) (string, error) {
	for {
		b, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		if isPPMSpace(b) {
			continue
		}
		if b == '#' {
			if _, err := br.ReadString('\n'); err != nil && err != io.EOF {
				return "", err
			}
			continue
		}
		if err := br.UnreadByte(); err != nil {
			return "", err
		}
		break
	}
	var b bytes.Buffer
	for {
		c, err := br.ReadByte()
		if err != nil {
			if err == io.EOF && b.Len() > 0 {
				return b.String(), nil
			}
			return "", err
		}
		if isPPMSpace(c) {
			break
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

func isPPMSpace(b byte) bool {
	switch b {
	case ' ', '\n', '\r', '\t', '\v', '\f':
		return true
	default:
		return false
	}
}

func vzWindowsInstallCmd(cfg vzscriptConfig) script.Cmd {
	return script.Command(
		script.CmdUsage{
			Summary: "drive Windows setup with QEMU monitor screenshots and OCR",
			Args:    "[timeout]",
		},
		func(s *script.State, args ...string) (script.WaitFunc, error) {
			timeout := 90 * time.Minute
			if len(args) > 1 {
				return nil, script.ErrUsage
			}
			if len(args) == 1 {
				d, err := time.ParseDuration(args[0])
				if err != nil {
					return nil, fmt.Errorf("invalid timeout %q: %w", args[0], err)
				}
				timeout = d
			}
			q := newQEMUVZScriptAutomation(cfg)
			if q == nil {
				return nil, fmt.Errorf("windows-install requires a QEMU monitor; pass -qemu-monitor or use a QEMU Windows VM")
			}
			return nil, q.runWindowsInstall(timeout)
		},
	)
}
