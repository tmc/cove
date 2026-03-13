// vzscript_apply.go - CLI for running vzscript recipes.
package main

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/txtar"
	"rsc.io/script"
)

//go:embed vzscripts/*.vzscript
var builtinScripts embed.FS

// vzscriptCommand handles the "vzscript" subcommand.
func vzscriptCommand(args []string) error {
	if len(args) < 1 {
		return vzscriptUsage()
	}
	switch args[0] {
	case "list":
		return vzscriptList()
	case "show":
		return vzscriptShow(args[1:])
	case "run":
		return vzscriptRun(args[1:])
	default:
		return fmt.Errorf("unknown vzscript command: %s", args[0])
	}
}

func vzscriptUsage() error {
	fmt.Fprintf(os.Stderr, `Usage: vz-macos vzscript <command> [args...]

Commands:
  list                    List built-in recipes
  show <recipe>           Print recipe contents
  run [-v] [-timeout d] <recipe...>  Run one or more recipes against a running VM

A recipe is a txtar archive (see golang.org/x/tools/txtar) executed
by rsc.io/script. The comment section contains commands; files in
the archive are extracted to a working directory.

Guest commands:
  guest-wait [timeout]        Wait for VM boot and guest agent to be reachable
  guest-ping                  Check guest agent connectivity
  guest-exec <args...>        Run a command in the guest
  guest-shell <file>          Run a script file in the guest via bash
  guest-terminal <file>       Run a script in Terminal.app (visible in VM)
  guest-cp <host> <guest>     Copy a file host→guest (streaming, for large files)
  guest-cp -from-guest <host> <guest>  Copy guest→host
  guest-write <dst> <src>     Copy a local file to the guest (small files)
  guest-read <path>           Read a guest file to stdout

UI automation commands (via control socket):
  ocr-click <text> [timeout] [region]  Find text via OCR and click its center
  ocr-wait <text> [timeout] [region]   Wait until text appears on screen
  ocr-gone <text> [timeout] [region]   Wait until text disappears from screen
  ocr                         Run OCR; stdout is all recognized text
  screenshot [file]           Capture VM screen to JPEG file
  type <text>                 Type text into the VM
  key <spec>                  Send key event (return, tab, cmd+v, etc.)
  click <x> <y>              Click at normalized coordinates (0-1)
  wait <duration>             Sleep for a duration (1s, 500ms, 2m)
  detect-page                 Detect Setup Assistant page via OCR
  detect-screen               Detect screen state (desktop, login, etc.)

Conditions:
  [screen:<state>]            True if screen state matches (desktop, login, etc.)
  [page:<name>]               True if Setup Assistant page matches
  [text-visible:<text>]       True if text is visible on screen

Standard rsc.io/script commands (echo, cat, env, etc.) and conditions
([darwin], [GOOS:linux], etc.) are also available.
Use '!' prefix for expected-failure, '?' for don't-care.

Dependencies:
  Scripts can declare dependencies with "# requires: recipe1, recipe2"
  in the header. Dependencies are resolved automatically and each recipe
  runs at most once. Multiple recipes can be given on the command line.

Examples:
  vz-macos vzscript list
  vz-macos vzscript show homebrew
  vz-macos vzscript run developer-tools
  vz-macos vzscript run homebrew golang openclaw
  vz-macos vzscript run ./custom.vzscript
`)
	return fmt.Errorf("command required")
}

func vzscriptList() error {
	type entry struct {
		name, desc string
		requires   []string
	}
	var entries []entry

	files, err := fs.ReadDir(builtinScripts, "vzscripts")
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".vzscript") {
			continue
		}
		data, err := builtinScripts.ReadFile("vzscripts/" + f.Name())
		if err != nil {
			continue
		}
		ar := txtar.Parse(data)
		meta := parseScriptMeta(ar.Comment)
		name := meta.name
		if name == "" {
			name = strings.TrimSuffix(f.Name(), ".vzscript")
		}
		entries = append(entries, entry{name, meta.desc, meta.requires})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	fmt.Println("Built-in recipes:")
	for _, e := range entries {
		line := fmt.Sprintf("  %-20s", e.name)
		if e.desc != "" {
			line += " " + e.desc
		}
		if len(e.requires) > 0 {
			line += fmt.Sprintf(" (requires: %s)", strings.Join(e.requires, ", "))
		}
		fmt.Println(line)
	}
	return nil
}

func vzscriptShow(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("show requires a recipe name or path")
	}
	data, err := loadVZScriptData(args[0])
	if err != nil {
		return err
	}
	os.Stdout.Write(data)
	return nil
}

