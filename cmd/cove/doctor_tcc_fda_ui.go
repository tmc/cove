package main

import (
	"errors"
	"fmt"
	"image"
	"io"
	"strings"
	"time"

	ocrx "github.com/tmc/apple/x/vzkit/ocr"
)

// fdaUIState is the observed state of the guest display during setup.
type fdaUIState string

const (
	fdaUIUnobservable fdaUIState = "unobservable"
	fdaUIOther        fdaUIState = "other"
	fdaUIPane         fdaUIState = "pane"
	fdaUIPaneAgent    fdaUIState = "pane-agent-listed"
	fdaUIOpenPanel    fdaUIState = "open-panel"
	fdaUIGoTo         fdaUIState = "go-to-sheet"
	fdaUIAuthPrompt   fdaUIState = "auth-prompt"
)

// fdaUI is the subset of the control client used to drive setup.
type fdaUI interface {
	Screenshot() (image.Image, error)
	MouseClick(x, y float64) error
	KeyPress(keyCode uint16) error
	KeyPressWithModifiers(keyCode uint16, modifiers uint) error
	TypeText(text string) error
}

// fdaText is recognized text with its bounds in screenshot pixels,
// origin at the top left.
type fdaText struct {
	text string
	box  image.Rectangle
}

type fdaFrame struct {
	state fdaUIState
	img   image.Image
	texts []fdaText
}

func recognizeFDAText(img image.Image) ([]fdaText, error) {
	obs, err := ocrx.NewService(false).RecognizeText(img)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	texts := make([]fdaText, 0, len(obs))
	for _, o := range obs {
		r := o.BoundingBox
		x0 := int(r.Origin.X * w)
		y0 := int((1 - r.Origin.Y - r.Size.Height) * h)
		texts = append(texts, fdaText{
			text: o.Text,
			box:  image.Rect(x0, y0, x0+int(r.Size.Width*w), y0+int(r.Size.Height*h)).Add(b.Min),
		})
	}
	return texts, nil
}

func classifyFDAScreen(text string) fdaUIState {
	lower := strings.ToLower(text)
	words := make(map[string]bool)
	for _, w := range strings.FieldsFunc(lower, func(r rune) bool { return (r < 'a' || r > 'z') && r != '-' }) {
		words[w] = true
	}
	switch {
	case strings.TrimSpace(text) == "":
		return fdaUIUnobservable
	case strings.Contains(lower, "is trying to modify") ||
		strings.Contains(lower, "enter your password to allow") ||
		strings.Contains(lower, "touch id or enter your password") ||
		strings.Contains(lower, "use password"):
		return fdaUIAuthPrompt
	case strings.Contains(lower, "go to the folder") || strings.Contains(lower, "go to folder"):
		return fdaUIGoTo
	case words["cancel"] && words["open"]:
		return fdaUIOpenPanel
	case strings.Contains(lower, "full disk access") && words["vz-agent"]:
		return fdaUIPaneAgent
	case strings.Contains(lower, "full disk access"):
		return fdaUIPane
	}
	return fdaUIOther
}

// findAgentRow returns the single recognized "vz-agent" row label.
func findAgentRow(texts []fdaText) (fdaText, bool) {
	var row fdaText
	n := 0
	for _, t := range texts {
		if strings.EqualFold(strings.TrimSpace(t.text), "vz-agent") {
			row = t
			n++
		}
	}
	return row, n == 1
}

// findAddControl returns the click point of the single "+" control that
// sits below the Full Disk Access heading.
func findAddControl(texts []fdaText) (image.Point, bool) {
	top := -1
	for _, t := range texts {
		if strings.Contains(strings.ToLower(t.text), "full disk access") {
			top = max(top, t.box.Max.Y)
		}
	}
	if top < 0 {
		return image.Point{}, false
	}
	var pt image.Point
	n := 0
	for _, t := range texts {
		s := strings.TrimSpace(t.text)
		rest, ok := strings.CutPrefix(s, "+")
		if !ok || strings.Trim(rest, " -–—−") != "" || t.box.Min.Y <= top {
			continue
		}
		pt = image.Pt((t.box.Min.X+t.box.Max.X)/2, (t.box.Min.Y+t.box.Max.Y)/2)
		if rest != "" {
			pt.X = t.box.Min.X + t.box.Dy()/2
		}
		n++
	}
	if n != 1 {
		return image.Point{}, false
	}
	return pt, true
}

type fdaSwitchState string

const (
	fdaSwitchOn  fdaSwitchState = "on"
	fdaSwitchOff fdaSwitchState = "off"
)

type fdaSwitch struct {
	center image.Point
	state  fdaSwitchState
}

