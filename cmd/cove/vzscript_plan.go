package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/txtar"
	"rsc.io/script"
)

type vzscriptPlan struct {
	Version     int                      `json:"version"`
	Recipes     []vzscriptPlannedRecipe  `json:"recipes"`
	Diagnostics []vzscriptPlanDiagnostic `json:"diagnostics,omitempty"`
}

type vzscriptPlannedRecipe struct {
	Source     string                     `json:"source"`
	RequiredBy []string                   `json:"required_by,omitempty"`
	GuestOS    string                     `json:"guest_os"`
	Route      string                     `json:"route"`
	Commands   []string                   `json:"commands,omitempty"`
	Mounts     []vzscriptPlannedMount     `json:"mounts,omitempty"`
	Injections []vzscriptPlannedInjection `json:"injections,omitempty"`
	NeedsUI    bool                       `json:"needs_ui"`
	Unresolved []string                   `json:"unresolved,omitempty"`
}

type vzscriptPlannedMount struct {
	Path     string `json:"path"`
	ReadOnly bool   `json:"read_only"`
}
type vzscriptPlannedInjection struct {
	GuestPath   string `json:"guest_path"`
	ArchiveFile string `json:"archive_file"`
	Mode        string `json:"mode,omitempty"`
	Owner       string `json:"owner,omitempty"`
}
type vzscriptPlanDiagnostic struct {
	Source     string `json:"source"`
	Line       int    `json:"line"`
	Class      string `json:"class"`
	Message    string `json:"message"`
	NextAction string `json:"next_action"`
}

func vzscriptPlanCommand(mode string, args []string, w io.Writer) error {
	fs := flag.NewFlagSet("vzscript "+mode, flag.ContinueOnError)
	fs.SetOutput(w)
	asJSON := fs.Bool("json", false, "emit the static plan as JSON")
	guestOS := fs.String("os", "", "target guest OS: darwin, linux, or windows")
	fs.Usage = func() {
		fmt.Fprintf(w, "Usage: cove vzscript %s [-json] [-os darwin|linux|windows] <recipe...>\nStatic inspection only; templates and guest checks remain unresolved.\n", mode)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("%s requires a recipe name or path", mode)
	}
	if *guestOS != "" && (normalizeVZScriptGuestOS(*guestOS) == "" || normalizeVZScriptGuestOS(*guestOS) == "both") {
		return fmt.Errorf("invalid target guest OS %q", *guestOS)
	}
	plan := planVZScripts(fs.Args(), normalizeVZScriptGuestOS(*guestOS), loadVZScriptData)
	if *asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(plan); err != nil {
			return err
		}
	} else {
		for i, r := range plan.Recipes {
			fmt.Fprintf(w, "%d. %s (%s, %s)\n", i+1, r.Source, r.GuestOS, r.Route)
			if len(r.RequiredBy) > 0 {
				fmt.Fprintf(w, "   required by: %s\n", strings.Join(r.RequiredBy, ", "))
			}
			for _, m := range r.Mounts {
				access := "rw"
				if m.ReadOnly {
					access = "ro"
				}
				fmt.Fprintf(w, "   mount: %s (%s)\n", m.Path, access)
			}
			for _, d := range r.Injections {
				fmt.Fprintf(w, "   inject: %s -> %s\n", d.ArchiveFile, d.GuestPath)
			}
			for _, u := range r.Unresolved {
				fmt.Fprintf(w, "   unresolved: %s\n", u)
			}
			if mode == "explain" {
				fmt.Fprintf(w, "   commands: %s; UI needed: %t\n", strings.Join(r.Commands, ", "), r.NeedsUI)
			}
		}
		for _, d := range plan.Diagnostics {
			fmt.Fprintf(w, "%s:%d: %s: %s; %s\n", d.Source, d.Line, d.Class, d.Message, d.NextAction)
		}
		if mode == "validate" && len(plan.Diagnostics) == 0 {
			fmt.Fprintln(w, "static validation passed; guest readiness and command outcomes remain unchecked")
		}
	}
	if len(plan.Diagnostics) > 0 {
		return fmt.Errorf("vzscript %s: %d static diagnostic(s)", mode, len(plan.Diagnostics))
	}
	return nil
}

