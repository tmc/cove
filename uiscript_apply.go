// uiscript_apply.go - CLI for running uiscript recipes.
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
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/txtar"
	"rsc.io/script"
)

//go:embed uiscripts/*.uiscript
var builtinUIScripts embed.FS

// uiscriptCommand handles the "uiscript" subcommand.
func uiscriptCommand(args []string) error {
	if len(args) < 1 {
		return uiscriptUsage()
	}
	switch args[0] {
	case "list":
		return uiscriptList()
	case "show":
		return uiscriptShow(args[1:])
	case "run":
		return uiscriptRun(args[1:])
	default:
		return fmt.Errorf("unknown uiscript command: %s", args[0])
	}
}

func uiscriptUsage() error {
	fmt.Fprintf(os.Stderr, `Usage: vz-macos uiscript <command> [args...]

Commands:
  list                    List built-in UI scripts
  show <script>           Print script contents
  run [-v] <script>       Run a script against a running VM (requires -gui)

A UI script is a txtar archive (see golang.org/x/tools/txtar) executed
by rsc.io/script. The comment section contains commands; files in
the archive are extracted to a working directory.

UI automation commands:
  screenshot [file]         Capture VM screen
  ocr                       Run OCR; stdout is all recognized text
  ocr-click <text> [t]      Find text via OCR and click it
  ocr-wait <text> [t]       Wait for text to appear on screen
  ocr-gone <text> [t]       Wait for text to disappear
  type <text>               Type text via clipboard paste
  key <spec>                Send key (return, tab, cmd+v, shift+a, ...)
  click <x> <y>             Click at normalized coords (0-1)
  wait <duration>           Sleep
  detect-page               Detect Setup Assistant page
  detect-screen             Detect screen state

Conditions:
  [screen:<state>]          Current screen state matches
  [page:<name>]             Current Setup Assistant page matches
  [text-visible:<text>]     Text is visible on screen

Standard rsc.io/script commands (echo, cat, env, etc.) and conditions
([darwin], [GOOS:linux], etc.) are also available.

Examples:
  vz-macos uiscript list
  vz-macos uiscript show setup-assistant
  vz-macos uiscript run -v setup-assistant
  vz-macos uiscript run ./custom.uiscript
`)
	return fmt.Errorf("command required")
}

func uiscriptList() error {
	type entry struct {
		name, desc string
	}
	var entries []entry

	files, err := fs.ReadDir(builtinUIScripts, "uiscripts")
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".uiscript") {
			continue
		}
		data, err := builtinUIScripts.ReadFile("uiscripts/" + f.Name())
		if err != nil {
			continue
		}
		ar := txtar.Parse(data)
		name, desc := parseScriptHeader(ar.Comment)
		if name == "" {
			name = strings.TrimSuffix(f.Name(), ".uiscript")
		}
		entries = append(entries, entry{name, desc})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	fmt.Println("Built-in UI scripts:")
	for _, e := range entries {
		if e.desc != "" {
			fmt.Printf("  %-25s %s\n", e.name, e.desc)
		} else {
			fmt.Printf("  %s\n", e.name)
		}
	}
	return nil
}

func uiscriptShow(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("show requires a script name or path")
	}
	data, err := loadUIScriptData(args[0])
	if err != nil {
		return err
	}
	os.Stdout.Write(data)
	return nil
}

func uiscriptRun(args []string) error {
	rf := flag.NewFlagSet("uiscript run", flag.ExitOnError)
	verboseFlag := rf.Bool("v", false, "Verbose output")
	debugDir := rf.String("debug-dir", "", "Directory to save debug screenshots")
	if err := rf.Parse(args); err != nil {
		return err
	}
	if rf.NArg() < 1 {
		return fmt.Errorf("run requires a script name or path")
	}

	data, err := loadUIScriptData(rf.Arg(0))
	if err != nil {
		return err
	}

	sock := GetControlSocketPath()
	cs, err := connectToControlServer(sock)
	if err != nil {
		return fmt.Errorf("connect to control server: %w", err)
	}

	ocr := NewOCRService(*verboseFlag)

	cfg := uiscriptConfig{
		cs:      cs,
		ocr:     ocr,
		verbose: *verboseFlag,
		saveDir: *debugDir,
	}
	return runUIScript(data, rf.Arg(0), cfg)
}

// connectToControlServer creates a ControlServer-like interface from a socket.
// For the CLI path, we need a different approach since we don't have an in-process
// ControlServer. This is a placeholder that returns an error — the recommended
// approach is to use uiscript from within the VM process (in-process automation).
func connectToControlServer(_ string) (*ControlServer, error) {
	return nil, fmt.Errorf("uiscript run requires in-process mode; use 'vz-macos run -gui -uiscript <script>' instead")
}

// runUIScript executes a UI automation script.
func runUIScript(data []byte, name string, cfg uiscriptConfig) error {
	ar := txtar.Parse(data)
	engine := newUIScriptEngine(cfg)

	workdir, err := os.MkdirTemp("", "uiscript-*")
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
		out = io.MultiWriter(os.Stderr, &log)
	}

	started := time.Now()
	err = engine.Execute(state, name, bufio.NewReader(bytes.NewReader(ar.Comment)), out)
	elapsed := time.Since(started)

	if !cfg.verbose && log.Len() > 0 {
		os.Stderr.Write(log.Bytes())
	}

	if err != nil {
		return fmt.Errorf("%s failed after %s: %w", name, elapsed.Round(time.Millisecond), err)
	}
	if cfg.verbose {
		fmt.Fprintf(os.Stderr, "%s completed in %s\n", name, elapsed.Round(time.Millisecond))
	}
	return err
}

// runUIScriptInProcess runs a UI script with an in-process ControlServer.
// Called from the VM runtime when -uiscript is specified.
func runUIScriptInProcess(cs *ControlServer, scriptData []byte, name string, verbose bool, debugDir string) error {
	ocr := NewOCRService(verbose)
	cfg := uiscriptConfig{
		cs:      cs,
		ocr:     ocr,
		verbose: verbose,
		saveDir: debugDir,
	}
	return runUIScript(scriptData, name, cfg)
}

func loadUIScriptData(nameOrPath string) ([]byte, error) {
	if _, err := os.Stat(nameOrPath); err == nil {
		return os.ReadFile(nameOrPath)
	}
	name := nameOrPath
	if !strings.HasSuffix(name, ".uiscript") {
		name += ".uiscript"
	}
	data, err := builtinUIScripts.ReadFile("uiscripts/" + name)
	if err != nil {
		return nil, fmt.Errorf("script not found: %s (not a file and not a built-in)", nameOrPath)
	}
	return data, nil
}
