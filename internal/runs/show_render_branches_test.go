package runs

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type failingWriter struct {
	afterN int
	n      int
}

func (f *failingWriter) Write(p []byte) (int, error) {
	f.n++
	if f.n > f.afterN {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

func TestRenderShowRejectsNilWriter(t *testing.T) {
	if err := RenderShow(nil, Show{}); err == nil {
		t.Fatal("expected error for nil writer")
	}
}

func TestRenderShowOmitsExitCodeAndFailureWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	show := Show{
		RunID:     "r1",
		Dir:       "/tmp/r1",
		Result:    Result{Status: "ok", WallclockMS: 42},
		Artifacts: []string{"a.log"},
	}
	if err := RenderShow(&buf, show); err != nil {
		t.Fatalf("RenderShow: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Result: ok wallclock=42ms") {
		t.Fatalf("missing result line: %s", out)
	}
	if strings.Contains(out, "exit_code=") {
		t.Fatalf("unexpected exit_code: %s", out)
	}
	if strings.Contains(out, "Failure:") {
		t.Fatalf("unexpected Failure line: %s", out)
	}
	if !strings.Contains(out, "  a.log\n") {
		t.Fatalf("missing artifact line: %s", out)
	}
}

func TestRenderShowPropagatesWriteErrors(t *testing.T) {
	show := Show{
		RunID:     "r1",
		Dir:       "/tmp/r1",
		Result:    Result{Status: "ok"},
		Artifacts: []string{"a.log"},
	}
	if err := RenderShow(&failingWriter{afterN: 0}, show); err == nil {
		t.Fatal("expected write error")
	}
}