// fdaListRight returns the right edge of the Full Disk Access list that
// contains label. The list is as wide as the pane's left-aligned text
// column (the description and app names below the heading), plus room for
// the row switches; anything farther right, such as a notification, is
// outside the list.
func fdaListRight(texts []fdaText, label fdaText) (int, bool) {
	top := -1
	for _, t := range texts {
		if strings.Contains(strings.ToLower(t.text), "full disk access") && t.box.Max.Y <= label.box.Min.Y {
			top = max(top, t.box.Max.Y)
		}
	}
	if top < 0 {
		return 0, false
	}
	right := label.box.Max.X
	for _, t := range texts {
		if t.box.Min.Y >= top && t.box.Min.X < label.box.Max.X {
			right = max(right, t.box.Max.X)
		}
	}
	return right + 6*label.box.Dy(), true
}

// findRowSwitch locates the switch on the same row as label by scanning
// the row's center line from the label to the list's right edge. A switch
// is a run of non-background pixels about two label heights wide; a
// saturated (accent) fill reads as on, a gray fill as off. It reports
// false unless exactly one such run lies within the list.
func findRowSwitch(img image.Image, texts []fdaText, label fdaText) (fdaSwitch, bool) {
	h := label.box.Dy()
	b := img.Bounds()
	y := (label.box.Min.Y + label.box.Max.Y) / 2
	start := label.box.Max.X + h
	end, ok := fdaListRight(texts, label)
	end = min(end, b.Max.X)
	if !ok || h < 4 || y < b.Min.Y || y >= b.Max.Y || start >= end {
		return fdaSwitch{}, false
	}
	bg := rgb8(img, label.box.Max.X+h/2, y)

	type run struct{ minX, maxX, n, accent int }
	var runs []run
	gap := h * 3 / 2
	for x := start; x < end; x++ {
		c := rgb8(img, x, y)
		accent := saturation(c) > 60
		if !accent && channelDist(c, bg) <= 12 {
			continue
		}
		if len(runs) == 0 || x-runs[len(runs)-1].maxX > gap {
			runs = append(runs, run{minX: x})
		}
		r := &runs[len(runs)-1]
		r.maxX = x
		r.n++
		if accent {
			r.accent++
		}
	}
	var sw fdaSwitch
	n := 0
	for _, r := range runs {
		w := r.maxX - r.minX + 1
		if w*10 < h*12 || w > h*5 || r.n*100 < w*35 {
			continue
		}
		sw = fdaSwitch{center: image.Pt((r.minX+r.maxX)/2, y), state: fdaSwitchOff}
		if r.accent*2 > r.n {
			sw.state = fdaSwitchOn
		}
		n++
	}
	if n != 1 {
		return fdaSwitch{}, false
	}
	return sw, true
}