func planVZScripts(names []string, guestOS string, load func(string) ([]byte, error)) vzscriptPlan {
	plan := vzscriptPlan{Version: 1, Recipes: []vzscriptPlannedRecipe{}}
	state := map[string]int{}
	indexes := map[string]int{}
	catalog := newVZScriptEngine(vzscriptConfig{})
	var visit func(string, string)
	visit = func(name, parent string) {
		if state[name] == 1 {
			plan.Diagnostics = append(plan.Diagnostics, vzscriptPlanDiagnostic{name, 1, "dependency", "dependency cycle", "remove the cyclic requires directive"})
			return
		}
		if state[name] == 2 {
			if parent != "" {
				i, ok := indexes[name]
				if !ok {
					return
				}
				plan.Recipes[i].RequiredBy = appendUnique(plan.Recipes[i].RequiredBy, parent)
			}
			return
		}
		state[name] = 1
		data, err := load(name)
		if err != nil {
			plan.Diagnostics = append(plan.Diagnostics, vzscriptPlanDiagnostic{name, 1, "source", "recipe cannot be loaded", "check the recipe name or file path"})
			state[name] = 2
			return
		}
		ar := txtar.Parse(data)
		meta := parseScriptMeta(ar.Comment)
		r := vzscriptPlannedRecipe{Source: name, GuestOS: meta.guestOS, Route: "user"}
		if parent != "" {
			r.RequiredBy = []string{parent}
		}
		if meta.runsOn != "" {
			r.Route = meta.runsOn
		}
		templated := strings.HasSuffix(name, ".tmpl") || bytes.Contains(data, []byte("{{"))
		if templated {
			r.Unresolved = append(r.Unresolved, "template rendering and inputs; dependency metadata may change")
			plan.Diagnostics = append(plan.Diagnostics, vzscriptPlanDiagnostic{name, 1, "unresolved-template", "template has not been rendered", "render with explicit inputs, then plan the rendered recipe"})
		}
		plan.Diagnostics = append(plan.Diagnostics, validateVZScriptPlanHeader(name, ar.Comment, ar.Files)...)
		if err := checkVZScriptGuestOS(name, meta, guestOS); err != nil {
			plan.Diagnostics = append(plan.Diagnostics, vzscriptPlanDiagnostic{name, 1, "guest-os", err.Error(), "choose a matching target OS or recipe"})
		}
		for _, m := range meta.mounts {
			r.Mounts = append(r.Mounts, vzscriptPlannedMount{m.hostPath, m.readOnly})
			if !filepath.IsAbs(m.hostPath) {
				r.Unresolved = appendUnique(r.Unresolved, "mount path base and home expansion must match the execution workspace")
			}
		}
		for _, d := range meta.inject {
			r.Injections = append(r.Injections, vzscriptPlannedInjection{d.guestPath, d.txtarFile, d.mode, d.owner})
		}
		r.Unresolved = append(r.Unresolved, "guest readiness, available tools, and command outcomes")
		if !templated {
			commands, diagnostics := inspectVZScriptCommands(name, ar.Comment, catalog)
			r.Commands = commands
			plan.Diagnostics = append(plan.Diagnostics, diagnostics...)
			for _, c := range commands {
				if isVZScriptUICommand(c) {
					r.NeedsUI = true
				}
			}
			if bytes.Contains(ar.Comment, []byte("$")) {
				r.Unresolved = append(r.Unresolved, "script environment expansion; values are not read or disclosed")
			}
		}
		for _, dep := range meta.requires {
			visit(dep, name)
		}
		state[name] = 2
		indexes[name] = len(plan.Recipes)
		plan.Recipes = append(plan.Recipes, r)
	}
	for _, name := range names {
		visit(name, "")
	}
	return plan
}

func appendUnique(s []string, value string) []string {
	for _, v := range s {
		if v == value {
			return s
		}
	}
	return append(s, value)
}

func validateVZScriptPlanHeader(name string, comment []byte, files []txtar.File) []vzscriptPlanDiagnostic {
	var ds []vzscriptPlanDiagnostic
	add := func(line int, class, message, action string) {
		ds = append(ds, vzscriptPlanDiagnostic{name, line, class, message, action})
	}
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.Name] || filepath.IsAbs(f.Name) || f.Name == ".." || strings.HasPrefix(filepath.Clean(f.Name), ".."+string(filepath.Separator)) {
			add(1, "archive", "duplicate or unsafe archive filename", "use unique archive-relative filenames")
		}
		seen[f.Name] = true
	}
	for i, line := range strings.Split(string(comment), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			break
		}
		key, value, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "#")), ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "guest-os":
			if normalizeVZScriptGuestOS(value) == "" {
				add(i+1, "metadata", "invalid guest-os directive", "use darwin, linux, windows, or both")
			}
		case "runs-on":
			if value != "daemon" && value != "terminal" && value != "terminal-gui" {
				add(i+1, "metadata", "invalid runs-on directive", "use daemon, terminal, or terminal-gui; omit for user agent")
			}
		case "requires":
			for _, dep := range strings.Split(value, ",") {
				if strings.TrimSpace(dep) == "" {
					add(i+1, "metadata", "empty dependency", "provide a recipe name for each dependency")
				}
			}
		case "mount":
			fields := strings.Fields(value)
			if len(fields) < 1 || len(fields) > 2 || (len(fields) == 2 && fields[1] != "ro" && fields[1] != "rw") {
				add(i+1, "metadata", "invalid mount directive", "use an unquoted host path and optional ro or rw; current metadata does not support spaces")
			}
		case "inject":
			fields := strings.Fields(value)
			if len(fields) < 2 || len(fields) > 4 {
				add(i+1, "metadata", "invalid inject directive", "use guest-path archive-file [mode] [owner]")
			} else if !seen[fields[1]] {
				add(i+1, "archive", "injection references a missing archive file", "include the referenced file in the txtar archive")
			}
		}
	}
	return ds
}

