package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/cove/internal/guibench"
	"github.com/tmc/cove/internal/vmconfig"
)

// runBenchGUIView renders an existing run bundle as a self-contained local HTML
// timeline (design 047 §16, the local interactive trace viewer). It is a pure
// transform over an on-disk bundle (~/.vz/runs/<id>/ events.jsonl, screenshots/,
// manifest.json): it parses the timeline, writes index.html next to the bundle
// referencing screenshots by relative path, and prints the file:// URL. There is
// no cloud dependency, closing trycua's cb-trace-view / app.hud.ai lead. The
// trace parsing and rendering are unit-tested in internal/guibench without a VM.
func runBenchGUIView(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui view", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	stdout := fs.Bool("stdout", false, "write the HTML to stdout instead of index.html in the bundle")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("bench gui view: want exactly one <run-dir> or <run-id-prefix>")
	}

	dir, err := resolveRunDir(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("bench gui view: %w", err)
	}
	tr, err := guibench.LoadTrace(dir)
	if err != nil {
		return fmt.Errorf("bench gui view: %w", err)
	}
	if *stdout {
		return tr.RenderHTML(env.Stdout)
	}
	path, err := tr.WriteHTML(dir)
	if err != nil {
		return fmt.Errorf("bench gui view: %w", err)
	}
	fmt.Fprintf(env.Stdout, "trace: %s\n", fileURL(path))
	return nil
}

// resolveRunDir resolves a view argument to a bundle directory. An existing
// directory path is used as-is; otherwise the argument is treated as a run-id
// prefix and matched against ~/.vz/runs. A prefix matching no run, or more than
// one, is an error so the viewer never silently renders the wrong run.
func resolveRunDir(arg string) (string, error) {
	if info, err := os.Stat(arg); err == nil && info.IsDir() {
		return arg, nil
	}
	root := vmconfig.RunsDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read runs dir %s: %w", root, err)
	}
	var matches []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), arg) {
			matches = append(matches, e.Name())
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no run bundle matching %q in %s", arg, root)
	case 1:
		return filepath.Join(root, matches[0]), nil
	default:
		return "", fmt.Errorf("run id prefix %q is ambiguous: %s", arg, strings.Join(matches, ", "))
	}
}

// fileURL renders an absolute filesystem path as a file:// URL, so a terminal
// can make it clickable.
func fileURL(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	u := url.URL{Scheme: "file", Path: abs}
	return u.String()
}