func rgb8(img image.Image, x, y int) [3]int {
	r, g, b, _ := img.At(x, y).RGBA()
	return [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
}

func saturation(c [3]int) int {
	return max(c[0], c[1], c[2]) - min(c[0], c[1], c[2])
}

func channelDist(a, b [3]int) int {
	d := 0
	for i := range a {
		d = max(d, a[i]-b[i], b[i]-a[i])
	}
	return d
}

// rowSelected reports whether label's row shows the accent selection
// highlight: most of the row's center line beside the label is saturated.
// An unselected row, or one with a gray (unfocused) highlight, reads false.
func rowSelected(img image.Image, label fdaText) bool {
	h := label.box.Dy()
	b := img.Bounds()
	y := (label.box.Min.Y + label.box.Max.Y) / 2
	x0, x1 := label.box.Max.X+h/2, min(label.box.Max.X+4*h, b.Max.X)
	if h < 4 || y < b.Min.Y || y >= b.Max.Y || x0 >= x1 {
		return false
	}
	n := 0
	for x := x0; x < x1; x++ {
		if saturation(rgb8(img, x, y)) > 60 {
			n++
		}
	}
	return n*10 >= (x1-x0)*8
}

// fdaSetup drives Full Disk Access setup from observed screen state. It
// sends input only to controls identified on the current screenshot, and
// after each input it sends nothing more until a later screenshot shows
// the expected transition or the transition wait expires. Authentication
// and ambiguous or unobservable states are handed to the user.
type fdaSetup struct {
	ui        fdaUI
	recognize func(image.Image) ([]fdaText, error)
	out       io.Writer
	timeout   time.Duration
	probe     func() (tccFDAStatus, string)
	reopen    func() error
	sleep     func(time.Duration)
	now       func() time.Time

	told         map[string]bool
	sent         map[string]int  // inputs sent, by action
	seen         map[string]bool // actions whose transition was observed
	pending      *fdaAction
	flow         bool // previous frame was reached from Full Disk Access
	unobservable int
}

// fdaAction is an input awaiting its observed effect.
type fdaAction struct {
	name string
	at   time.Time
	done func(fdaFrame) bool
}

const (
	fdaObserveInterval      = 2 * time.Second
	fdaTransitionWait       = time.Minute
	fdaSetupMaxPaneOpens    = 2
	fdaSetupMaxUnobservable = 3
)

var errFDAUnobservable = errors.New("COVE_TCC_FDA_UI_UNOBSERVABLE detail='guest display is black or has no text; no further input sent'")

func (s *fdaSetup) run() error {
	s.told = make(map[string]bool)
	s.sent = make(map[string]int)
	s.seen = make(map[string]bool)
	deadline := s.now().Add(s.timeout)
	state := fdaUIOther
	for ; ; s.sleep(fdaObserveInterval) {
		status, detail := s.probe()
		if status == tccFDAAuthorized {
			printTCCFDAStatus(s.out, status, detail)
			return nil
		}
		if !s.now().Before(deadline) {
			printTCCFDAStatus(s.out, status, detail)
			return fmt.Errorf("COVE_TCC_FDA_SETUP_TIMEOUT ui=%s timeout=%s", state, s.timeout)
		}
		f := s.observe()
		state = f.state
		if f.state == fdaUIUnobservable {
			if s.unobservable++; s.unobservable >= fdaSetupMaxUnobservable {
				return errFDAUnobservable
			}
			continue
		}
		s.unobservable = 0
		if a := s.pending; a != nil {
			switch {
			case a.done(f):
				s.seen[a.name] = true
				fmt.Fprintf(s.out, "COVE_TCC_FDA_UI_OBSERVED action=%s ui=%s\n", a.name, f.state)
			case f.state == fdaUIAuthPrompt:
			case s.now().Sub(a.at) >= fdaTransitionWait:
				fmt.Fprintf(s.out, "COVE_TCC_FDA_UI_NO_TRANSITION action=%s waited=%s ui=%s\n", a.name, fdaTransitionWait, f.state)
			default:
				continue
			}
			s.pending = nil
		}
		if err := s.step(f); err != nil {
			return err
		}
		switch f.state {
		case fdaUIPane, fdaUIPaneAgent:
			s.flow = true
		case fdaUIOther:
			s.flow = false
		}
	}
}

func (s *fdaSetup) observe() fdaFrame {
	img, err := s.ui.Screenshot()
	if err != nil || img == nil || DetectScreenState(img) == ScreenStateBlack {
		return fdaFrame{state: fdaUIUnobservable}
	}
	texts, err := s.recognize(img)
	if err != nil {
		return fdaFrame{state: fdaUIUnobservable}
	}
	var all []string
	for _, t := range texts {
		all = append(all, t.text)
	}
	return fdaFrame{state: classifyFDAScreen(strings.Join(all, "\n")), img: img, texts: texts}
}

func (s *fdaSetup) step(f fdaFrame) error {
	switch f.state {
	case fdaUIPane:
		return s.addAgent(f)
	case fdaUIPaneAgent:
		return s.enableAgent(f)
	case fdaUIAuthPrompt:
		s.handoff("auth", "authenticate in the guest window; cove does not type passwords")
	case fdaUIOpenPanel, fdaUIGoTo:
		if !s.flow {
			s.handoff("open-panel-context", "a file dialog is open that cove did not reach from Full Disk Access; cove sends it no input.\n"+
				"  Close it in the guest window.")
			return nil
		}
		return s.fillOpenPanel(f)
	case fdaUIOther:
		if s.sent["open-pane"] >= fdaSetupMaxPaneOpens {
			s.handoff("other", "return the guest window to Privacy & Security > Full Disk Access")
			return nil
		}
		return s.act("open-pane", func(g fdaFrame) bool {
			return g.state == fdaUIPane || g.state == fdaUIPaneAgent
		}, s.reopen)
	}
	return nil
}

// act sends one input and records the transition that confirms it.
func (s *fdaSetup) act(name string, done func(fdaFrame) bool, send func() error) error {
	s.sent[name]++
	fmt.Fprintf(s.out, "COVE_TCC_FDA_UI_INPUT action=%s attempt=%d\n", name, s.sent[name])
	s.pending = &fdaAction{name: name, at: s.now(), done: done}
	return send()
}

func (s *fdaSetup) addAgent(f fdaFrame) error {
	pt, ok := findAddControl(f.texts)
	if !ok {
		s.handoff("add-unidentified", "click + below the app list in the guest window; cove will fill in the agent path")
		return nil
	}
	if s.sent["add"] > 0 {
		s.handoff("add-no-effect", "clicking + did not open a file dialog; click + below the app list in the guest window")
		return nil
	}
	return s.act("add", func(g fdaFrame) bool {
		return g.state == fdaUIOpenPanel || g.state == fdaUIGoTo
	}, func() error { return s.click(f.img, pt) })
}

// agentSwitch reports the state of vz-agent's switch, or "" if the row or
// switch cannot be identified.
func agentSwitch(f fdaFrame) (fdaSwitch, string) {
	row, ok := findAgentRow(f.texts)
	if !ok {
		return fdaSwitch{}, "row-ambiguous"
	}
	sw, ok := findRowSwitch(f.img, f.texts, row)
	if !ok {
		return fdaSwitch{}, "switch-unidentified"
	}
	return sw, ""
}

func (s *fdaSetup) enableAgent(f fdaFrame) error {
	sw, problem := agentSwitch(f)
	switch problem {
	case "row-ambiguous":
		s.handoff(problem, "vz-agent is listed but its row could not be identified; turn its switch on in the guest window")
		return nil
	case "switch-unidentified":
		s.handoff(problem, "vz-agent is listed but exactly one switch could not be identified in its row; turn it on in the guest window")
		return nil
	}
	if sw.state == fdaSwitchOn {
		s.handoff("switch-on", "vz-agent's switch reads on, but the agent still cannot open protected files.\n"+
			"  The cause is not determined from the UI; cove sends no further input and keeps verifying.")
		return nil
	}
	if s.sent["enable"] > 0 {
		s.handoff("switch-no-effect", "vz-agent's switch still reads off after clicking it; turn it on in the guest window")
		return nil
	}
	return s.act("enable", func(g fdaFrame) bool {
		sw, problem := agentSwitch(g)
		return g.state != fdaUIPaneAgent || problem != "" || sw.state != fdaSwitchOff
	}, func() error { return s.click(f.img, sw.center) })
}

func agentPathVisible(f fdaFrame) bool {
	for _, t := range f.texts {
		if strings.Contains(t.text, strings.TrimPrefix(guestVZAgentPath, "/")) {
			return true
		}
	}
	return false
}

// fillOpenPanel selects the agent in the file dialog one observed step at
// a time: open the go-to-path sheet, type the path once the sheet's text is
// recognized, confirm it once the typed path is recognized, and press Open
// once the dialog shows the vz-agent row highlighted as selected. A missing
// observation hands off; a changed frame alone is never evidence.
func (s *fdaSetup) fillOpenPanel(f fdaFrame) error {
	const manual = "select /usr/local/bin/vz-agent in the guest file dialog and click Open"
	switch {
	case s.sent["go-to"] == 0:
		return s.act("go-to", func(g fdaFrame) bool {
			return g.state == fdaUIGoTo
		}, func() error { return s.ui.KeyPressWithModifiers(KeyCodeG, ModifierCommand|ModifierShift) })
	case !s.seen["go-to"]:
		s.handoff("go-to-unobserved", "the go-to-path sheet did not appear; "+manual)
	case s.sent["type-path"] == 0:
		return s.act("type-path", agentPathVisible, func() error { return s.ui.TypeText(guestVZAgentPath) })
	case !s.seen["type-path"]:
		s.handoff("path-unobserved", "the typed agent path did not appear; "+manual)
	case s.sent["go"] == 0:
		return s.act("go", func(g fdaFrame) bool {
			row, ok := findAgentRow(g.texts)
			return g.state == fdaUIOpenPanel && ok && !agentPathVisible(g) && rowSelected(g.img, row)
		}, func() error { return s.ui.KeyPress(KeyCodeReturn) })
	case !s.seen["go"]:
		s.handoff("selection-unobserved", "vz-agent was not observed selected in the file dialog; "+manual)
	case s.sent["open"] == 0:
		return s.act("open", func(g fdaFrame) bool {
			return g.state != fdaUIOpenPanel && g.state != fdaUIGoTo
		}, func() error { return s.ui.KeyPress(KeyCodeReturn) })
	default:
		s.handoff("open-panel", manual)
	}
	return nil
}

func (s *fdaSetup) click(img image.Image, pt image.Point) error {
	b := img.Bounds()
	x := float64(pt.X-b.Min.X) / float64(b.Dx())
	y := float64(pt.Y-b.Min.Y) / float64(b.Dy())
	if err := s.ui.MouseClick(x, y); err != nil {
		return fmt.Errorf("click at %.3f,%.3f: %w", x, y, err)
	}
	return nil
}

func (s *fdaSetup) handoff(key, msg string) {
	if s.told[key] {
		return
	}
	s.told[key] = true
	fmt.Fprintf(s.out, "COVE_TCC_FDA_UI_HANDOFF state=%s\n  %s\n", key, msg)
}
