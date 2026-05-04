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
	"sync"
	"time"

	agentstate "github.com/tmc/vz-macos/internal/agent"
	"github.com/tmc/vz-macos/internal/vmconfig"
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
		return fmt.Errorf("unknown vzscript command: %s\nValid commands: list, show, run", args[0])
	}
}

func vzscriptUsage() error {
	printVzscriptUsage(os.Stderr)
	return fmt.Errorf("command required")
}

func printVzscriptUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: cove vzscript <command> [args...]

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
  guest-cp -from-guest <guest> <host>  Copy guest→host
  host-cp <host> <guest>      Copy host file/directory to guest (30m timeout)
  guest-write <dst> <src>     Copy a local file to the guest (small files)
  guest-read <path>           Read a guest file to stdout

UI automation commands (via control socket):
  ocr-click <text> [timeout] [region]  Find text via OCR and click its center
  ocr-wait <text> [timeout] [region]   Wait until text appears on screen
  ocr-gone <text> [timeout] [region]   Wait until text disappears from screen
  ocr                         Run OCR; stdout is all recognized text
  screenshot [file]           Capture VM screen to JPEG file
  reboot-to-recovery [timeout] Stop VM and start macOS Recovery
  type <text>                 Type text into the VM
  key <spec>                  Send key event (return, tab, cmd+v, etc.)
  click <x> <y>              Click at normalized coordinates (0-1)
  wait <duration>             Sleep for a duration (1s, 500ms, 2m)
  detect-page                 Detect Setup Assistant page via OCR
  detect-screen               Detect screen state (desktop, login, etc.)

Conditions:
  [screen:desktop]            True if screen state matches (desktop, login, etc.)
  [page:language]             True if Setup Assistant page matches
  [text-visible:Continue]     True if text is visible on screen
  [text-visible:Not+Now]      Space and punctuation use URL encoding

Standard rsc.io/script commands (echo, cat, env, etc.) and conditions
([darwin], [GOOS:linux], etc.) are also available.
Use '!' prefix for expected-failure, '?' for don't-care.

Dependencies:
  Scripts can declare dependencies with "# requires: recipe1, recipe2"
  in the header. Dependencies are resolved automatically and each recipe
  runs at most once. Multiple recipes can be given on the command line.

Host mounts:
  Scripts can declare host directories to mount via VirtioFS with
  "# mount: <host-path> [ro|rw]" in the header. Paths support ~/
  expansion. Default mode is rw. Mounts are registered as shared folders,
  hot-plugged into the running VM, and mounted in the guest automatically.

Examples:
  cove vzscript list
  cove vzscript show homebrew
  cove vzscript run developer-tools
  cove vzscript run homebrew golang openclaw
  cove vzscript run ./custom.vzscript
  cove vzscript run -template -var Mode=disable -var Reboot=true ./custom.vzscript.tmpl
