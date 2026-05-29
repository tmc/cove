package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"os"
	"reflect"
	"testing"
)

func TestCloneValueAnySlice(t *testing.T) {
	in := []any{"a", float64(2), []any{"nested"}, map[string]any{"k": "v"}}
	got := cloneValue(in)
	out, ok := got.([]any)
	if !ok {
		t.Fatalf("cloneValue([]any) type = %T, want []any", got)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("cloneValue([]any) = %#v, want %#v", out, in)
	}
	// Mutate clone; original must be untouched.
	out[0] = "z"
	if in[0] != "a" {
		t.Fatalf("clone shares backing array with input: in[0] = %v", in[0])
	}
	nestedClone := out[2].([]any)
	nestedClone[0] = "z"
	origNested := in[2].([]any)
	if origNested[0] != "nested" {
		t.Fatalf("nested []any shared with input: orig[0] = %v", origNested[0])
	}
	mapClone := out[3].(map[string]any)
	mapClone["k"] = "z"
	origMap := in[3].(map[string]any)
	if origMap["k"] != "v" {
		t.Fatalf("nested map shared with input: orig[k] = %v", origMap["k"])
	}
}

func TestCloneValueScalars(t *testing.T) {
	cases := []any{nil, "s", 42, 3.14, true}
	for _, c := range cases {
		if got := cloneValue(c); !reflect.DeepEqual(got, c) {
			t.Errorf("cloneValue(%v) = %v, want %v", c, got, c)
		}
	}
}

func TestCloneMapEmpty(t *testing.T) {
	if got := cloneMap(nil); got != nil {
		t.Errorf("cloneMap(nil) = %v, want nil", got)
	}
	if got := cloneMap(map[string]any{}); got != nil {
		t.Errorf("cloneMap(empty) = %v, want nil", got)
	}
}

func TestRunDumpDocsCommandJSON(t *testing.T) {
	out := captureStdoutForDumpDocs(t, func() {
		if err := runDumpDocsCommand([]string{"-type", "api"}); err != nil {
			t.Fatalf("runDumpDocsCommand: %v", err)
		}
	})

	var bundle dumpDocsBundle
	if err := json.Unmarshal(out, &bundle); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	if bundle.API == nil || len(bundle.API.Endpoints) == 0 {
		t.Fatalf("expected API endpoints in bundle, got %#v", bundle)
	}
	if bundle.CLI != nil || bundle.MCP != nil {
		t.Fatalf("expected only API populated, got CLI=%v MCP=%v", bundle.CLI != nil, bundle.MCP != nil)
	}
}

func TestRunDumpDocsCommandPretty(t *testing.T) {
	out := captureStdoutForDumpDocs(t, func() {
		if err := runDumpDocsCommand([]string{"-type", "cli", "-pretty"}); err != nil {
			t.Fatalf("runDumpDocsCommand: %v", err)
		}
	})
	if !bytes.Contains(out, []byte("\n  ")) {
		t.Fatalf("pretty output missing two-space indent: %s", out)
	}
}

func TestRunDumpDocsCommandBadType(t *testing.T) {
	_ = captureStdoutForDumpDocs(t, func() {
		err := runDumpDocsCommand([]string{"-type", "wat"})
		if err == nil {
			t.Fatal("runDumpDocsCommand(-type wat) = nil, want error")
		}
	})
}

func TestRunDumpDocsCommandHelp(t *testing.T) {
	_ = captureStdoutForDumpDocs(t, func() {
		err := runDumpDocsCommand([]string{"-h"})
		if err != flag.ErrHelp {
			t.Fatalf("runDumpDocsCommand(-h) = %v, want flag.ErrHelp", err)
		}
	})
}

func captureStdoutForDumpDocs(t *testing.T, fn func()) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- data
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}