func vzscriptRun(args []string) error {
	// Reorder args so flags can appear after the recipe name.
	// Go's flag package stops parsing at the first non-flag arg, so
	// "vzscript run -vm=test1 golang -terminal -v" would leave -terminal
	// and -v unparsed. We move the recipe name to the end.
	args = reorderArgsForFlags(args)

	rf := flag.NewFlagSet("vzscript run", flag.ExitOnError)
	socketPath := rf.String("socket", "", "Control socket path")
	timeout := rf.Duration("timeout", 10*time.Minute, "Timeout for guest-exec commands")
	verbose := rf.Bool("v", false, "Verbose output")
	terminal := rf.Bool("terminal", false, "Run guest-shell commands in Terminal.app (visible in VM GUI)")
	autoApprove := rf.Bool("auto-approve", false, "Auto-click Allow/OK on system dialogs via OCR")
	vm := rf.String("vm", "", "VM name (default: active VM or 'default')")
	if err := rf.Parse(args); err != nil {
		return err
	}
	if rf.NArg() < 1 {
		return fmt.Errorf("run requires a recipe name or path")
	}

	// Update global vmDir if -vm was specified.
	if *vm != "" {
		dir, err := EnsureVMDir(*vm)
		if err != nil {
			return err
		}
		vmDir = dir
		vmName = *vm
	}

	sock := *socketPath
	if sock == "" {
		sock = GetControlSocketPath()
	}

	cfg := vzscriptConfig{
		socketPath:  sock,
		execTimeout: *timeout,
		verbose:     *verbose,
		terminal:    *terminal,
		autoApprove: *autoApprove,
	}

	// Collect all recipe names from positional args.
	recipes := rf.Args()

	// Resolve dependencies and run in order.
	return runVZScriptWithDeps(recipes, cfg)
}

// runVZScriptWithDeps resolves dependencies for the given recipes and runs
// them in topological order. Each recipe is run at most once.
func runVZScriptWithDeps(recipes []string, cfg vzscriptConfig) error {
	// Start background auto-approver if enabled (shared across all recipes).
	if cfg.autoApprove {
		stop := startAutoApprover(cfg.socketPath, cfg.verbose)
		defer stop()
	}

	completed := map[string]bool{}
	var run func(name string) error
	run = func(name string) error {
		if completed[name] {
			return nil
		}
		data, err := loadVZScriptData(name)
		if err != nil {
			return err
		}
		ar := txtar.Parse(data)
		meta := parseScriptMeta(ar.Comment)

		// Run dependencies first.
		for _, dep := range meta.requires {
			if err := run(dep); err != nil {
				return fmt.Errorf("dependency %s (required by %s): %w", dep, name, err)
			}
		}

		if completed[name] {
			return nil
		}
		if cfg.verbose {
			fmt.Fprintf(os.Stderr, "--- running recipe: %s ---\n", name)
		}
		if err := runVZScript(data, name, cfg); err != nil {
			return err
		}
		completed[name] = true
		return nil
	}

	for _, recipe := range recipes {
		if err := run(recipe); err != nil {
			return err
		}
	}
	return nil
}

func runVZScript(data []byte, name string, cfg vzscriptConfig) error {
	ar := txtar.Parse(data)
	engine := newVZScriptEngine(cfg)

	workdir, err := os.MkdirTemp("", "vzscript-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	state, err := script.NewState(context.Background(), workdir, os.Environ())
	if err != nil {
		return err
	}
	if err := state.ExtractFiles(ar); err != nil {
		return fmt.Errorf("extract files: %w", err)
	}

	var log bytes.Buffer
	var out io.Writer = &log
	if cfg.verbose {
		out = cfg.logWriter
		if out == nil {
			out = os.Stderr
		}
	}
	err = engine.Execute(state, name, bufio.NewReader(bytes.NewReader(ar.Comment)), out)
	if !cfg.verbose && log.Len() > 0 {
		os.Stderr.Write(log.Bytes())
	}
	return err
}

// startAutoApprover starts a background goroutine that polls the VM screen
// via OCR and auto-clicks "Allow" or "OK" buttons on system dialogs (e.g.,
// TCC permission prompts). Returns a stop function.
func startAutoApprover(sock string, verbose bool) func() {
	done := make(chan struct{})
	go func() {
		// Buttons to look for, in priority order.
		// "Allow" handles TCC dialogs ("vz-agent wants to control Terminal").
		// "OK" handles generic confirmation dialogs.
		buttons := []string{"Allow", "OK"}

		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				autoApproveOnce(sock, buttons, verbose)
			}
		}
	}()
	return func() { close(done) }
}

