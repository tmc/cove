package guibench

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tmc/cove/internal/controlclient"
)

// Probe is the getter transport: the minimal slice of the control socket a
// Tier-A getter needs to read live state off a forked guest. *controlclient.Client
// satisfies it via [ClientProbe]; tests use [FakeProbe]. Keeping the surface
// small makes getters unit-testable without a VM (design 047 §5).
type Probe interface {
	// Exec runs a command in the guest and returns its exit code and output.
	Exec(args []string, env map[string]string, workDir string) (exitCode int, stdout, stderr string, err error)
	// ReadFile reads a user-space file off the guest.
	ReadFile(path string) ([]byte, error)
	// OCRAllText returns all text recognized on the guest display.
	OCRAllText() (string, error)
}

// GetterSpec declares how to read one value off the guest. Kind selects a
// getter; the remaining fields are kind-specific and may contain {PARAM}
// placeholders resolved against materialized task parameters before the getter
// runs. Getters are classified by the TCC grant they need (see [Tier]):
// Tier-A getters read user-space state and need no grant; Tier-B getters
// (sqlite, protected_file, tccdb) need Full Disk Access; Tier-C getters
// (applescript, accessibility) need Apple Events and Accessibility (design 047
// §5). Every Tier-B/C getter runs through the same exec transport, so no new
// guest-agent surface is required (the §13 osascript one-shot path).
type GetterSpec struct {
	Kind    string            `json:"kind"`               // see getterKinds
	Args    []string          `json:"args,omitempty"`     // exec: command argv
	Env     map[string]string `json:"env,omitempty"`      // exec: environment
	WorkDir string            `json:"work_dir,omitempty"` // exec: working directory
	Path    string            `json:"path,omitempty"`     // file/protected_file: guest path; sqlite/tccdb: db path
	Domain  string            `json:"domain,omitempty"`   // defaults: preference domain
	Key     string            `json:"key,omitempty"`      // defaults: preference key
	Field   string            `json:"field,omitempty"`    // exec: "stdout" (default) | "exit"
	Query   string            `json:"query,omitempty"`    // sqlite/tccdb: SQL query (single scalar)
	Script  string            `json:"script,omitempty"`   // applescript: AppleScript or JXA source
	JXA     bool              `json:"jxa,omitempty"`      // applescript: run as JavaScript for Automation
	App     string            `json:"app,omitempty"`      // accessibility: target application name
	Element string            `json:"element,omitempty"`  // accessibility: AX element selector
	Attr    string            `json:"attr,omitempty"`     // accessibility: AX attribute to read
	Dump    bool              `json:"dump,omitempty"`     // accessibility: emit the front-window AX tree as XML instead of one attr
}

// getterKinds is the set of valid getter kinds, with the privilege tier each
// one requires (design 047 §5).
var getterKinds = map[string]Tier{
	"exec":           TierA,
	"file":           TierA,
	"defaults":       TierA,
	"screen_ocr":     TierA,
	"sqlite":         TierB,
	"protected_file": TierB,
	"tccdb":          TierB,
	"applescript":    TierC,
	"accessibility":  TierC,
}

// validate checks the spec's kind and required fields.
func (g GetterSpec) validate() error {
	if _, ok := getterKinds[g.Kind]; !ok {
		return fmt.Errorf("unknown getter kind %q", g.Kind)
	}
	switch g.Kind {
	case "exec":
		if len(g.Args) == 0 {
			return fmt.Errorf("exec getter: args is empty")
		}
		switch g.Field {
		case "", "stdout", "exit":
		default:
			return fmt.Errorf("exec getter: invalid field %q", g.Field)
		}
	case "file", "protected_file":
		if g.Path == "" {
			return fmt.Errorf("%s getter: path is empty", g.Kind)
		}
	case "defaults":
		if g.Domain == "" || g.Key == "" {
			return fmt.Errorf("defaults getter: domain and key required")
		}
	case "sqlite":
		if g.Path == "" {
			return fmt.Errorf("sqlite getter: path is empty")
		}
		if g.Query == "" {
			return fmt.Errorf("sqlite getter: query is empty")
		}
	case "tccdb":
		if g.Query == "" {
			return fmt.Errorf("tccdb getter: query is empty")
		}
	case "applescript":
		if g.Script == "" {
			return fmt.Errorf("applescript getter: script is empty")
		}
	case "accessibility":
		if g.App == "" {
			return fmt.Errorf("accessibility getter: app is empty")
		}
		// A dump reads the whole front-window AX tree, so it needs no single
		// attribute; a scalar read requires Attr.
		if !g.Dump && g.Attr == "" {
			return fmt.Errorf("accessibility getter: attr is empty")
		}
	}
	return nil
}

