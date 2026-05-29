package guibench

import (
	"strings"
	"testing"
)

func TestExamineReportsGetterOutput(t *testing.T) {
	// Examine runs setup, pauses (no-op here), then prints the getter result and
	// the score the metric would assign. The fake env's solved-state getter
	// reports "false" after setup-only, so the score would be 0 — the human
	// would then act on the guest and re-run to see it flip.
	var out strings.Builder
	task := feasibleTask()
	task.Instruction = "make the probe report present"
	if err := Examine(&memEnv{}, task, 1, NoPause{}, &out); err != nil {
		t.Fatalf("Examine: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"task:        mem-feasible",
		"make the probe report present",
		"result tier: A",
		`getter result: "false"`,
		"score would be: 0.00",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Examine output missing %q\n--- output ---\n%s", want, got)
		}
	}
}

func TestExamineShowsGoldAnswer(t *testing.T) {
	var out strings.Builder
	task := &Task{
		ID:    "examine-gold",
		Image: "macos-base:v1",
		Schema: []Param{
			{Name: "WORD", Pool: []string{"alpha", "bravo"}},
		},
		Evaluator: Evaluator{
			Func:    StringList{"exact_match"},
			Result:  GetterSpec{Kind: "exec", Args: []string{"probe"}},
			Options: map[string]any{"expected": "{WORD}"},
		},
	}
	if err := Examine(&memEnv{}, task, 2, NoPause{}, &out); err != nil {
		t.Fatalf("Examine: %v", err)
	}
	params := task.Params(2)
	if !strings.Contains(out.String(), "gold answer: "+params["WORD"]) {
		t.Errorf("Examine did not print the materialized gold answer; got:\n%s", out.String())
	}
}

func TestReaderPauserContinuesOnNewline(t *testing.T) {
	var prompt strings.Builder
	p := ReaderPauser{R: strings.NewReader("\n"), Prompt: &prompt, Banner: "go? "}
	if err := p.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if !strings.Contains(prompt.String(), "go?") {
		t.Errorf("banner not printed; got %q", prompt.String())
	}
}

func TestReaderPauserContinuesOnEOF(t *testing.T) {
	// EOF (e.g. piped empty stdin) continues rather than erroring, so an
	// unattended examine does not hang.
	p := ReaderPauser{R: strings.NewReader("")}
	if err := p.Pause(); err != nil {
		t.Fatalf("Pause on EOF: %v", err)
	}
}
