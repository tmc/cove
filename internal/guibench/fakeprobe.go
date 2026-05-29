package guibench

import "fmt"

// FakeProbe is an in-memory [Probe] for testing getters without a VM. Files
// maps a guest path to its contents; Commands maps a space-joined argv to a
// canned exec result; OCRText is returned by OCRAllText. A missing file or
// command yields a not-found error, matching the real transport's shape.
type FakeProbe struct {
	Files    map[string]string
	Commands map[string]ExecResult
	OCRText  string
	OCRErr   error
}

// ExecResult is a canned exec outcome for [FakeProbe].
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

// Exec returns the canned result for the joined argv.
func (f FakeProbe) Exec(args []string, _ map[string]string, _ string) (int, string, string, error) {
	key := joinArgs(args)
	r, ok := f.Commands[key]
	if !ok {
		return 0, "", "", fmt.Errorf("fake probe: no command %q", key)
	}
	return r.ExitCode, r.Stdout, r.Stderr, r.Err
}

// ReadFile returns the canned file contents.
func (f FakeProbe) ReadFile(path string) ([]byte, error) {
	c, ok := f.Files[path]
	if !ok {
		return nil, fmt.Errorf("fake probe: no file %q", path)
	}
	return []byte(c), nil
}

// OCRAllText returns the canned OCR text.
func (f FakeProbe) OCRAllText() (string, error) {
	return f.OCRText, f.OCRErr
}

// joinArgs joins argv with single spaces for use as a Commands map key.
func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