// Tier reports the privilege tier the getter requires (design 047 §5). It is
// used to verify the base image carries exactly the grants its corpus needs.
func (g GetterSpec) Tier() Tier {
	return getterKinds[g.Kind]
}

// Get reads the spec's value off the guest through p, after materializing any
// {PARAM} placeholders. The returned string is the value a [Metric] scores.
func (g GetterSpec) Get(p Probe, params map[string]string) (string, error) {
	switch g.Kind {
	case "exec":
		return getExec(p, g, params)
	case "file":
		return getFile(p, g, params)
	case "protected_file":
		return getProtectedFile(p, g, params)
	case "defaults":
		return getDefaults(p, g, params)
	case "screen_ocr":
		return getScreenOCR(p)
	case "sqlite":
		return getSQLite(p, g, params)
	case "tccdb":
		return getTCCDB(p, g, params)
	case "applescript":
		return getAppleScript(p, g, params)
	case "accessibility":
		return getAccessibility(p, g, params)
	default:
		return "", fmt.Errorf("unknown getter kind %q", g.Kind)
	}
}

// getExec runs the command and returns either trimmed stdout (default) or the
// exit code as a string (field "exit"). A nonzero exit is not an error: it is
// the value the metric scores.
func getExec(p Probe, g GetterSpec, params map[string]string) (string, error) {
	args := materializeArgs(g.Args, params)
	exit, stdout, stderr, err := p.Exec(args, g.Env, g.WorkDir)
	if err != nil {
		return "", fmt.Errorf("exec getter: %w", err)
	}
	if g.Field == "exit" {
		return strconv.Itoa(exit), nil
	}
	if stdout == "" && stderr != "" && exit != 0 {
		// Surface stderr only when there is no stdout and the command failed,
		// so a metric can match on a known error string if it wants to.
		return strings.TrimRight(stderr, "\n"), nil
	}
	return strings.TrimRight(stdout, "\n"), nil
}

// getFile reads a user-space file and returns its contents as a string.
func getFile(p Probe, g GetterSpec, params map[string]string) (string, error) {
	path := Materialize(g.Path, params)
	b, err := p.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("file getter: %w", err)
	}
	return string(b), nil
}

