package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTCCFDAArgs(t *testing.T) {
	opts, err := parseTCCFDAArgs([]string{
		"-vm", "ftue",
		"-tcc-path", "/Volumes/ml-explore",
		"-timeout", "12s",
		"-upgrade-agent",
		"-check",
		"-export-profile", "out.mobileconfig",
	})
	if err != nil {
		t.Fatalf("parseTCCFDAArgs: %v", err)
	}
	if opts.vmName != "ftue" || opts.path != "/Volumes/ml-explore" || opts.exportProfile != "out.mobileconfig" {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.timeout != 12*time.Second || !opts.upgradeAgent || !opts.check {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestParseTCCFDAArgsRejectsPassword(t *testing.T) {
	if _, err := parseTCCFDAArgs([]string{"-password", "secret"}); err == nil {
		t.Fatal("parseTCCFDAArgs accepted -password; cove must not collect guest passwords")
	}
}

func TestParseTCCFDAArgsRejectsPositional(t *testing.T) {
	_, err := parseTCCFDAArgs([]string{"-tcc-path", "/Volumes/ml-explore", "extra"})
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("err = %v, want unexpected arguments", err)
	}
}

func TestClassifyTCCFDAOpen(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want tccFDAStatus
	}{
		{"opened", nil, tccFDAAuthorized},
		{"stat eperm", errors.New("not_found: stat: stat /Library/Application Support/com.apple.TCC/TCC.db: operation not permitted"), tccFDADenied},
		{"open eperm", errors.New("internal: open: open /x: Operation not permitted"), tccFDADenied},
		{"missing", errors.New("not_found: stat: no such file or directory"), tccFDAUnknown},
		{"transport", errors.New("unavailable: connection refused"), tccFDAUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := classifyTCCFDAOpen(tt.err)
			if got != tt.want {
				t.Fatalf("classifyTCCFDAOpen(%v) = %s, want %s", tt.err, got, tt.want)
			}
		})
	}
}

func TestParseTCCFDAProbe(t *testing.T) {
	tests := []struct {
		data       string
		want       tccFDAStatus
		wantDetail string
	}{
		{"authorized", tccFDAAuthorized, ""},
		{"denied stat: operation not permitted", tccFDADenied, "stat: operation not permitted"},
		{"unknown agent unavailable", tccFDAUnknown, "unknown agent unavailable"},
		{"", tccFDAUnknown, ""},
		{"authorizedx", tccFDAUnknown, "authorizedx"},
	}
	for _, tt := range tests {
		got, detail := parseTCCFDAProbe(tt.data)
		if got != tt.want || detail != tt.wantDetail {
			t.Errorf("parseTCCFDAProbe(%q) = %s, %q; want %s, %q", tt.data, got, detail, tt.want, tt.wantDetail)
		}
	}
	for _, s := range []tccFDAStatus{tccFDAAuthorized, tccFDADenied} {
		if got, _ := parseTCCFDAProbe(formatTCCFDAProbe(s, "x y")); got != s {
			t.Errorf("round trip %s = %s", s, got)
		}
	}
}

func TestPrintTCCFDAStatusTokens(t *testing.T) {
	tests := []struct {
		status tccFDAStatus
		want   string
	}{
		{tccFDAAuthorized, "COVE_TCC_FDA_AUTHORIZED agent=/usr/local/bin/vz-agent evidence=in-process-open"},
		{tccFDADenied, "COVE_TCC_FDA_DENIED"},
		{tccFDAUnknown, "COVE_TCC_FDA_UNVERIFIED"},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		printTCCFDAStatus(&buf, tt.status, "detail")
		if !strings.HasPrefix(buf.String(), tt.want) {
			t.Errorf("status %s printed %q, want prefix %q", tt.status, buf.String(), tt.want)
		}
	}
}

