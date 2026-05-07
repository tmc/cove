package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreAuthRunSavesGrantedResult(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "tcc.json")
	state := &TCCState{}
	services := []tccPreAuthService{
		{ID: "svc_a", Desc: "Service A", Target: "AppA", Script: "noop"},
	}
	fakeRun := func(ctx context.Context, script string) TCCResult { return TCCResultGranted }
	out := &bytes.Buffer{}
	err := preAuthRun(state, statePath, services, false, true, strings.NewReader(""), out, fakeRun)
	if err != nil {
		t.Fatalf("preAuthRun: %v", err)
	}
	loaded, err := LoadTCCState(statePath)
	if err != nil {
		t.Fatalf("LoadTCCState: %v", err)
	}
	entry, ok := loaded.HostEntry("svc_a")
	if !ok || entry.Result != TCCResultGranted {
		t.Errorf("HostEntry(svc_a) = (%v, %v), want granted", entry, ok)
	}
}

func TestPreAuthRunDeniedRecorded(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "tcc.json")
	state := &TCCState{}
	services := []tccPreAuthService{
		{ID: "svc_b", Desc: "Service B", Target: "AppB", Script: "noop"},
	}
	fakeRun := func(ctx context.Context, script string) TCCResult { return TCCResultDenied }
	out := &bytes.Buffer{}
	if err := preAuthRun(state, statePath, services, false, true, strings.NewReader(""), out, fakeRun); err != nil {
		t.Fatalf("preAuthRun: %v", err)
	}
	loaded, _ := LoadTCCState(statePath)
	entry, _ := loaded.HostEntry("svc_b")
	if entry.Result != TCCResultDenied {
		t.Errorf("entry.Result = %q, want denied", entry.Result)
	}
}

func TestPreAuthRunSkipsAlreadyGranted(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "tcc.json")
	state := &TCCState{}
	state.SetHostEntry("svc_c", TCCResultGranted, time.Now())
	services := []tccPreAuthService{
		{ID: "svc_c", Desc: "Service C", Target: "AppC", Script: "noop"},
	}
	called := 0
	fakeRun := func(ctx context.Context, script string) TCCResult {
		called++
		return TCCResultGranted
	}
	out := &bytes.Buffer{}
	if err := preAuthRun(state, statePath, services, false, true, strings.NewReader(""), out, fakeRun); err != nil {
		t.Fatalf("preAuthRun: %v", err)
	}
	if called != 0 {
		t.Errorf("runner called %d times for already-granted service, want 0", called)
	}
	if !strings.Contains(out.String(), "All services already preflighted") {
		t.Errorf("expected already-preflighted message, got: %s", out.String())
	}
}

func TestPreAuthRunForceReRunsGranted(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "tcc.json")
	state := &TCCState{}
	state.SetHostEntry("svc_d", TCCResultGranted, time.Now())
	services := []tccPreAuthService{
		{ID: "svc_d", Desc: "Service D", Target: "AppD", Script: "noop"},
	}
	called := 0
	fakeRun := func(ctx context.Context, script string) TCCResult {
		called++
		return TCCResultGranted
	}
	out := &bytes.Buffer{}
	if err := preAuthRun(state, statePath, services, true, true, strings.NewReader(""), out, fakeRun); err != nil {
		t.Fatalf("preAuthRun: %v", err)
	}
	if called != 1 {
		t.Errorf("runner called %d times under --force, want 1", called)
	}
}

func TestPreAuthRunInteractivePromptDeclined(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "tcc.json")
	state := &TCCState{}
	services := []tccPreAuthService{
		{ID: "svc_e", Desc: "Service E", Target: "AppE", Script: "noop"},
	}
	called := 0
	fakeRun := func(ctx context.Context, script string) TCCResult {
		called++
		return TCCResultGranted
	}
	out := &bytes.Buffer{}
	if err := preAuthRun(state, statePath, services, false, false, strings.NewReader("n\n"), out, fakeRun); err != nil {
		t.Fatalf("preAuthRun: %v", err)
	}
	if called != 0 {
		t.Errorf("runner called %d times after user declined, want 0", called)
	}
	if !strings.Contains(out.String(), "skipped") {
		t.Errorf("expected skipped message, got: %s", out.String())
	}
}

func TestHostTCCServicesCovered(t *testing.T) {
	want := []string{"system_events", "utm"}
	if len(hostTCCServices) != len(want) {
		t.Fatalf("hostTCCServices has %d entries, want %d", len(hostTCCServices), len(want))
	}
	for i, id := range want {
		if hostTCCServices[i].ID != id {
			t.Errorf("hostTCCServices[%d].ID = %q, want %q", i, hostTCCServices[i].ID, id)
		}
		if hostTCCServices[i].Script == "" {
			t.Errorf("hostTCCServices[%d].Script is empty", i)
		}
	}
}

