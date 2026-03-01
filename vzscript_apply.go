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
  run [-v] [-timeout d] <recipe>  Run a recipe against a running VM

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
  ocr-click <text> [timeout]  Find text via OCR and click its center
  ocr-wait <text> [timeout]   Wait until text appears on screen
  ocr-gone <text> [timeout]   Wait until text disappears from screen
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

Examples:
  vz-macos vzscript list
  vz-macos vzscript show homebrew
  vz-macos vzscript run developer-tools
  vz-macos vzscript run ./custom.vzscript
`)
	return fmt.Errorf("command required")
}

func vzscriptList() error {
	type entry struct {
		name, desc string
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
		name, desc := parseScriptHeader(ar.Comment)
		if name == "" {
			name = strings.TrimSuffix(f.Name(), ".vzscript")
		}
		entries = append(entries, entry{name, desc})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	fmt.Println("Built-in recipes:")
	for _, e := range entries {
		if e.desc != "" {
			fmt.Printf("  %-20s %s\n", e.name, e.desc)
		} else {
			fmt.Printf("  %s\n", e.name)
		}
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
	rf := flag.NewFlagSet("vzscript run", flag.ExitOnError)
	socketPath := rf.String("socket", "", "Control socket path")
	timeout := rf.Duration("timeout", 10*time.Minute, "Timeout for guest-exec commands")
	verbose := rf.Bool("v", false, "Verbose output")
	terminal := rf.Bool("terminal", false, "Run guest-shell commands in Terminal.app (visible in VM GUI)")
	if err := rf.Parse(args); err != nil {
		return err
	}
	if rf.NArg() < 1 {
		return fmt.Errorf("run requires a recipe name or path")
	}

	data, err := loadVZScriptData(rf.Arg(0))
	if err != nil {
		return err
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
	}
	return runVZScript(data, rf.Arg(0), cfg)
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
		out = os.Stderr
	}
	err = engine.Execute(state, name, bufio.NewReader(bytes.NewReader(ar.Comment)), out)
	if !cfg.verbose && log.Len() > 0 {
		os.Stderr.Write(log.Bytes())
	}
	return err
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

// parseScriptHeader extracts name and description from the first comment line.
// Expected: "# name — description" or "# name - description".
func parseScriptHeader(comment []byte) (name, desc string) {
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
		for _, sep := range []string{" — ", " - "} {
			if i := strings.Index(text, sep); i >= 0 {
				return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+len(sep):])
			}
		}
		return text, ""
	}
	return "", ""
}
