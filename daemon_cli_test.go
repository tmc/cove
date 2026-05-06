package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstallDaemonPlist(t *testing.T) {
	home := t.TempDir()
	paths := daemonPaths{
		SocketPath: filepath.Join(home, ".vz", "cove.sock"),
		PIDPath:    filepath.Join(home, ".vz", "cove.pid"),
		PlistPath:  filepath.Join(home, "Library", "LaunchAgents", "com.cove.daemon.plist"),
		LogPath:    filepath.Join(home, ".vz", "coved.log"),
		CovedPath:  filepath.Join(home, "bin", "coved"),
	}
	if err := installDaemonPlist(paths); err != nil {
		t.Fatalf("installDaemonPlist: %v", err)
	}
	data, err := os.ReadFile(paths.PlistPath)
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	out := string(data)
	for _, want := range []string{paths.CovedPath, paths.SocketPath, paths.PIDPath, paths.LogPath, "com.cove.daemon"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plist missing %q:\n%s", want, out)
		}
	}
}

func TestPrintDaemonMetrics(t *testing.T) {
	var out strings.Builder
	printDaemonMetrics(&out, "# HELP x y\ncoved_uptime_seconds 12\ncoved_vms_managed 3\n")
	got := out.String()
	for _, want := range []string{"coved_uptime_seconds: 12", "coved_vms_managed: 3"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestFetchDaemonMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			t.Fatalf("path = %q, want /metrics", r.URL.Path)
		}
		w.Write([]byte("coved_uptime_seconds 1\n")) //nolint
	}))
	defer srv.Close()
	got, err := fetchDaemonMetrics(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("fetchDaemonMetrics: %v", err)
	}
	if !strings.Contains(got, "coved_uptime_seconds 1") {
		t.Fatalf("body = %q", got)
	}
}

func TestDaemonStartCommandLoadsRenderedPlist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldRun := daemonRunCommand
	oldExe := daemonExecutable
	defer func() {
		daemonRunCommand = oldRun
		daemonExecutable = oldExe
	}()
	daemonExecutable = func() (string, error) { return filepath.Join(home, "bin", "cove"), nil }
	var gotName string
	var gotArgs []string
	daemonRunCommand = func(name string, args ...string) ([]byte, error) {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil, nil
	}
	if err := daemonStartCommand([]string{"-coved", filepath.Join(home, "bin", "coved")}); err != nil {
		t.Fatalf("daemonStartCommand: %v", err)
	}
	wantPlist := filepath.Join(home, "Library", "LaunchAgents", "com.cove.daemon.plist")
	if gotName != "launchctl" || !reflect.DeepEqual(gotArgs, []string{"load", wantPlist}) {
		t.Fatalf("command = %s %v", gotName, gotArgs)
	}
	if _, err := os.Stat(wantPlist); err != nil {
		t.Fatalf("plist stat: %v", err)
	}
}

func TestDaemonStopCommandUnloadsPlist(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldRun := daemonRunCommand
	defer func() { daemonRunCommand = oldRun }()
	var got []string
	daemonRunCommand = func(name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return nil, nil
	}
	if err := daemonStopCommand(nil); err != nil {
		t.Fatalf("daemonStopCommand: %v", err)
	}
	want := []string{"launchctl", "unload", filepath.Join(home, "Library", "LaunchAgents", "com.cove.daemon.plist")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %v, want %v", got, want)
	}
}

func TestQueryDaemonStatus(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "cove.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 16)
		n, _ := conn.Read(buf)
		if string(buf[:n]) == "STATUS\n" {
			_ = json.NewEncoder(conn).Encode(daemonStatus{
				Version:                "test",
				UptimeS:                7,
				VMsManaged:             3,
				ImageGCLastRunTS:       "2026-05-05T12:00:00Z",
				ImageGCRunsTotal:       2,
				ImageGCBytesFreedTotal: 99,
				LifecycleEnforced:      4,
				LifecycleLastRunTS:     "2026-05-05T00:00:00Z",
			})
		}
	}()
	got, err := queryDaemonStatus(socket)
	if err != nil {
		t.Fatalf("queryDaemonStatus: %v", err)
	}
	<-done
	if got != (daemonStatus{Version: "test", UptimeS: 7, VMsManaged: 3, ImageGCLastRunTS: "2026-05-05T12:00:00Z", ImageGCRunsTotal: 2, ImageGCBytesFreedTotal: 99, LifecycleEnforced: 4, LifecycleLastRunTS: "2026-05-05T00:00:00Z"}) {
		t.Fatalf("status = %+v", got)
	}
}
