package guibench

import (
	"strings"
	"testing"
)

func TestGetters(t *testing.T) {
	probe := FakeProbe{
		Files: map[string]string{
			"/Users/tmc/Desktop/Trip/notes.txt": "hello\n",
		},
		Commands: map[string]ExecResult{
			"test -d /Users/tmc/Desktop/Project":   {ExitCode: 0, Stdout: "true\n"},
			"defaults read -g AppleInterfaceStyle": {ExitCode: 0, Stdout: "Dark\n"},
			"defaults read -g MissingKey":          {ExitCode: 1, Stderr: "does not exist"},
			"echo ok":                              {ExitCode: 0, Stdout: "ok\n"},
			"sh -c exit 3":                         {ExitCode: 3, Stderr: "boom\n"},
		},
		OCRText: "Continue\nProject",
	}

	tests := []struct {
		name    string
		spec    GetterSpec
		params  map[string]string
		want    string
		wantErr bool
	}{
		{
			name: "exec stdout trimmed",
			spec: GetterSpec{Kind: "exec", Args: []string{"echo", "ok"}},
			want: "ok",
		},
		{
			name:   "exec exit field",
			spec:   GetterSpec{Kind: "exec", Args: []string{"test", "-d", "{DIR}"}, Field: "exit"},
			params: map[string]string{"DIR": "/Users/tmc/Desktop/Project"},
			want:   "0",
		},
		{
			name: "exec stderr on failure",
			spec: GetterSpec{Kind: "exec", Args: []string{"sh", "-c", "exit 3"}},
			want: "boom",
		},
		{
			name:   "file read with param",
			spec:   GetterSpec{Kind: "file", Path: "/Users/tmc/Desktop/{TITLE}/notes.txt"},
			params: map[string]string{"TITLE": "Trip"},
			want:   "hello\n",
		},
		{
			name:    "file missing",
			spec:    GetterSpec{Kind: "file", Path: "/nope"},
			wantErr: true,
		},
		{
			name: "defaults live value",
			spec: GetterSpec{Kind: "defaults", Domain: "-g", Key: "AppleInterfaceStyle"},
			want: "Dark",
		},
		{
			name:    "defaults missing key errors",
			spec:    GetterSpec{Kind: "defaults", Domain: "-g", Key: "MissingKey"},
			wantErr: true,
		},
		{
			name: "screen ocr",
			spec: GetterSpec{Kind: "screen_ocr"},
			want: "Continue\nProject",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.spec.Get(probe, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Get = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEvaluateInfeasible(t *testing.T) {
	task := &Task{
		ID:         "impossible",
		Infeasible: true,
		Evaluator: Evaluator{
			Func:   StringList{"infeasible"},
			Result: GetterSpec{Kind: "exec", Args: []string{"echo", "unused"}},
		},
	}
	// The agent correctly declined: score 1 from agentAnswer, no probe call.
	score, err := Evaluate(FakeProbe{}, task, nil, "FAIL")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if score != 1 {
		t.Fatalf("infeasible FAIL score = %v, want 1", score)
	}
	// The agent attempted an answer: score 0.
	score, err = Evaluate(FakeProbe{}, task, nil, "I created it")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if score != 0 {
		t.Fatalf("infeasible non-FAIL score = %v, want 0", score)
	}
}

func TestEvaluateExecMetric(t *testing.T) {
	task := &Task{
		ID: "make-folder",
		Evaluator: Evaluator{
			Func:    StringList{"file_exists"},
			Result:  GetterSpec{Kind: "exec", Args: []string{"test", "-d", "{DIR}"}, Field: "exit"},
			Options: map[string]any{"expected": "0"},
		},
	}
	probe := FakeProbe{Commands: map[string]ExecResult{
		"test -d /Users/tmc/Desktop/Project": {ExitCode: 0},
	}}
	// file_exists treats "0" as falsey, but exit 0 means present — so we score
	// via must_include against the expected option instead. Use exact_match.
	task.Evaluator.Func = StringList{"exact_match"}
	score, err := Evaluate(probe, task, map[string]string{"DIR": "/Users/tmc/Desktop/Project"}, "")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if score != 1 {
		t.Fatalf("score = %v, want 1 (exit 0 == expected 0)", score)
	}
}

func TestGetterSpecValidate(t *testing.T) {
	tests := []struct {
		name string
		spec GetterSpec
		ok   bool
	}{
		{"exec ok", GetterSpec{Kind: "exec", Args: []string{"x"}}, true},
		{"exec no args", GetterSpec{Kind: "exec"}, false},
		{"exec bad field", GetterSpec{Kind: "exec", Args: []string{"x"}, Field: "weird"}, false},
		{"file ok", GetterSpec{Kind: "file", Path: "/x"}, true},
		{"file no path", GetterSpec{Kind: "file"}, false},
		{"defaults ok", GetterSpec{Kind: "defaults", Domain: "-g", Key: "k"}, true},
		{"defaults no key", GetterSpec{Kind: "defaults", Domain: "-g"}, false},
		{"ocr ok", GetterSpec{Kind: "screen_ocr"}, true},
		{"unknown", GetterSpec{Kind: "haruspex"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.validate()
			if tt.ok && err != nil {
				t.Fatalf("validate = %v, want nil", err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("validate = nil, want error")
			}
		})
	}
}

// Confirm the production adapter satisfies Probe at compile time.
var _ Probe = ClientProbe{}
var _ Probe = FakeProbe{}

func TestExecStderrTrim(t *testing.T) {
	// stdout present wins over stderr even on nonzero exit.
	probe := FakeProbe{Commands: map[string]ExecResult{
		"cmd": {ExitCode: 2, Stdout: "partial\n", Stderr: "warn\n"},
	}}
	got, err := GetterSpec{Kind: "exec", Args: []string{"cmd"}}.Get(probe, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "partial" {
		t.Fatalf("Get = %q, want %q", got, "partial")
	}
	if strings.Contains(got, "warn") {
		t.Fatalf("stderr leaked into stdout result: %q", got)
	}
}
