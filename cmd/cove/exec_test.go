package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseExecArgsDockerShapedFlags(t *testing.T) {
	opts, vm, argv, err := parseExecArgs([]string{
		"-it",
		"-e", "CI=1",
		"--secret-env", "TOKEN=plain",
		"-w", "/work",
		"-u", "ubuntu",
		"dev",
		"--",
		"bash",
		"-lc",
		"echo ok",
	})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if vm != "dev" {
		t.Fatalf("vm = %q, want dev", vm)
	}
	if !opts.interactive || !opts.tty {
		t.Fatalf("interactive/tty = %v/%v, want true/true", opts.interactive, opts.tty)
	}
	if opts.workDir != "/work" || opts.user != "ubuntu" {
		t.Fatalf("workdir/user = %q/%q, want /work/ubuntu", opts.workDir, opts.user)
	}
	if got, want := []string(opts.env), []string{"CI=1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %#v, want %#v", got, want)
	}
	if got, want := []string(opts.secretEnv), []string{"TOKEN=plain"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("secretEnv = %#v, want %#v", got, want)
	}
	if want := []string{"bash", "-lc", "echo ok"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %#v, want %#v", argv, want)
	}
}

func TestParseExecArgsDoesNotRewriteGuestFlags(t *testing.T) {
	opts, vm, argv, err := parseExecArgs([]string{"-it", "dev", "bash", "-it"})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if vm != "dev" {
		t.Fatalf("vm = %q, want dev", vm)
	}
	if !opts.interactive || !opts.tty {
		t.Fatalf("interactive/tty = %v/%v, want true/true", opts.interactive, opts.tty)
	}
	if want := []string{"bash", "-it"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %#v, want %#v", argv, want)
	}
}

func TestExecSessionSendsDockerShapedFields(t *testing.T) {
	dir, err := os.MkdirTemp("", "execsess*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sockPath := filepath.Join(dir, "c.sock")
	srv, err := newFakeShellServer(sockPath)
	if err != nil {
		t.Fatalf("start fake server: %v", err)
	}
	defer srv.Close()

	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()

	exit, err := runShellSession(
		context.Background(),
		sockPath,
		"",
		"vm0",
		[]string{"true"},
		map[string]string{"CI": "1"},
		nil,
		shellSessionOptions{TTY: false, Interactive: false, User: "ubuntu", WorkingDir: "/work"},
		devnull,
		devnull,
		devnull,
	)
	if err != nil {
		t.Fatalf("runShellSession: %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.lastTTY {
		t.Fatal("tty = true, want false")
	}
	if srv.lastUser != "ubuntu" || srv.lastDir != "/work" {
		t.Fatalf("user/workdir = %q/%q, want ubuntu//work", srv.lastUser, srv.lastDir)
	}
	if srv.lastEnv["CI"] != "1" {
		t.Fatalf("CI env = %q, want 1", srv.lastEnv["CI"])
	}
}
