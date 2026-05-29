package guibench

import (
	"fmt"
	"strings"
)

// Provenance records that a task was adapted from a foreign computer-use
// benchmark (OSWorld, WebArena, AndroidWorld, WindowsAgentArena, ...). It is
// the machine-readable parse of a [Task.Source] string that follows the
// adapter convention (docs/benchmarks/guibench/adapters.md): a leading
// "adapted:" tag names the upstream benchmark, the upstream task id, and the
// adaptation mode, so a citable result can name exactly which foreign task each
// adapted task descends from.
//
// The Source convention is:
//
//	adapted:<benchmark>:<upstream-id> mode=<mode>; <free-text rationale>
//
// for example:
//
//	adapted:osworld:030eeff7-b492-4218-b312-701ec99ee0cc mode=port; Chrome→Safari Do-Not-Track via defaults
//	adapted:osworld:01b269ae-2111-4a07-81fd-3fcd711993b0 mode=intent; LibreOffice Calc fill-down → Numbers/exec CSV
//
// A cove-original task (not adapted) does not carry the "adapted:" tag and has
// no Provenance.
type Provenance struct {
	// Benchmark is the upstream benchmark name, lowercased (osworld, webarena,
	// androidworld, winarena, cua-bench).
	Benchmark string
	// UpstreamID is the foreign task's own identifier (an OSWorld UUID, a
	// WebArena task_id, ...), copied verbatim so the lineage is auditable.
	UpstreamID string
	// Mode is how the foreign task was carried over (see [AdaptMode]).
	Mode AdaptMode
	// Note is the free-text rationale following the tag.
	Note string
}

// AdaptMode is how a foreign task was carried into the native-macOS corpus.
//
// The distinction is the load-bearing one for the adapter trap (design 047
// §16, NotebookLM guidance): a verbatim port of a foreign-app task tests that
// foreign app running on macOS, which is a distraction. Only two modes are
// legitimate, and the corpus enforces it ([Provenance.validate]):
//
//   - ModePort: the upstream task is genuinely cross-platform (a web task, a
//     filesystem/Terminal task, a generic OS-setting task), so the same goal is
//     a real native-macOS workflow. OSWorld Chrome → Safari, OSWorld os/file →
//     Finder/Terminal, WebArena web → Safari.
//   - ModeIntent: the upstream task targets a foreign app with no honest
//     cross-platform reading, so only its INTENT is re-expressed against an
//     Apple-native app. OSWorld LibreOffice Calc → Numbers, Thunderbird → Mail.
//     The foreign app is never installed on macOS.
type AdaptMode string

// The two legitimate adaptation modes.
const (
	ModePort   AdaptMode = "port"   // genuinely cross-platform; same goal is native
	ModeIntent AdaptMode = "intent" // foreign-app intent re-expressed against an Apple-native app
)

// adaptedPrefix tags a Source string as an adapted task.
const adaptedPrefix = "adapted:"

// IsAdapted reports whether the task's Source follows the adapter convention
// (its Source begins with the "adapted:" tag). A cove-original task returns
// false.
func (t *Task) IsAdapted() bool {
	return strings.HasPrefix(strings.TrimSpace(t.Source), adaptedPrefix)
}

// Provenance parses the task's Source as an adapter citation. It returns an
// error when the Source does not follow the convention, so a corpus test can
// assert every adapted task cites a real upstream task and a valid mode. A
// cove-original task (no "adapted:" tag) is not an error caller-side; callers
// gate on [Task.IsAdapted] first.
func (t *Task) Provenance() (Provenance, error) {
	return parseProvenance(t.Source)
}

// parseProvenance parses one Source string of the form
// "adapted:<benchmark>:<upstream-id> mode=<mode>; <note>".
func parseProvenance(source string) (Provenance, error) {
	s := strings.TrimSpace(source)
	if !strings.HasPrefix(s, adaptedPrefix) {
		return Provenance{}, fmt.Errorf("source %q is not an adapted citation (missing %q tag)", source, adaptedPrefix)
	}
	s = strings.TrimPrefix(s, adaptedPrefix)

	// Split the leading "benchmark:upstream-id mode=..." head from the rest. The
	// head runs up to the first whitespace; everything after is "mode=...; note".
	head, rest, _ := strings.Cut(s, " ")
	benchmark, upstreamID, ok := strings.Cut(head, ":")
	if !ok || benchmark == "" || upstreamID == "" {
		return Provenance{}, fmt.Errorf("adapted source %q: want adapted:<benchmark>:<upstream-id>", source)
	}

	p := Provenance{
		Benchmark:  strings.ToLower(benchmark),
		UpstreamID: upstreamID,
	}

	// The remainder is "mode=<mode>; <note>". The mode token is required.
	rest = strings.TrimSpace(rest)
	modeField, note, _ := strings.Cut(rest, ";")
	modeField = strings.TrimSpace(modeField)
	if !strings.HasPrefix(modeField, "mode=") {
		return Provenance{}, fmt.Errorf("adapted source %q: missing mode=<port|intent>", source)
	}
	p.Mode = AdaptMode(strings.TrimSpace(strings.TrimPrefix(modeField, "mode=")))
	p.Note = strings.TrimSpace(note)

	if err := p.validate(); err != nil {
		return Provenance{}, fmt.Errorf("adapted source %q: %w", source, err)
	}
	return p, nil
}

// validate checks a parsed provenance for the adapter invariants.
func (p Provenance) validate() error {
	switch p.Mode {
	case ModePort, ModeIntent:
	default:
		return fmt.Errorf("unknown adaptation mode %q (want %q or %q)", p.Mode, ModePort, ModeIntent)
	}
	return nil
}