func TestParseDesignatedRequirement(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want string
		ok   bool
	}{
		{"adhoc", "Executable=/usr/local/bin/vz-agent\n# designated => cdhash H\"21e4c813bf5893db14f61f624442acb1a5db9c8d\"\n", `cdhash H"21e4c813bf5893db14f61f624442acb1a5db9c8d"`, true},
		{"cert", "designated => identifier \"com.tmc.cove.vz-agent\" and certificate leaf = H\"ab\"\n", `identifier "com.tmc.cove.vz-agent" and certificate leaf = H"ab"`, true},
		{"none", "Executable=/x\n", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseDesignatedRequirement(tt.out)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("parseDesignatedRequirement = %q, %t; want %q, %t", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRequirementBinding(t *testing.T) {
	tests := []struct {
		req  string
		want string
	}{
		{`cdhash H"21e4"`, "hash-bound"},
		{`identifier "x" or cdhash H"21e4"`, "hash-bound"},
		{`identifier "com.tmc.cove.vz-agent" and certificate leaf = H"ab"`, "unqualified"},
		{`identifier "com.tmc.cove.vz-agent"`, "unqualified"},
	}
	for _, tt := range tests {
		if got := requirementBinding(tt.req); got != tt.want {
			t.Errorf("requirementBinding(%q) = %s, want %s", tt.req, got, tt.want)
		}
	}
	var buf bytes.Buffer
	printTCCFDAIdentity(&buf, `identifier "x"`)
	if strings.Contains(buf.String(), "stable") {
		t.Errorf("identity output claims stability: %s", buf.String())
	}
}

func TestExportTCCFDAProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vz-agent.mobileconfig")
	req := `cdhash H"21e4c813bf5893db14f61f624442acb1a5db9c8d"`
	var out bytes.Buffer
	if err := exportTCCFDAProfile(&out, path, req); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"COVE_TCC_FDA_PROFILE_EXPORTED", "binding=hash-bound", "grants nothing by itself", "re-export after every agent upgrade"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<string>com.apple.TCC.configuration-profile-policy</string>",
		"<key>SystemPolicyAllFiles</key>",
		"<string>/usr/local/bin/vz-agent</string>",
		"<string>path</string>",
		"<string>cdhash H&#34;21e4c813bf5893db14f61f624442acb1a5db9c8d&#34;</string>",
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("profile missing %q", want)
		}
	}
	if strings.Count(string(data), "<key>Authorization</key>") != 1 {
		t.Error("profile must grant exactly one service entry")
	}
	if plutil, err := exec.LookPath("plutil"); err == nil {
		if out, err := exec.Command(plutil, "-lint", path).CombinedOutput(); err != nil {
			t.Fatalf("plutil -lint: %v\n%s", err, out)
		}
	}
}

func TestClassifyFDAScreen(t *testing.T) {
	tests := []struct {
		name string
		text string
		want fdaUIState
	}{
		{"empty", "  ", fdaUIUnobservable},
		{"pane", "Privacy & Security\nFull Disk Access\nAllow the applications below to access data", fdaUIPane},
		{"pane with agent", "Full Disk Access\nTerminal\nvz-agent", fdaUIPaneAgent},
		{"open panel", "Full Disk Access\nApplications\nCancel\nOpen", fdaUIOpenPanel},
		{"go-to sheet", "Go to the folder:\n/usr/local/bin/vz-agent\nCancel\nOpen", fdaUIGoTo},
		{"auth", "Privacy & Security\nSystem Settings is trying to modify your system settings.\nTouch ID or enter your password to allow this.\nUse Password...", fdaUIAuthPrompt},
		{"desktop", "Finder File Edit View Go Window Help", fdaUIOther},
		{"opened word only", "Full Disk Access\nopened recently", fdaUIPane},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyFDAScreen(tt.text); got != tt.want {
				t.Fatalf("classifyFDAScreen = %s, want %s", got, tt.want)
			}
		})
	}
}

var (
	testBG      = color.RGBA{255, 255, 255, 255}
	testOffFill = color.RGBA{228, 228, 232, 255}
	testOnFill  = color.RGBA{10, 132, 255, 255}
	testText    = color.RGBA{30, 30, 30, 255}
)

// paneImage draws a Full Disk Access row: a dark label at (40,40)-(100,56)
// and, when fill is non-nil, a 40x20 switch at x=500 with a white knob.
func paneImage(fill *color.RGBA, knobRight bool) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 800, 200))
	draw.Draw(img, img.Bounds(), &image.Uniform{testBG}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(40, 44, 100, 52), &image.Uniform{testText}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(580, 0, 581, 200), &image.Uniform{color.RGBA{200, 200, 200, 255}}, image.Point{}, draw.Src)
	if fill != nil {
		drawSwitch(img, 500, *fill, knobRight)
	}
	return img
}

