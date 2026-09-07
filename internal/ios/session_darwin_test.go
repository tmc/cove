package ios

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
)

func TestCanceledOpenDoesNotMutateBundle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	session, err := OpenSession(ctx, dir, true, 0, io.Discard)
	if session != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("session=%v err=%v", session, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("created guest state: %v", entries)
	}
}

func TestWaitMainCompletionAndCancellation(t *testing.T) {
	failure := errors.New("native failure")
	for _, tt := range []struct {
		name     string
		complete bool
		result   error
		want     error
	}{
		{"complete", true, nil, nil}, {"failed", true, failure, failure}, {"canceled", false, nil, context.Canceled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			done := make(chan error, 1)
			if tt.complete {
				done <- tt.result
			}
			if got := waitMain(ctx, done); !errors.Is(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func ExampleOpenSession() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := OpenSession(ctx, "phone.covevm", false, 0, io.Discard)
	fmt.Println(err)
	// Output: context canceled
}

func ExampleSession() {
	var session Session
	fmt.Println(session.ECID())
	fmt.Println(session.Start(context.Background(), StartOptions{ForceDFU: true}))
	fmt.Println(session.Wait(context.Background()))
	fmt.Println(session.Stop(context.Background()))
	fmt.Println(session.Close(context.Background()))
	// Output:
	// 0
	// ios session is closed
	// ios session is closed
	// ios session is closed
	// <nil>
}

func TestSerialShutdownDrainsBufferedOutput(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	want := []byte("final boot log\n")
	if _, err := write.Write(want); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	session := Session{
		graph:      &deviceGraph{serialOutput: read, serialOutputWriter: write, files: []*os.File{read, write}},
		serialDone: make(chan error, 1),
	}
	go func() { _, err := io.Copy(&output, read); session.serialDone <- err }()
	session.closeSerial(context.Background())
	if !bytes.Equal(output.Bytes(), want) {
		t.Fatalf("output=%q", output.Bytes())
	}
	if session.graph != nil {
		t.Fatal("graph still owned after serial shutdown")
	}
}

type gatedSerialWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (w gatedSerialWriter) Write(p []byte) (int, error) {
	select {
	case w.entered <- struct{}{}:
	default:
	}
	<-w.release
	return len(p), nil
}

func TestSerialDrainCancellationRetainsOwnership(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	writer := gatedSerialWriter{entered: make(chan struct{}, 1), release: make(chan struct{})}
	session := Session{graph: &deviceGraph{serialOutputWriter: write, files: []*os.File{read, write}}, serialDone: make(chan error, 1)}
	go func() { _, err := io.Copy(writer, read); session.serialDone <- err }()
	if _, err := write.Write([]byte("log")); err != nil {
		close(writer.release)
		t.Fatal(err)
	}
	<-writer.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = session.closeSerial(ctx)
	close(writer.release)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("close = %v", err)
	}
	if session.graph == nil {
		t.Fatal("released graph before serial drain completed")
	}
	if err := session.closeSerial(context.Background()); err != nil {
		t.Fatal(err)
	}
	if session.graph != nil {
		t.Fatal("retry did not release graph")
	}
}
