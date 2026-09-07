//go:build darwin || linux

package firmware

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBoundedOutput(t *testing.T) {
	output := boundedOutput{limit: 5}
	for _, chunk := range []string{"abc", "defg", "hijklmnop", "q"} {
		if n, err := output.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write=%d %v", n, err)
		}
	}
	if string(output.data) != "mnopq" || !output.overflow {
		t.Fatalf("output=%q overflow=%v", output.data, output.overflow)
	}
}

func TestGitCancellationStopsDescendant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "marker with spaces")
	done := make(chan error, 1)
	go func() {
		_, err := git(ctx, "", "-c", `alias.wait=!f() { printf ready > "$1.started"; sleep 1; printf late > "$1.finished"; }; f`, "wait", path)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path + ".started"); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("git exited before readiness: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("git child did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("git did not stop")
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(path + ".finished"); !os.IsNotExist(err) {
		t.Fatalf("descendant survived cancellation: %v", err)
	}
}