`)
}

func vzscriptList() error {
	type entry struct {
		name, desc string
		requires   []string
		mounts     int
		template   bool
	}
	var entries []entry

	files, err := fs.ReadDir(builtinScripts, "vzscripts")
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.IsDir() || (!strings.HasSuffix(f.Name(), ".vzscript") && !strings.HasSuffix(f.Name(), ".vzscript.tmpl")) {
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
			name = strings.TrimSuffix(f.Name(), ".vzscript.tmpl")
			name = strings.TrimSuffix(name, ".vzscript")
		}
		entries = append(entries, entry{name, meta.desc, meta.requires, len(meta.mounts), strings.HasSuffix(f.Name(), ".tmpl")})
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
		if e.mounts > 0 {
			line += fmt.Sprintf(" (mounts: %d)", e.mounts)
		}
		if e.template {
			line += " (template)"
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
	daemon := rf.Bool("daemon", false, "Route guest commands through daemon agent (root) instead of user agent")
	vm := rf.String("vm", "", "VM name (default: active VM or 'default')")
	parallel := rf.Bool("parallel", false, "Run independent recipes concurrently")
	renderTemplate := rf.Bool("template", false, "Render recipes as Go text/templates before running")
	templateVars := templateVarFlag{}
	rf.Var(&templateVars, "var", "Template variable name=value (repeatable)")
	if err := rf.Parse(args); err != nil {
		return err
	}
	if rf.NArg() < 1 {
		return fmt.Errorf("run requires a recipe name or path")
	}

	// Resolve socket path without mutating global vmDir.
	sock := *socketPath
	if sock == "" {
		if *vm != "" {
			dir, err := vmconfig.EnsureDir(*vm, vmDir)
			if err != nil {
				return err
			}
			sock = GetControlSocketPathForVM(dir)
		} else {
			sock = GetControlSocketPath()
		}
	}

	cfg := vzscriptConfig{
		socketPath:   sock,
		execTimeout:  *timeout,
		verbose:      *verbose,
		terminal:     *terminal,
		autoApprove:  *autoApprove,
		daemon:       *daemon,
		template:     *renderTemplate,
		templateVars: templateVars,
		guestOS:      vzscriptGuestOSForSocket(sock),
	}

	// Open a persistent log file in the VM directory.
	logFile, err := openVZScriptLog(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: vzscript log: %v\n", err)
	} else {
		defer logFile.Close()
		cfg.hostLogFile = logFile
	}

	// Collect all recipe names from positional args.
	recipes := rf.Args()

	if *parallel {
		return runVZScriptsParallel(recipes, cfg)
	}
	// Resolve dependencies and run in order.
	return runVZScriptWithDeps(recipes, cfg)
}

// runVZScriptWithDeps resolves dependencies for the given recipes and runs
// them in topological order. Each recipe is run at most once. Cycles are
// detected and reported as errors, and missing dependencies fail loudly
// before any recipe body runs.
func runVZScriptWithDeps(recipes []string, cfg vzscriptConfig) error {
	// Start background auto-approver if enabled (shared across all recipes).
	if cfg.autoApprove {
		stop := startAutoApprover(cfg.socketPath, cfg.verbose)
		defer stop()
	}

	completed := map[string]bool{}
	inProgress := map[string]bool{}
	var run func(name, requiredBy string) error
	run = func(name, requiredBy string) error {
		if completed[name] {
			return nil
		}
		if inProgress[name] {
			return fmt.Errorf("dependency cycle detected at %s", name)
		}
		data, err := loadVZScriptData(name)
		if err != nil {
			if requiredBy != "" {
				return fmt.Errorf("dependency %q (required by %s) cannot be resolved: %w", name, requiredBy, err)
			}
			return err
		}
		data, err = maybeRenderVZScript(data, name, cfg)
		if err != nil {
			return fmt.Errorf("recipe %s: template: %w", name, err)
		}
		ar := txtar.Parse(data)
		meta := parseScriptMeta(ar.Comment)
		if err := checkVZScriptGuestOS(name, meta, cfg.guestOS); err != nil {
			return err
		}

		inProgress[name] = true
		defer delete(inProgress, name)

		// Run dependencies first.
		for _, dep := range meta.requires {
			if completed[dep] {
				continue
			}
			fmt.Fprintf(os.Stderr, "running dependency: %s (required by %s)\n", dep, name)
			if err := run(dep, name); err != nil {
				return err
			}
		}

		if completed[name] {
			return nil
		}
		// Apply mount directives before running the script.
		if len(meta.mounts) > 0 {
			if err := applyMountDirectives(meta.mounts, cfg.socketPath, cfg.verbose); err != nil {
				return fmt.Errorf("recipe %s: mount: %w", name, err)
			}
		}
		if cfg.verbose {
			fmt.Fprintf(os.Stderr, "--- running recipe: %s ---\n", name)
		}
		rcfg := cfgForRecipe(cfg, meta)
		rcfg.template = false
		if err := runVZScript(data, name, rcfg); err != nil {
			return err
		}
		completed[name] = true
		return nil
	}

	for _, recipe := range recipes {
		if err := run(recipe, ""); err != nil {
			return err
		}
	}
	return nil
}

// uiCommands are vzscript commands that interact with the VM display.
// Recipes using these must run sequentially to avoid input conflicts.
var uiCommands = map[string]bool{
	"ocr-click":     true,
	"ocr-wait":      true,
	"ocr-gone":      true,
	"ocr":           true,
	"screenshot":    true,
	"type":          true,
	"key":           true,
	"click":         true,
	"detect-page":   true,
	"detect-screen": true,
}

// scriptUsesUI reports whether a vzscript's command section references
// any UI automation commands. Used to determine if a recipe must run
// sequentially in parallel mode.
func scriptUsesUI(comment []byte) bool {
	s := bufio.NewScanner(bytes.NewReader(comment))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cmd := strings.Fields(line)[0]
		// Strip negation/optional prefixes used by rsc.io/script.
		cmd = strings.TrimLeft(cmd, "!?")
		if uiCommands[cmd] {
			return true
		}
	}
	return false
}

// resolvedRecipe holds a loaded recipe with its parsed metadata and data.
type resolvedRecipe struct {
	name     string
	data     []byte
	meta     scriptMeta
	usesUI   bool
	depNames []string // flattened transitive dependency names
}

// runVZScriptsParallel resolves dependencies and runs independent recipes
// concurrently. Recipes that use UI commands are serialized to avoid
// conflicting display interactions. Dependencies are coordinated via
// channels: each recipe waits for its dependencies to signal completion
// before starting.
func runVZScriptsParallel(names []string, cfg vzscriptConfig) error {
	if cfg.autoApprove {
		stop := startAutoApprover(cfg.socketPath, cfg.verbose)
		defer stop()
	}

	// Load and resolve all recipes (including transitive deps).
	all := map[string]*resolvedRecipe{}
	var order []string
	var resolve func(name, requiredBy string) error
	resolve = func(name, requiredBy string) error {
		if all[name] != nil {
			return nil
		}
		data, err := loadVZScriptData(name)
		if err != nil {
			if requiredBy != "" {
				return fmt.Errorf("dependency %q (required by %s) cannot be resolved: %w", name, requiredBy, err)
			}
			return err
		}
		data, err = maybeRenderVZScript(data, name, cfg)
		if err != nil {
			return fmt.Errorf("recipe %s: template: %w", name, err)
		}
		ar := txtar.Parse(data)
		meta := parseScriptMeta(ar.Comment)
		if err := checkVZScriptGuestOS(name, meta, cfg.guestOS); err != nil {
			return err
		}
		r := &resolvedRecipe{
			name:   name,
			data:   data,
			meta:   meta,
			usesUI: scriptUsesUI(ar.Comment),
		}
		all[name] = r
		for _, dep := range meta.requires {
			if err := resolve(dep, name); err != nil {
				return err
			}
			r.depNames = append(r.depNames, dep)
		}
		order = append(order, name)
		return nil
	}
	for _, name := range names {
		if err := resolve(name, ""); err != nil {
			return err
		}
	}

	// Create a done channel per recipe for dependency signaling.
	done := map[string]chan struct{}{}
	for name := range all {
		done[name] = make(chan struct{})
	}

	// UI recipes share a mutex to serialize display interactions.
	var uiMu sync.Mutex

	var wg sync.WaitGroup
	errCh := make(chan error, len(order))

	for _, name := range order {
		r := all[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(done[r.name])

			// Wait for all dependencies to complete.
			for _, dep := range r.depNames {
				<-done[dep]
			}

			if r.usesUI {
				uiMu.Lock()
				defer uiMu.Unlock()
			}

			if len(r.meta.mounts) > 0 {
				if err := applyMountDirectives(r.meta.mounts, cfg.socketPath, cfg.verbose); err != nil {
					errCh <- fmt.Errorf("%s: mount: %w", r.name, err)
					return
				}
			}
			if cfg.verbose {
				fmt.Fprintf(os.Stderr, "--- running recipe: %s ---\n", r.name)
			}
			rcfg := cfgForRecipe(cfg, r.meta)
			rcfg.template = false
			if err := runVZScript(r.data, r.name, rcfg); err != nil {
				errCh <- fmt.Errorf("%s: %w", r.name, err)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}
	if len(errs) == 1 {
		return errs[0]
	}
	if len(errs) > 1 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return fmt.Errorf("%d recipes failed:\n  %s", len(errs), strings.Join(msgs, "\n  "))
	}
	return nil
}

func runVZScript(data []byte, name string, cfg vzscriptConfig) error {
	return runVZScriptContext(context.Background(), data, name, cfg)
}

func runVZScriptContext(ctx context.Context, data []byte, name string, cfg vzscriptConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var err error
	data, err = maybeRenderVZScript(data, name, cfg)
	if err != nil {
		return fmt.Errorf("template: %w", err)
	}
	ar := txtar.Parse(data)
	start := time.Now()

	// Log header to persistent log file.
	if cfg.hostLogFile != nil {
		fmt.Fprintf(cfg.hostLogFile, "\n=== %s: %s started ===\n", start.Format(time.RFC3339), name)
	}

	// Wrap output writers with recipe name prefix for clarity.
	cfg = prefixOutputWriters(cfg, name)
	if cfg.labels == nil {
		cfg.labels = &vzscriptLabelStack{}
	}
	engine := newVZScriptEngine(cfg)

	workdir, err := os.MkdirTemp("", "vzscript-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workdir)

	env := os.Environ()
	env = append(env, cfg.env...)
	state, err := script.NewState(ctx, workdir, env)
	if err != nil {
		return err
	}
	if err := state.ExtractFiles(ar); err != nil {
		return fmt.Errorf("extract files: %w", err)
	}

	var logBuf bytes.Buffer
	var out io.Writer = &logBuf
	if cfg.verbose {
		out = cfg.logWriter
		if out == nil {
			out = os.Stderr
		}
	}
	// Tee script output to host log file.
	if cfg.hostLogFile != nil {
		out = io.MultiWriter(out, cfg.hostLogFile)
	}

	err = engine.Execute(state, name, bufio.NewReader(bytes.NewReader(ar.Comment)), out)
	setVZScriptWindowLabel(cfg, "", nil)
	if !cfg.verbose && logBuf.Len() > 0 {
		os.Stderr.Write(logBuf.Bytes())
	}

	// Log footer with duration and result.
	if cfg.hostLogFile != nil {
		status := "ok"
		if err != nil {
			status = fmt.Sprintf("error: %v", err)
		}
		fmt.Fprintf(cfg.hostLogFile, "=== %s: %s finished (%s) [%s] ===\n",
			time.Now().Format(time.RFC3339), name, time.Since(start).Round(time.Millisecond), status)
	}
	return err
}

// prefixOutputWriters wraps cfg's log and stream writers with a line-prefixing
// writer that prepends "[recipe-name] " to each output line. Sets defaults
// for nil writers so the prefix is always visible.
func prefixOutputWriters(cfg vzscriptConfig, name string) vzscriptConfig {
	prefix := "[" + name + "] "
	if cfg.logWriter == nil {
		cfg.logWriter = os.Stderr
	}
	cfg.logWriter = newPrefixWriter(cfg.logWriter, prefix)
	if cfg.streamOut == nil {
		cfg.streamOut = os.Stdout
	}
	cfg.streamOut = newPrefixWriter(cfg.streamOut, prefix)
	if cfg.streamErr == nil {
		cfg.streamErr = os.Stderr
	}
	cfg.streamErr = newPrefixWriter(cfg.streamErr, prefix)
	return cfg
}

// prefixWriter wraps an io.Writer and prepends a prefix at the start of
// each line. Partial writes (no trailing newline) buffer until the next
// newline arrives.
type prefixWriter struct {
	w      io.Writer
	prefix string
	atBOL  bool // at beginning of line
}

func newPrefixWriter(w io.Writer, prefix string) *prefixWriter {
	return &prefixWriter{w: w, prefix: prefix, atBOL: true}
}

func (pw *prefixWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if pw.atBOL {
			if _, err := io.WriteString(pw.w, pw.prefix); err != nil {
				return written, err
			}
			pw.atBOL = false
		}
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			n, err := pw.w.Write(p)
			written += n
			return written, err
		}
		n, err := pw.w.Write(p[:i+1])
		written += n
		if err != nil {
			return written, err
		}
		pw.atBOL = true
		p = p[i+1:]
	}
	return written, nil
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
	case "socket", "timeout", "vm", "var":
		return true
	}
	return false
}

// openVZScriptLog opens (or creates) the vzscript log file in the VM directory
// derived from the control socket path. The file is opened in append mode.
func openVZScriptLog(socketPath string) (*os.File, error) {
	vmDirectory := filepath.Dir(socketPath)
	logPath := filepath.Join(vmDirectory, "vzscript.log")
	return os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
}

func loadVZScriptData(nameOrPath string) ([]byte, error) {
	if _, err := os.Stat(nameOrPath); err == nil {
		return os.ReadFile(nameOrPath)
	}
	for _, name := range vzscriptBuiltinNames(nameOrPath) {
		if data, err := builtinScripts.ReadFile("vzscripts/" + name); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("recipe not found: %s (not a file and not a built-in)", nameOrPath)
}

func vzscriptBuiltinNames(name string) []string {
	var names []string
	add := func(s string) {
		for _, old := range names {
			if old == s {
				return
			}
		}
		names = append(names, s)
	}
	switch {
	case strings.HasSuffix(name, ".vzscript.tmpl"):
		add(name)
		add(filepath.Base(name))
	case strings.HasSuffix(name, ".vzscript"):
		add(name)
		add(filepath.Base(name))
		add(strings.TrimSuffix(name, ".vzscript") + ".vzscript.tmpl")
		add(strings.TrimSuffix(filepath.Base(name), ".vzscript") + ".vzscript.tmpl")
	default:
		add(name + ".vzscript")
		add(filepath.Base(name) + ".vzscript")
		add(name + ".vzscript.tmpl")
		add(filepath.Base(name) + ".vzscript.tmpl")
	}
	return names
}

// cfgForRecipe returns a copy of cfg with per-recipe overrides applied.
// Currently handles "runs-on: daemon" to route commands through the root agent.
func cfgForRecipe(cfg vzscriptConfig, meta scriptMeta) vzscriptConfig {
	if meta.runsOn == "daemon" {
		cfg.daemon = true
	}
	return cfg
}

// injectDirective describes a file to inject into the VM disk before running.
type injectDirective struct {
	guestPath string // macOS guest path relative to Data volume
	txtarFile string // name of file in txtar archive
	mode      string // octal mode string, e.g. "0644"
	owner     string // e.g. "root:wheel"
}

// mountDirective describes a host directory to mount in the guest via VirtioFS.
type mountDirective struct {
	hostPath string // host directory path (may contain ~/)
	readOnly bool   // true for read-only mount
}

// scriptMeta holds parsed metadata from a vzscript header.
type scriptMeta struct {
	name     string
	desc     string
	guestOS  string            // darwin, linux, or both
	requires []string          // recipe names this script depends on
	runsOn   string            // "daemon" to run as root via daemon agent
	inject   []injectDirective // files to inject into VM disk
	mounts   []mountDirective  // host directories to mount via VirtioFS
}

// parseScriptMeta extracts metadata from script comment lines.
// Recognized headers:
//
//	# name — description   (first non-blank comment line)
//	# guest-os: darwin|linux|both
//	# requires: a, b       (dependency list)
//	# runs-on: daemon      (run as root via daemon agent)
//	# inject: guest-path txtar-file [mode] [owner]
func parseScriptMeta(comment []byte) scriptMeta {
	m := scriptMeta{guestOS: "darwin"}
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

		// Check for "guest-os:" directive.
		if strings.HasPrefix(strings.ToLower(text), "guest-os:") {
			if osName := normalizeVZScriptGuestOS(strings.TrimSpace(text[len("guest-os:"):])); osName != "" {
				m.guestOS = osName
			}
			continue
		}

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

		// Check for "runs-on:" directive.
		if strings.HasPrefix(strings.ToLower(text), "runs-on:") {
			m.runsOn = strings.TrimSpace(text[len("runs-on:"):])
			continue
		}

		// Check for "inject:" directive.
		if strings.HasPrefix(strings.ToLower(text), "inject:") {
			fields := strings.Fields(strings.TrimSpace(text[len("inject:"):]))
			if len(fields) >= 2 {
				d := injectDirective{guestPath: fields[0], txtarFile: fields[1]}
				if len(fields) >= 3 {
					d.mode = fields[2]
				}
				if len(fields) >= 4 {
					d.owner = fields[3]
				}
				m.inject = append(m.inject, d)
			}
			continue
		}

		// Check for "mount:" directive.
		if strings.HasPrefix(strings.ToLower(text), "mount:") {
			fields := strings.Fields(strings.TrimSpace(text[len("mount:"):]))
			if len(fields) >= 1 {
				d := mountDirective{hostPath: fields[0]}
				if len(fields) >= 2 {
					switch strings.ToLower(fields[1]) {
					case "ro":
						d.readOnly = true
					case "rw":
						d.readOnly = false
					}
				}
				m.mounts = append(m.mounts, d)
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

func normalizeVZScriptGuestOS(osName string) string {
	switch strings.ToLower(strings.TrimSpace(osName)) {
	case "darwin", "macos", "mac":
		return "darwin"
	case "linux":
		return "linux"
	case "both", "all", "any":
		return "both"
	default:
		return ""
	}
}

func vzscriptGuestOSFromPlatform(platform string) string {
	switch platform {
	case agentstate.PlatformLinux:
		return "linux"
	default:
		return "darwin"
	}
}

func vzscriptGuestOSForSocket(socketPath string) string {
	return vzscriptGuestOSFromPlatform(ctlGuestPlatform(socketPath))
}

func vzscriptGuestOSMatches(recipeOS, targetOS string) bool {
	recipeOS = normalizeVZScriptGuestOS(recipeOS)
	targetOS = normalizeVZScriptGuestOS(targetOS)
	if recipeOS == "" {
		recipeOS = "darwin"
	}
	return recipeOS == "both" || targetOS == "" || recipeOS == targetOS
}

func checkVZScriptGuestOS(name string, meta scriptMeta, targetOS string) error {
	if vzscriptGuestOSMatches(meta.guestOS, targetOS) {
		return nil
	}
	recipeOS := normalizeVZScriptGuestOS(meta.guestOS)
	if recipeOS == "" {
		recipeOS = "darwin"
	}
	return fmt.Errorf("vzscript: recipe '%s' is for %s guests only; this VM is %s", name, recipeOS, vzscriptGuestOSDisplay(targetOS))
}

func vzscriptGuestOSDisplay(osName string) string {
	switch normalizeVZScriptGuestOS(osName) {
	case "linux":
		return "Linux"
	case "darwin":
		return "Darwin"
	default:
		return "unknown"
	}
}

// expandTilde replaces a leading ~/ (or bare ~) with the current user's
// home directory. Non-tilde paths are returned unchanged.
func expandTilde(path string) string {
	if path == "" {
		return ""
	}
	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// applyMountDirectives registers host directories as shared folders and
// attempts to hot-plug and mount them in the guest. Errors from hot-plug
// and guest mount are treated as warnings (the share is saved for next boot).
func applyMountDirectives(mounts []mountDirective, socketPath string, verbose bool) error {
	vmDirectory := filepath.Dir(socketPath)

	for _, m := range mounts {
		abs, err := filepath.Abs(expandTilde(m.hostPath))
		if err != nil {
			return fmt.Errorf("resolve mount path %q: %w", m.hostPath, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return fmt.Errorf("mount path %q: %w", abs, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("mount path %q is not a directory", abs)
		}

		entry, added, err := addSharedFolderEntry(vmDirectory, abs, "", m.readOnly)
		if err != nil {
			return fmt.Errorf("add shared folder %q: %w", abs, err)
		}
		if added && verbose {
			mode := "rw"
			if entry.ReadOnly {
				mode = "ro"
			}
			fmt.Fprintf(os.Stderr, "mount: added shared folder %s (tag=%s, %s)\n", entry.Path, entry.Tag, mode)
		}
	}

	// Hot-plug into running VM (best-effort).
	client := NewControlClient(socketPath)
	client.SetTimeout(15 * time.Second)
	if _, err := client.SharedFoldersApply(); err != nil {
		if verbose {
			fmt.Fprintf(os.Stderr, "mount: warning: hot-plug failed: %v (will apply on next boot)\n", err)
		}
		return nil
	}

	// Mount in guest (best-effort).
	if _, err := mountSharedFoldersInGuest(vmDirectory, defaultSharedFoldersMountPoint); err != nil {
		if verbose {
			fmt.Fprintf(os.Stderr, "mount: warning: guest mount failed: %v\n", err)
		}
	}
	return nil
}
