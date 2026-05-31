package main

import "testing"

func TestParseFleetLabels(t *testing.T) {
	got, err := parseFleetLabels([]string{"zone=desk", "role=runner"})
	if err != nil {
		t.Fatal(err)
	}
	if got["zone"] != "desk" || got["role"] != "runner" {
		t.Fatalf("labels = %#v", got)
	}
	if _, err := parseFleetLabels([]string{"bad"}); err == nil {
		t.Fatal("parseFleetLabels error = nil, want invalid label")
	}
}
