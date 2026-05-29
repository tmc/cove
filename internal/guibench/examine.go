package guibench

import (
	"fmt"
	"io"
)

// Examine runs a task's setup, pauses for a human to act on the live guest,
// then reads and prints the verifier's getter output — OSWorld's
// run_manual_examine shape (design 047 §9 slice 4). It is the tool for
// inspecting GUI-action-to-disk-state lag (§7): a human performs the GUI action
// by hand, presses enter, and sees exactly what the getter reads, so a verifier
// that reads stale state (a cfprefsd or WAL flush bug) is caught before the
// corpus relies on it.
//
// Examine does not score. It reports the raw getter result and the score the
// metric would assign, leaving the human to judge whether the verifier read the
// state the GUI action produced.
func Examine(env SelfCheckEnv, t *Task, seed uint64, pause Pauser, out io.Writer) error {
	params := t.Params(seed)

	sess, err := env.Acquire(t.Image)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer sess.Close()

	if err := runSteps(sess.Probe(), t.Config, params); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	fmt.Fprintf(out, "task:        %s\n", t.ID)
	fmt.Fprintf(out, "instruction: %s\n", Materialize(t.Instruction, params))
	fmt.Fprintf(out, "result tier: %s (%s)\n", t.Evaluator.Result.Tier(), t.Evaluator.Result.Tier().Grant())
	if exp := expectedValue(t, params); exp != "" {
		fmt.Fprintf(out, "gold answer: %s\n", exp)
	}
	fmt.Fprintln(out, "setup done — act on the guest now, then continue to read the verifier state.")

	if err := pause.Pause(); err != nil {
		return fmt.Errorf("examine paused: %w", err)
	}

	result, gerr := t.Evaluator.Result.Get(sess.Probe(), params)
	if gerr != nil {
		fmt.Fprintf(out, "getter error: %v\n", gerr)
		return nil
	}
	fmt.Fprintf(out, "getter result: %q\n", result)

	expected := expectedValue(t, params)
	if t.Evaluator.Expected != nil {
		v, err := t.Evaluator.Expected.Get(sess.Probe(), params)
		if err != nil {
			fmt.Fprintf(out, "expected getter error: %v\n", err)
		} else {
			expected = v
		}
	}
	score, err := ScoreMetrics(t.Evaluator, result, expected, params)
	if err != nil {
		fmt.Fprintf(out, "scoring error: %v\n", err)
		return nil
	}
	fmt.Fprintf(out, "score would be: %.2f\n", score)
	return nil
}

// expectedValue returns the literal gold value an evaluator carries in its
// "expected" option (materialized against params), or "" when the gold value
// comes from an expected getter instead.
func expectedValue(t *Task, params map[string]string) string {
	if lit, ok := t.Evaluator.Options["expected"].(string); ok {
		return Materialize(lit, params)
	}
	return ""
}

// Pauser blocks until a human signals to continue. The CLI uses [ReaderPauser]
// over stdin; a test uses a no-op pauser so Examine runs unattended.
type Pauser interface {
	Pause() error
}

// ReaderPauser blocks until it reads a line from R (typically os.Stdin),
// prompting on Prompt first.
type ReaderPauser struct {
	R      io.Reader
	Prompt io.Writer
	Banner string
}

// Pause prints the banner, then reads one line (waiting for the human to press
// enter). EOF is treated as a continue, not an error.
func (p ReaderPauser) Pause() error {
	if p.Prompt != nil {
		banner := p.Banner
		if banner == "" {
			banner = "press enter to read the verifier state... "
		}
		fmt.Fprint(p.Prompt, banner)
	}
	var b [1]byte
	for {
		n, err := p.R.Read(b[:])
		if n > 0 && b[0] == '\n' {
			return nil
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// NoPause is a [Pauser] that returns immediately, for unattended runs and tests.
type NoPause struct{}

// Pause returns nil without blocking.
func (NoPause) Pause() error { return nil }
