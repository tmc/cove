package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	pb "github.com/tmc/cove/proto/agentpb"
)

type directoryExportStream struct {
	messages []*pb.ExecOutput
	err      error
}

func (s *directoryExportStream) Recv() (*pb.ExecOutput, error) {
	if len(s.messages) == 0 {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	msg := s.messages[0]
	s.messages = s.messages[1:]
	return msg, nil
}

type failedDirectoryWriter struct{}

func (failedDirectoryWriter) Write([]byte) (int, error) { return 0, errors.New("host disk full") }

func TestCopyGuestDirectoryStream(t *testing.T) {
	success, failure := int32(0), int32(1)
	tests := []struct {
		name      string
		messages  []*pb.ExecOutput
		streamErr error
		writeErr  bool
		want      string
		wantErr   string
	}{
		{name: "binary", messages: []*pb.ExecOutput{{Data: []byte{0, 255, 1}}, {ExitCode: &success}}, want: string([]byte{0, 255, 1})},
		{name: "stderr separated", messages: []*pb.ExecOutput{{Stream: pb.ExecOutput_STDERR, Data: []byte("warning")}, {Data: []byte("archive")}, {ExitCode: &success}}, want: "archive"},
		{name: "failed tar", messages: []*pb.ExecOutput{{Stream: pb.ExecOutput_STDERR, Data: []byte("permission denied")}, {ExitCode: &failure}}, wantErr: "permission denied"},
		{name: "missing status", messages: []*pb.ExecOutput{{Data: []byte("partial")}}, want: "partial", wantErr: "without exit status"},
		{name: "transport failure", streamErr: errors.New("disconnected"), wantErr: "disconnected"},
		{name: "host full", messages: []*pb.ExecOutput{{Data: []byte("archive")}}, writeErr: true, wantErr: "host disk full"},
		{name: "bounded stderr", messages: []*pb.ExecOutput{{Stream: pb.ExecOutput_STDERR, Data: bytes.Repeat([]byte("x"), 100000)}, {ExitCode: &failure}}, wantErr: "guest tar: exit 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			var writer io.Writer = &out
			if tt.writeErr {
				writer = failedDirectoryWriter{}
			}
			err := copyGuestDirectoryStream(writer, &directoryExportStream{messages: tt.messages, err: tt.streamErr})
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
			if out.String() != tt.want {
				t.Fatalf("output = %q, want %q", out.String(), tt.want)
			}
			if err != nil && len(err.Error()) > 66*1024 {
				t.Fatalf("unbounded diagnostic: %d bytes", len(err.Error()))
			}
		})
	}
}

func TestGuestDirectoryStreamFailurePreservesDestination(t *testing.T) {
	for _, failure := range []error{syscall.ENOSPC, context.Canceled, errors.New("guest tar: source changed")} {
		t.Run(failure.Error(), func(t *testing.T) {
			root := t.TempDir()
			dest := filepath.Join(root, "dest")
			if err := os.Mkdir(dest, 0700); err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(dest, "old")
			if err := os.WriteFile(old, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			tw := tar.NewWriter(&archive)
			if err := tw.WriteHeader(&tar.Header{Name: "source/new", Mode: 0600, Size: 3}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write([]byte("new")); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			err := copyDirectoryToHost(context.Background(), dest, true, func(w io.Writer) error {
				if _, err := w.Write(archive.Bytes()); err != nil {
					return err
				}
				return failure
			})
			if !errors.Is(err, failure) {
				t.Fatalf("error = %v, want %v", err, failure)
			}
			data, err := os.ReadFile(old)
			if err != nil || string(data) != "old" {
				t.Fatalf("destination changed: %q, %v", data, err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 {
				t.Fatalf("staging remains: %v, %v", entries, err)
			}
		})
	}
}

func TestGuestDirectoryExportScript(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "-source with spaces")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "data"), []byte("export"), 0600); err != nil {
		t.Fatal(err)
	}
	archive, err := exec.Command("/bin/sh", "-c", guestDirectoryExportScript, "test", source).Output()
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "destination")
	if err := copyDirectoryToHost(context.Background(), dest, false, func(w io.Writer) error { _, err := w.Write(archive); return err }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dest, "data"))
	if err != nil || string(data) != "export" {
		t.Fatalf("export = %q, %v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected staging: %v, %v", entries, err)
	}
}