// The engine owns parsing and quote/variable rules. These adapters preserve
// registration metadata but never call command or condition implementations.
type vzscriptPlanCondition struct{ usage script.CondUsage }

func (c vzscriptPlanCondition) Usage() *script.CondUsage                 { return &c.usage }
func (c vzscriptPlanCondition) Eval(*script.State, string) (bool, error) { return true, nil }

var vzscriptGuardToken = regexp.MustCompile(`^\[[^\s\]]*\]$`)

func inspectVZScriptCommands(name string, comment []byte, catalog *script.Engine) ([]string, []vzscriptPlanDiagnostic) {
	var commands []string
	var ds []vzscriptPlanDiagnostic
	state, _ := script.NewState(context.Background(), "/", []string{})
	defer state.CloseAndWait(io.Discard)
	engine := &script.Engine{Cmds: map[string]script.Cmd{}, Conds: map[string]script.Cond{}}
	for key, condition := range catalog.Conds {
		engine.Conds[key] = vzscriptPlanCondition{*condition.Usage()}
	}
	record := false
	for key, command := range catalog.Cmds {
		key := key
		usage := *command.Usage()
		engine.Cmds[key] = script.Command(usage, func(*script.State, ...string) (script.WaitFunc, error) {
			if record {
				commands = appendUnique(commands, key)
			}
			if usage.Async {
				return func(*script.State) (string, string, error) { return "", "", nil }, nil
			}
			return nil, nil
		})
	}
	for i, line := range strings.Split(string(comment), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "-- ") {
			ds = append(ds, vzscriptPlanDiagnostic{name, i + 1, "archive", "malformed txtar file delimiter", "use -- filename -- on its own line"})
			continue
		}
		record = false
		err := engine.Execute(state, name, bufio.NewReader(strings.NewReader(line+"\n")), io.Discard)
		if err == nil || errors.Is(err, script.ErrUnexpectedSuccess) {
			// Check guarded command bodies even when an inert condition skipped them.
			body := stripVZScriptPlanGuards(line)
			record = true
			err = engine.Execute(state, name, bufio.NewReader(strings.NewReader(body+"\n")), io.Discard)
		}
		if err != nil && !errors.Is(err, script.ErrUnexpectedSuccess) {
			message := "invalid script syntax or condition"
			var ce *script.CommandError
			if errors.As(err, &ce) {
				message = ce.Err.Error()
				if ce.Op != "" {
					message = ce.Op + ": " + message
				}
			}
			ds = append(ds, vzscriptPlanDiagnostic{name, i + 1, "syntax", message, "check command/condition registration and script quoting"})
		}
	}
	sort.Strings(commands)
	return commands, ds
}

func stripVZScriptPlanGuards(line string) string {
	rest := strings.TrimSpace(line)
	var prefixes []string
	for rest != "" {
		end := strings.IndexAny(rest, " \t\r")
		if end < 0 {
			end = len(rest)
		}
		token := rest[:end]
		if vzscriptGuardToken.MatchString(token) {
			rest = strings.TrimSpace(rest[end:])
			continue
		}
		if token == "!" || token == "?" {
			prefixes = append(prefixes, token)
			rest = strings.TrimSpace(rest[end:])
			continue
		}
		break
	}
	if len(prefixes) > 0 {
		return strings.Join(prefixes, " ") + " " + rest
	}
	return rest
}

func isVZScriptUICommand(name string) bool {
	return strings.HasPrefix(name, "ocr") || strings.HasPrefix(name, "detect-") || strings.HasPrefix(name, "label-") || strings.HasPrefix(name, "recovery-") || name == "startup-options" || name == "reboot-to-recovery" || name == "screenshot" || name == "type" || name == "type-keycodes" || name == "key" || name == "click" || name == "answer-visible" || name == "click-menu-item" || name == "wait-menu-text" || name == "wait-prompt-clear" || name == "windows-install"
}