func drawSwitch(img *image.RGBA, x int, fill color.RGBA, knobRight bool) {
	draw.Draw(img, image.Rect(x, 38, x+40, 58), &image.Uniform{fill}, image.Point{}, draw.Src)
	knob := image.Rect(x+2, 40, x+18, 56)
	if knobRight {
		knob = image.Rect(x+22, 40, x+38, 56)
	}
	draw.Draw(img, knob, &image.Uniform{testBG}, image.Point{}, draw.Src)
}

var (
	testAgentRow = fdaText{text: "vz-agent", box: image.Rect(40, 40, 100, 56)}
	heading      = fdaText{text: "Full Disk Access", box: image.Rect(40, 5, 200, 20)}
	description  = fdaText{text: "Allow the applications below to access data", box: image.Rect(40, 22, 460, 34)}
)

func TestFindRowSwitch(t *testing.T) {
	outside := paneImage(&testOffFill, false)
	drawSwitch(outside, 700, testOnFill, true)
	two := paneImage(&testOffFill, false)
	drawSwitch(two, 420, testOffFill, false)
	texts := []fdaText{heading, description, testAgentRow}
	tests := []struct {
		name   string
		img    image.Image
		texts  []fdaText
		want   fdaSwitchState
		wantOK bool
	}{
		{"off", paneImage(&testOffFill, false), texts, fdaSwitchOff, true},
		{"on", paneImage(&testOnFill, true), texts, fdaSwitchOn, true},
		{"absent", paneImage(nil, false), texts, "", false},
		{"control outside list", outside, texts, fdaSwitchOff, true},
		{"two controls", two, texts, "", false},
		{"no heading", paneImage(&testOffFill, false), []fdaText{description, testAgentRow}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sw, ok := findRowSwitch(tt.img, tt.texts, testAgentRow)
			if ok != tt.wantOK || sw.state != tt.want {
				t.Fatalf("findRowSwitch = %+v, %t; want %s, %t", sw, ok, tt.want, tt.wantOK)
			}
			if ok && (sw.center.X < 500 || sw.center.X >= 540 || sw.center.Y != 48) {
				t.Fatalf("switch center %v outside drawn switch", sw.center)
			}
		})
	}
}

// selectedImage draws the vz-agent row with an accent selection highlight.
func selectedImage() *image.RGBA {
	img := paneImage(nil, false)
	draw.Draw(img, image.Rect(30, 38, 400, 58), &image.Uniform{testOnFill}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(40, 44, 100, 52), &image.Uniform{testBG}, image.Point{}, draw.Src)
	return img
}

