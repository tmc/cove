package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	pb "github.com/tmc/vz-macos/proto/agentpb"
)

type fakeLinuxProxyRT struct {
	files     map[string][]byte
	readErr   map[string]error
	writes    []writeRec
	execCalls [][]string
}

type writeRec struct {
	path string
	data []byte
	mode uint32
}

func (f *fakeLinuxProxyRT) ReadFile(_ context.Context, path string) ([]byte, error) {
	if err, ok := f.readErr[path]; ok {
		return nil, err
	}
	data, ok := f.files[path]
	if !ok {
		return nil, errors.New("no such file")
	}
	return data, nil
}

func (f *fakeLinuxProxyRT) WriteFile(_ context.Context, path string, data []byte, mode uint32) error {
	f.writes = append(f.writes, writeRec{path, data, mode})
	if f.files == nil {
		f.files = map[string][]byte{}
	}
	f.files[path] = data
	return nil
}

func (f *fakeLinuxProxyRT) Exec(_ context.Context, args []string, _ map[string]string, _ string) (*pb.ExecResponse, error) {
	f.execCalls = append(f.execCalls, args)
	return &pb.ExecResponse{}, nil
}

func TestLinuxProxyFilesContent(t *testing.T) {
	files := linuxProxyFiles(proxySpec{Scheme: "http", Host: "10.0.0.1", Port: 3128})
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	for path, content := range files {
		if !strings.Contains(content, "10.0.0.1:3128") {
			t.Errorf("file %s missing host: %s", path, content)
		}
		if strings.HasSuffix(path, ".sh") {
			if !strings.Contains(content, "export HTTP_PROXY=") {
				t.Errorf("profile.d missing export: %s", content)
			}
		}
	}
}

func TestCaptureLinuxProxyState(t *testing.T) {
	rt := &fakeLinuxProxyRT{files: map[string][]byte{
		"/etc/environment.d/" + proxyEnvFileName: []byte("HTTP_PROXY=old\n"),
	}}
	state, err := captureLinuxProxyState(context.Background(), rt)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(state.Files) != 2 {
		t.Fatalf("got %d files, want 2", len(state.Files))
	}
	var present, missing int
	for _, f := range state.Files {
		if f.Present {
			present++
			if string(f.Data) != "HTTP_PROXY=old\n" {
				t.Errorf("data = %q", f.Data)
			}
		} else {
			missing++
		}
	}
	if present != 1 || missing != 1 {
		t.Errorf("present=%d missing=%d, want 1/1", present, missing)
	}
}

func TestCaptureLinuxProxyStatePropagatesError(t *testing.T) {
	rt := &fakeLinuxProxyRT{readErr: map[string]error{
		"/etc/environment.d/" + proxyEnvFileName: errors.New("permission denied"),
	}}
	if _, err := captureLinuxProxyState(context.Background(), rt); err == nil {
		t.Fatal("err = nil, want permission denied")
	}
}

func TestApplyLinuxProxyWritesOnlyKnownPaths(t *testing.T) {
	rt := &fakeLinuxProxyRT{}
	state := &linuxProxyState{Files: []proxyFileBackup{
		{Path: "/etc/environment.d/" + proxyEnvFileName},
		{Path: "/etc/profile.d/" + proxyProfileFileName},
		{Path: "/some/unrelated/path"},
	}}
	err := applyLinuxProxy(context.Background(), rt, state, proxySpec{Scheme: "http", Host: "p", Port: 8080})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rt.writes) != 2 {
		t.Fatalf("got %d writes, want 2", len(rt.writes))
	}
	for _, w := range rt.writes {
		if !strings.Contains(string(w.data), "p:8080") {
			t.Errorf("write %s missing host: %s", w.path, w.data)
		}
		if w.mode != 0644 {
			t.Errorf("mode = %o, want 0644", w.mode)
		}
	}
}

func TestRestoreLinuxProxyRemovesAbsentFiles(t *testing.T) {
	rt := &fakeLinuxProxyRT{}
	state := &linuxProxyState{Files: []proxyFileBackup{
		{Path: "/etc/profile.d/" + proxyProfileFileName, Present: false},
		{Path: "/etc/environment.d/" + proxyEnvFileName, Present: true, Mode: 0644, Data: []byte("orig\n")},
	}}
	if err := restoreLinuxProxy(context.Background(), rt, state); err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(rt.execCalls) != 1 || rt.execCalls[0][0] != "/bin/rm" {
		t.Fatalf("expected one rm exec, got %v", rt.execCalls)
	}
	if len(rt.writes) != 1 || string(rt.writes[0].data) != "orig\n" {
		t.Fatalf("expected one restore write, got %+v", rt.writes)
	}
}