// getDefaults reads a preference through `defaults read <domain> <key>`. Going
// through the defaults CLI routes the read via cfprefsd, which returns the live
// value rather than a stale on-disk plist (design 047 §7).
func getDefaults(p Probe, g GetterSpec, params map[string]string) (string, error) {
	domain := Materialize(g.Domain, params)
	key := Materialize(g.Key, params)
	exit, stdout, stderr, err := p.Exec([]string{"defaults", "read", domain, key}, nil, "")
	if err != nil {
		return "", fmt.Errorf("defaults getter: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("defaults getter: read %s %s exited %d: %s", domain, key, exit, strings.TrimSpace(stderr))
	}
	return strings.TrimRight(stdout, "\n"), nil
}

// getScreenOCR returns all text recognized on the guest display.
func getScreenOCR(p Probe) (string, error) {
	text, err := p.OCRAllText()
	if err != nil {
		return "", fmt.Errorf("screen_ocr getter: %w", err)
	}
	return text, nil
}

// getProtectedFile reads a TCC-protected ~/Library path through `cat`, so a
// Full Disk Access denial surfaces as a nonzero exit (a hard error) rather than
// the silent failure a direct file read would give (design 047 §5, §8). It is
// the Tier-B counterpart to getFile, which is for user-space paths only.
func getProtectedFile(p Probe, g GetterSpec, params map[string]string) (string, error) {
	path := Materialize(g.Path, params)
	exit, stdout, stderr, err := p.Exec([]string{"cat", path}, nil, "")
	if err != nil {
		return "", fmt.Errorf("protected_file getter: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("protected_file getter: cat %s exited %d (check FDA grant): %s", path, exit, strings.TrimSpace(stderr))
	}
	return stdout, nil
}

// getSQLite runs Query against the SQLite db at Path through the guest's
// sqlite3 CLI, after first checkpointing the write-ahead log so the read sees
// committed-but-unflushed writes (design 047 §7). A nonzero exit (e.g. an FDA
// denial reading a protected app store) is a hard error, never a stale read.
func getSQLite(p Probe, g GetterSpec, params map[string]string) (string, error) {
	path := Materialize(g.Path, params)
	query := Materialize(g.Query, params)
	// One sqlite3 invocation: checkpoint the WAL, then run the query, so the
	// scalar result reflects settled state.
	sql := "PRAGMA wal_checkpoint(FULL);\n" + query
	exit, stdout, stderr, err := p.Exec([]string{"sqlite3", path, sql}, nil, "")
	if err != nil {
		return "", fmt.Errorf("sqlite getter: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("sqlite getter: query %s exited %d (check FDA grant): %s", path, exit, strings.TrimSpace(stderr))
	}
	// PRAGMA wal_checkpoint(FULL) prints a "busy|log|checkpointed" row before the
	// query output; drop that leading line so the caller sees only the query
	// result.
	return dropCheckpointLine(strings.TrimRight(stdout, "\n")), nil
}

// getTCCDB reads the system or user TCC database, the canonical store of grant
// state (design 047 §5, Tier B). When Path is empty it defaults to the system
// TCC db. The read itself needs Full Disk Access. Like getSQLite it checkpoints
// the WAL before the query so the grant state reflects settled writes: tccd
// keeps TCC.db in WAL mode, so a just-granted permission lives in -wal until a
// checkpoint and would otherwise read stale (design 047 §7).
func getTCCDB(p Probe, g GetterSpec, params map[string]string) (string, error) {
	path := Materialize(g.Path, params)
	if path == "" {
		path = "/Library/Application Support/com.apple.TCC/TCC.db"
	}
	// One sqlite3 invocation: checkpoint the WAL, then run the query, so the
	// result reflects settled grant state.
	sql := "PRAGMA wal_checkpoint(FULL);\n" + Materialize(g.Query, params)
	exit, stdout, stderr, err := p.Exec([]string{"sqlite3", path, sql}, nil, "")
	if err != nil {
		return "", fmt.Errorf("tccdb getter: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("tccdb getter: query %s exited %d (check FDA grant): %s", path, exit, strings.TrimSpace(stderr))
	}
	return dropCheckpointLine(strings.TrimRight(stdout, "\n")), nil
}

// getAppleScript runs a one-shot AppleScript (or JXA, when JXA is set) through
// the guest's osascript, returning its stdout. This is the §13 one-shot path
// (no guest-agent RPC, no proto bump). osascript needs the Apple Events grant,
// baked into the base image (Tier C); a denial surfaces as a nonzero exit.
func getAppleScript(p Probe, g GetterSpec, params map[string]string) (string, error) {
	script := Materialize(g.Script, params)
	args := []string{"osascript"}
	if g.JXA {
		args = append(args, "-l", "JavaScript")
	}
	args = append(args, "-e", script)
	exit, stdout, stderr, err := p.Exec(args, nil, "")
	if err != nil {
		return "", fmt.Errorf("applescript getter: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("applescript getter: osascript exited %d (check Apple Events grant): %s", exit, strings.TrimSpace(stderr))
	}
	return strings.TrimRight(stdout, "\n"), nil
}

// getAccessibility reads guest GUI state through System Events, the reliable
// synchronous AX probe (design 047 §5). With Dump set it returns the front
// window's AX subtree as an XML document (see [axDumpScript]) for the
// accessibility_match metric to select over; otherwise it returns a single
// attribute (Attr) off App's front window, optionally narrowed by Element.
// This needs the Accessibility grant (Tier C, independent of Apple Events /
// FDA); a denial surfaces as a nonzero exit.
func getAccessibility(p Probe, g GetterSpec, params map[string]string) (string, error) {
	app := Materialize(g.App, params)
	if g.Dump {
		exit, stdout, stderr, err := p.Exec([]string{"osascript", "-l", "JavaScript", "-e", axDumpScript(app)}, nil, "")
		if err != nil {
			return "", fmt.Errorf("accessibility getter: %w", err)
		}
		if exit != 0 {
			return "", fmt.Errorf("accessibility getter: dump tree of %s exited %d (check Accessibility grant): %s", app, exit, strings.TrimSpace(stderr))
		}
		return strings.TrimSpace(stdout), nil
	}
	element := Materialize(g.Element, params)
	attr := Materialize(g.Attr, params)
	script := axScript(app, element, attr)
	exit, stdout, stderr, err := p.Exec([]string{"osascript", "-e", script}, nil, "")
	if err != nil {
		return "", fmt.Errorf("accessibility getter: %w", err)
	}
	if exit != 0 {
		return "", fmt.Errorf("accessibility getter: read %s of %s exited %d (check Accessibility grant): %s", attr, app, exit, strings.TrimSpace(stderr))
	}
	return strings.TrimRight(stdout, "\n"), nil
}

// axScript builds the System Events AppleScript that reads attr off the named
// app's front window (optionally an element within it). Names are quoted with
// quoteAS so a value with a quote cannot break out of the string literal.
func axScript(app, element, attr string) string {
	target := "front window"
	if element != "" {
		target = "UI element " + quoteAS(element) + " of front window"
	}
	return fmt.Sprintf(
		"tell application \"System Events\" to tell process %s to get %s of %s",
		quoteAS(app), attr, target,
	)
}

// axDumpScript builds the JXA (JavaScript for Automation) program that walks
// the front window's UI-element subtree of the named process via System Events
// and prints it as the XML document the accessibility_match metric selects
// over: a <node> per UI element carrying role, title, identifier (subrole), and
// value attributes, nested by containment. Depth is capped so a deep view
// hierarchy cannot run unbounded. The app name is JSON-quoted so it cannot
// break out of the string literal.
//
// The emitted shape (one line per run, reformatted here) is:
//
//	<ax app="Notes">
//	  <node role="AXWindow" title="Notes" identifier="" value="">
//	    <node role="AXTextArea" title="" identifier="" value="Buy milk"/>
//	  </node>
//	</ax>
func axDumpScript(app string) string {
	// xmlEsc and a recursive walk live inside the JXA program so the whole dump
	// is one osascript invocation (the §13 one-shot path, no guest-agent RPC).
	return fmt.Sprintf(`
function xmlEsc(s){return String(s==null?"":s).replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;");}
function attr(e,name){try{return e[name]();}catch(x){return "";}}
function walk(e,depth){
  if(depth>20){return "";}
  var role=attr(e,"role"), title=attr(e,"title"), ident=attr(e,"subrole"), value=attr(e,"value");
  var open='<node role="'+xmlEsc(role)+'" title="'+xmlEsc(title)+'" identifier="'+xmlEsc(ident)+'" value="'+xmlEsc(value)+'">';
  var kids="";
  var children;
  try{children=e.uiElements();}catch(x){children=[];}
  for(var i=0;i<children.length;i++){kids+=walk(children[i],depth+1);}
  return open+kids+'</node>';
}
var se=Application("System Events");
var proc=se.processes[%s];
var out='<ax app="'+xmlEsc(%s)+'">';
var wins=proc.windows();
if(wins.length>0){out+=walk(wins[0],0);}
out+='</ax>';
out;`, quoteJS(app), quoteJS(app))
}

// quoteJS wraps s as a JavaScript double-quoted string literal, escaping
// backslashes and quotes so the value cannot break out of the literal.
func quoteJS(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return `"` + r.Replace(s) + `"`
}

// quoteAS wraps s in AppleScript double quotes, escaping embedded backslashes
// and quotes so the value cannot break out of the literal.
func quoteAS(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// dropCheckpointLine removes the leading "busy|log|checkpointed" row that
// `PRAGMA wal_checkpoint(FULL)` emits, returning the query output that follows.
// When the output has no such row (e.g. a single-line query result), it is
// returned unchanged only if it does not look like a checkpoint row.
func dropCheckpointLine(out string) string {
	nl := strings.IndexByte(out, '\n')
	if nl < 0 {
		if isCheckpointRow(out) {
			return ""
		}
		return out
	}
	if isCheckpointRow(out[:nl]) {
		return out[nl+1:]
	}
	return out
}

// isCheckpointRow reports whether line is the three-integer row sqlite emits
// for a WAL checkpoint pragma (e.g. "0|12|12").
func isCheckpointRow(line string) bool {
	parts := strings.Split(line, "|")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.Atoi(strings.TrimSpace(p)); err != nil {
			return false
		}
	}
	return true
}

// materializeArgs applies parameter substitution to each argv entry.
func materializeArgs(args []string, params map[string]string) []string {
	if len(params) == 0 {
		return args
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = Materialize(a, params)
	}
	return out
}

// ClientProbe adapts a *controlclient.Client to the Probe interface. It is the
// production transport; tests use [FakeProbe] instead, so this adapter is only
// exercised against a live client.
type ClientProbe struct {
	Client *controlclient.Client
}

// Exec runs args in the guest via the control client.
func (cp ClientProbe) Exec(args []string, env map[string]string, workDir string) (int, string, string, error) {
	resp, err := cp.Client.AgentExecTyped(args, env, workDir)
	if err != nil {
		return 0, "", "", err
	}
	return int(resp.GetExitCode()), resp.GetStdout(), resp.GetStderr(), nil
}

// ReadFile reads a guest file via the control client.
func (cp ClientProbe) ReadFile(path string) ([]byte, error) {
	return cp.Client.AgentReadFile(path)
}

// OCRAllText returns recognized display text via the control client.
func (cp ClientProbe) OCRAllText() (string, error) {
	return cp.Client.OCRAllText()
}