// autoApproveOnce runs one OCR pass looking for dialog buttons to click.
func autoApproveOnce(sock string, buttons []string, verbose bool) {
	// Get all text on screen.
	resp, err := ctlSendOCR(sock, "ocr-all-text", "", "", 5*time.Second)
	if err != nil || !resp.Success {
		return
	}
	screenText := resp.Data

	// Only act if this looks like a dialog (contains permission-related keywords).
	if !looksLikePermissionDialog(screenText) {
		return
	}

	if verbose {
		fmt.Fprintf(os.Stderr, "[auto-approve] detected dialog, looking for buttons...\n")
	}

	// Try to click the first matching button.
	for _, btn := range buttons {
		if !strings.Contains(screenText, btn) {
			continue
		}
		resp, err := ctlSendOCR(sock, "ocr-click", btn, "3s", 5*time.Second)
		if err != nil {
			continue
		}
		if resp.Success {
			fmt.Fprintf(os.Stderr, "[auto-approve] clicked %q\n", btn)
			// Wait a moment for the dialog to dismiss before next poll.
			time.Sleep(2 * time.Second)
			return
		}
	}
}

// looksLikePermissionDialog checks if screen text suggests a TCC/permission dialog.
// TCC dialogs have a specific pattern: a request phrase AND both "Don't Allow" and
// "Allow" buttons. We require both to avoid false positives from System Settings.
func looksLikePermissionDialog(text string) bool {
	lower := strings.ToLower(text)

	// Must have "Don't Allow" — this is specific to TCC dialogs.
	if !strings.Contains(lower, "don't allow") && !strings.Contains(lower, "don\u2019t allow") {
		return false
	}

	// Must also have a request phrase.
	requestPhrases := []string{
		"would like to",    // "X would like to access/control Y"
		"wants to access",  // "X wants to access Y"
		"wants to control", // "vz-agent wants to control Terminal"
		"wants access to",  // accessibility prompts
	}
	for _, phrase := range requestPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

// reorderArgsForFlags moves the first non-flag argument (the recipe name)
// to the end so that Go's flag package can parse all flags regardless of
// where they appear. This allows:
//
//	vzscript run -vm=test1 golang -terminal -v
//
// to work the same as:
//
//	vzscript run -vm=test1 -terminal -v golang
func reorderArgsForFlags(args []string) []string {
	var flags []string
	var positional []string
	i := 0
	for i < len(args) {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// Check if this flag takes a value as the next arg
			// (e.g., "-vm test1" vs "-vm=test1").
			if !strings.Contains(a, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				// Could be a flag value. Heuristic: known value-taking flags.
				name := strings.TrimLeft(a, "-")
				if flagTakesValue(name) {
					i++
					flags = append(flags, args[i])
				}
			}
		} else {
			positional = append(positional, a)
		}
		i++
	}
	return append(flags, positional...)
}

// flagTakesValue returns true for vzscript run flags that take a value argument.
func flagTakesValue(name string) bool {
	switch name {
	case "socket", "timeout", "vm":
		return true
	}
	return false
}

func loadVZScriptData(nameOrPath string) ([]byte, error) {
	if _, err := os.Stat(nameOrPath); err == nil {
		return os.ReadFile(nameOrPath)
	}
	name := nameOrPath
	if !strings.HasSuffix(name, ".vzscript") {
		name += ".vzscript"
	}
	data, err := builtinScripts.ReadFile("vzscripts/" + name)
	if err != nil {
		data, err = builtinScripts.ReadFile("vzscripts/" + filepath.Base(name))
		if err != nil {
			return nil, fmt.Errorf("recipe not found: %s (not a file and not a built-in)", nameOrPath)
		}
	}
	return data, nil
}

// scriptMeta holds parsed metadata from a vzscript header.
type scriptMeta struct {
	name     string
	desc     string
	requires []string // recipe names this script depends on
}

// parseScriptMeta extracts metadata from script comment lines.
// Recognized headers:
//
//	# name — description   (first non-blank comment line)
//	# requires: a, b       (dependency list)
func parseScriptMeta(comment []byte) scriptMeta {
	var m scriptMeta
	s := bufio.NewScanner(bytes.NewReader(comment))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || line == "#" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			break
		}
		text := strings.TrimSpace(strings.TrimPrefix(line, "#"))

		// Check for "requires:" directive.
		if strings.HasPrefix(strings.ToLower(text), "requires:") {
			deps := strings.TrimSpace(text[len("requires:"):])
			for _, dep := range strings.Split(deps, ",") {
				dep = strings.TrimSpace(dep)
				if dep != "" {
					m.requires = append(m.requires, dep)
				}
			}
			continue
		}

		// First non-directive comment is the name/description.
		if m.name == "" {
			for _, sep := range []string{" — ", " - "} {
				if i := strings.Index(text, sep); i >= 0 {
					m.name = strings.TrimSpace(text[:i])
					m.desc = strings.TrimSpace(text[i+len(sep):])
					break
				}
			}
			if m.name == "" {
				m.name = text
			}
		}
	}
	return m
}