func TestRowSelected(t *testing.T) {
	gray := paneImage(nil, false)
	draw.Draw(gray, image.Rect(30, 38, 400, 58), &image.Uniform{testOffFill}, image.Point{}, draw.Src)
	tests := []struct {
		name string
		img  image.Image
		want bool
	}{
		{"accent highlight", selectedImage(), true},
		{"unselected", paneImage(nil, false), false},
		{"gray highlight", gray, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rowSelected(tt.img, testAgentRow); got != tt.want {
				t.Fatalf("rowSelected = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestFindAgentRow(t *testing.T) {
	one := []fdaText{{text: "Full Disk Access"}, testAgentRow, {text: "vz-agent-user"}}
	if row, ok := findAgentRow(one); !ok || row.box != testAgentRow.box {
		t.Fatalf("findAgentRow = %+v, %t", row, ok)
	}
	if _, ok := findAgentRow(append(one, testAgentRow)); ok {
		t.Fatal("findAgentRow accepted two vz-agent rows")
	}
}

func TestFindAddControl(t *testing.T) {
	heading := fdaText{text: "Full Disk Access", box: image.Rect(40, 10, 200, 30)}
	tests := []struct {
		name   string
		texts  []fdaText
		want   image.Point
		wantOK bool
	}{
		{"plus", []fdaText{heading, {text: "+", box: image.Rect(40, 150, 50, 160)}}, image.Pt(45, 155), true},
		{"plus minus", []fdaText{heading, {text: "+ -", box: image.Rect(40, 150, 70, 160)}}, image.Pt(45, 155), true},
		{"above heading", []fdaText{heading, {text: "+", box: image.Rect(40, 0, 50, 8)}}, image.Point{}, false},
		{"no heading", []fdaText{{text: "+", box: image.Rect(40, 150, 50, 160)}}, image.Point{}, false},
		{"two", []fdaText{heading, {text: "+", box: image.Rect(40, 150, 50, 160)}, {text: "+", box: image.Rect(90, 150, 100, 160)}}, image.Point{}, false},
		{"plus text", []fdaText{heading, {text: "+ Add App", box: image.Rect(40, 150, 90, 160)}}, image.Point{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := findAddControl(tt.texts)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("findAddControl = %v, %t; want %v, %t", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// fakeFDAUI replays screens. The first input after an observation moves
// to the observed screen's next index, modeling the guest's response.
type fakeFDAUI struct {
	screens []fakeScreen
	cur     int
	armed   bool
	inputs  []string
}

type fakeScreen struct {
	img   image.Image
	texts []fdaText
	next  int // screen shown after any input; 0 keeps the current screen
	after int // screen shown after this one is observed once; 0 stays
}

func (f *fakeFDAUI) Screenshot() (image.Image, error) {
	return f.screens[f.cur].img, nil
}

func (f *fakeFDAUI) recognize(image.Image) ([]fdaText, error) {
	s := f.screens[f.cur]
	f.armed = true
	if s.after != 0 {
		defer func() { f.cur = s.after }()
	}
	return s.texts, nil
}

func (f *fakeFDAUI) input(name string) error {
	f.inputs = append(f.inputs, name)
	if n := f.screens[f.cur].next; f.armed && n != 0 {
		f.cur = n
	}
	f.armed = false
	return nil
}

func (f *fakeFDAUI) MouseClick(x, y float64) error {
	return f.input(fmt.Sprintf("click %.2f,%.2f", x, y))
}
func (f *fakeFDAUI) KeyPress(keyCode uint16) error { return f.input("key") }
func (f *fakeFDAUI) KeyPressWithModifiers(keyCode uint16, modifiers uint) error {
	return f.input("go-to")
}
func (f *fakeFDAUI) TypeText(text string) error { return f.input("type " + text) }

func newTestFDASetup(ui *fakeFDAUI, authorized func() bool) (*fdaSetup, *bytes.Buffer) {
	var out bytes.Buffer
	clock := time.Unix(0, 0)
	s := &fdaSetup{
		ui:        ui,
		recognize: ui.recognize,
		out:       &out,
		timeout:   20 * time.Second,
		probe: func() (tccFDAStatus, string) {
			if authorized != nil && authorized() {
				return tccFDAAuthorized, ""
			}
			return tccFDADenied, "operation not permitted"
		},
		reopen: func() error { return ui.input("reopen") },
		sleep:  func(d time.Duration) { clock = clock.Add(d) },
		now:    func() time.Time { return clock },
	}
	return s, &out
}

var (
	plus      = fdaText{text: "+", box: image.Rect(40, 150, 50, 160)}
	cancel    = fdaText{text: "Cancel", box: image.Rect(300, 150, 340, 160)}
	openBtn   = fdaText{text: "Open", box: image.Rect(350, 150, 390, 160)}
	goToTitle = fdaText{text: "Go to the folder:", box: image.Rect(300, 60, 400, 70)}
	typedPath = fdaText{text: "/usr/local/bin/vz-agent", box: image.Rect(300, 80, 450, 90)}
	authText  = fdaText{text: "System Settings is trying to modify your system settings. Use Password"}
	finderBar = fdaText{text: "Finder File Edit View"}
)

func TestFDASetupAddsAndEnablesAgent(t *testing.T) {
	ui := &fakeFDAUI{}
	ui.screens = []fakeScreen{
		0: {img: paneImage(nil, false), texts: []fdaText{heading, description, plus}, next: 1},
		1: {img: paneImage(nil, false), texts: []fdaText{heading, cancel, openBtn}, next: 2},
		2: {img: paneImage(nil, false), texts: []fdaText{heading, goToTitle, cancel, openBtn}, next: 3},
		3: {img: paneImage(nil, false), texts: []fdaText{heading, goToTitle, typedPath, cancel, openBtn}, next: 4},
		4: {img: selectedImage(), texts: []fdaText{heading, testAgentRow, cancel, openBtn}, next: 5},
		5: {img: paneImage(&testOffFill, false), texts: []fdaText{heading, description, testAgentRow, plus}, next: 6},
		6: {img: paneImage(&testOnFill, true), texts: []fdaText{heading, description, testAgentRow, plus}},
	}
	s, out := newTestFDASetup(ui, func() bool { return ui.cur == 6 })
	if err := s.run(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	want := []string{"click 0.06,0.78", "go-to", "type /usr/local/bin/vz-agent", "key", "key", "click 0.65,0.24"}
	if strings.Join(ui.inputs, "|") != strings.Join(want, "|") {
		t.Fatalf("inputs = %q, want %q", ui.inputs, want)
	}
	for _, w := range []string{"action=add", "action=enable", "COVE_TCC_FDA_UI_OBSERVED action=go-to ui=go-to-sheet", "COVE_TCC_FDA_AUTHORIZED"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output missing %q:\n%s", w, out)
		}
	}
	if strings.Contains(out.String(), "HANDOFF") {
		t.Errorf("fully identified flow handed off:\n%s", out)
	}
}

func TestFDASetupWaitsForGoToSheet(t *testing.T) {
	// A large redraw (another window, a notification) is not a go-to sheet.
	redraw := paneImage(nil, false)
	draw.Draw(redraw, image.Rect(200, 60, 600, 180), &image.Uniform{testOffFill}, image.Point{}, draw.Src)
	tests := []struct {
		name  string
		sheet image.Image
	}{
		{"unchanged frame", paneImage(nil, false)},
		{"unrelated redraw", redraw},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := &fakeFDAUI{screens: []fakeScreen{
				0: {img: paneImage(nil, false), texts: []fdaText{heading, plus}, next: 1},
				1: {img: paneImage(nil, false), texts: []fdaText{cancel, openBtn}, next: 2},
				2: {img: tt.sheet, texts: []fdaText{cancel, openBtn}},
			}}
			s, out := newTestFDASetup(ui, nil)
			s.timeout = 3 * time.Minute
			if err := s.run(); err == nil {
				t.Fatal("run succeeded")
			}
			if got, want := strings.Join(ui.inputs, "|"), "click 0.06,0.78|go-to"; got != want {
				t.Fatalf("inputs = %q, want %q (no text without an identified sheet)", got, want)
			}
			if !strings.Contains(out.String(), "state=go-to-unobserved") {
				t.Errorf("missing go-to handoff:\n%s", out)
			}
		})
	}
}

func TestFDASetupRequiresSelectedAgentBeforeOpen(t *testing.T) {
	ui := &fakeFDAUI{screens: []fakeScreen{
		0: {img: paneImage(nil, false), texts: []fdaText{heading, plus}, next: 1},
		1: {img: paneImage(nil, false), texts: []fdaText{cancel, openBtn}, next: 2},
		2: {img: paneImage(nil, false), texts: []fdaText{goToTitle, cancel, openBtn}, next: 3},
		3: {img: paneImage(nil, false), texts: []fdaText{goToTitle, typedPath, cancel, openBtn}, next: 4},
		4: {img: paneImage(nil, false), texts: []fdaText{testAgentRow, cancel, openBtn}},
	}}
	s, out := newTestFDASetup(ui, nil)
	s.timeout = 3 * time.Minute
	if err := s.run(); err == nil {
		t.Fatal("run succeeded")
	}
	want := "click 0.06,0.78|go-to|type /usr/local/bin/vz-agent|key"
	if got := strings.Join(ui.inputs, "|"); got != want {
		t.Fatalf("inputs = %q, want %q (no Open for an unselected row)", got, want)
	}
	if !strings.Contains(out.String(), "state=selection-unobserved") {
		t.Errorf("missing selection handoff:\n%s", out)
	}
}

func TestFDASetupOpenPanelNeedsFDAContext(t *testing.T) {
	ui := &fakeFDAUI{screens: []fakeScreen{{img: paneImage(nil, false), texts: []fdaText{cancel, openBtn}}}}
	s, out := newTestFDASetup(ui, nil)
	if err := s.run(); err == nil {
		t.Fatal("run succeeded")
	}
	if len(ui.inputs) != 0 {
		t.Fatalf("sent input to unrelated file dialog: %q", ui.inputs)
	}
	if !strings.Contains(out.String(), "state=open-panel-context") {
		t.Errorf("missing context handoff:\n%s", out)
	}
}

func TestFDASetupSwitchOnButDeniedIsAmbiguous(t *testing.T) {
	ui := &fakeFDAUI{screens: []fakeScreen{{img: paneImage(&testOnFill, true), texts: []fdaText{heading, description, testAgentRow}}}}
	s, out := newTestFDASetup(ui, nil)
	err := s.run()
	if err == nil || !strings.Contains(err.Error(), "COVE_TCC_FDA_SETUP_TIMEOUT") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if len(ui.inputs) != 0 {
		t.Fatalf("toggled an already-on switch: %q", ui.inputs)
	}
	if !strings.Contains(out.String(), "state=switch-on") || !strings.Contains(out.String(), "cause is not determined") {
		t.Errorf("missing ambiguous handoff:\n%s", out)
	}
	if strings.Contains(out.String(), "older") {
		t.Errorf("handoff asserts a cause:\n%s", out)
	}
}

func TestFDASetupWaitsForSwitchTransition(t *testing.T) {
	ui := &fakeFDAUI{screens: []fakeScreen{{img: paneImage(&testOffFill, false), texts: []fdaText{heading, description, testAgentRow}}}}
	s, out := newTestFDASetup(ui, nil)
	s.timeout = 5 * time.Minute
	if err := s.run(); err == nil {
		t.Fatal("run succeeded")
	}
	if len(ui.inputs) != 1 {
		t.Fatalf("inputs = %q, want one click against an unchanged switch", ui.inputs)
	}
	for _, w := range []string{"COVE_TCC_FDA_UI_NO_TRANSITION action=enable", "state=switch-no-effect"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output missing %q:\n%s", w, out)
		}
	}
}

func TestFDASetupUnidentifiedControlsHandOff(t *testing.T) {
	ui := &fakeFDAUI{screens: []fakeScreen{
		{img: paneImage(nil, false), texts: []fdaText{heading}, after: 1},
		{img: paneImage(nil, false), texts: []fdaText{heading, description, testAgentRow}},
	}}
	s, out := newTestFDASetup(ui, nil)
	if err := s.run(); err == nil {
		t.Fatal("run succeeded")
	}
	if len(ui.inputs) != 0 {
		t.Fatalf("clicked unidentified controls: %q", ui.inputs)
	}
	for _, w := range []string{"state=add-unidentified", "state=switch-unidentified"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output missing %q:\n%s", w, out)
		}
	}
}

func TestFDASetupHandsOffAuthentication(t *testing.T) {
	ui := &fakeFDAUI{screens: []fakeScreen{{img: paneImage(nil, false), texts: []fdaText{heading, authText}}}}
	s, out := newTestFDASetup(ui, nil)
	if err := s.run(); err == nil {
		t.Fatal("run succeeded")
	}
	if len(ui.inputs) != 0 {
		t.Fatalf("sent input to authentication prompt: %q", ui.inputs)
	}
	if !strings.Contains(out.String(), "does not type passwords") {
		t.Errorf("missing auth handoff:\n%s", out)
	}
}

func TestFDASetupStopsWhenUnobservable(t *testing.T) {
	black := image.NewRGBA(image.Rect(0, 0, 600, 200))
	ui := &fakeFDAUI{screens: []fakeScreen{{img: black, texts: []fdaText{heading, plus}}}}
	s, _ := newTestFDASetup(ui, nil)
	err := s.run()
	if !errors.Is(err, errFDAUnobservable) {
		t.Fatalf("err = %v, want unobservable", err)
	}
	if len(ui.inputs) != 0 {
		t.Fatalf("sent input to unobservable screen: %q", ui.inputs)
	}
}

func TestFDASetupBoundsPaneOpens(t *testing.T) {
	ui := &fakeFDAUI{screens: []fakeScreen{{img: paneImage(nil, false), texts: []fdaText{finderBar}}}}
	s, out := newTestFDASetup(ui, nil)
	s.timeout = 5 * time.Minute
	if err := s.run(); err == nil {
		t.Fatal("run succeeded without authorization")
	}
	if strings.Join(ui.inputs, "|") != "reopen|reopen" {
		t.Fatalf("inputs = %q, want two pane opens a transition wait apart", ui.inputs)
	}
	for _, w := range []string{"state=other", "COVE_TCC_FDA_DENIED"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output missing %q:\n%s", w, out)
		}
	}
}

func TestFDASetupAuthorizedSendsNoInput(t *testing.T) {
	ui := &fakeFDAUI{screens: []fakeScreen{{img: paneImage(nil, false), texts: []fdaText{heading, plus}}}}
	s, _ := newTestFDASetup(ui, func() bool { return true })
	if err := s.run(); err != nil {
		t.Fatal(err)
	}
	if len(ui.inputs) != 0 {
		t.Fatalf("inputs = %q, want none", ui.inputs)
	}
}
