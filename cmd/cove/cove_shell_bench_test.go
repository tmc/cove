package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkCoveShellRoundtrip measures the host-side cost of a single
// `cove shell <vm> -- <cmd>` invocation against a fully in-process fake
// agent. It exercises the same runShellSession code path the CLI uses,
// minus the unix-socket dial round-trip cost added by the kernel only
// (no real VM, no vsock, no PTY).
//
// Scenarios:
//   - cold:  one fresh fakeShellServer per b.N iteration. Captures the
//     dominant cost of socket dial + JSON attach handshake + frame pump
//     teardown that an interactive `cove shell` first-command pays.
//   - warm:  one fakeShellServer reused across b.N iterations. Captures
//     the steady-state per-command cost (each iteration is still a fresh
//     attach — ExecAttach has no multiplexing today, see design 023).
//
// Each scenario runs against three stdout payload sizes — 1 B, 1 KiB,
// 1 MiB — to surface throughput-vs-overhead behavior.
//
// The fake agent in shell_test.go sleeps 20 ms after writing the done
// frame so the client can drain. Subtract ~20 ms from any wall-clock
// number reported here to get the protocol-only cost.
func BenchmarkCoveShellRoundtrip(b *testing.B) {
	scenarios := []struct {
		name string
		size int
	}{
		{"stdout1B", 1},
		{"stdout1KiB", 1 << 10},
		{"stdout1MiB", 1 << 20},
	}

	for _, sc := range scenarios {
		payload := make([]byte, sc.size)
		for i := range payload {
			payload[i] = 'x'
		}

		b.Run("cold/"+sc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(sc.size))
			// Use a short host tmpdir so sun_path stays under 104 bytes.
			dir, err := os.MkdirTemp("", "csb")
			if err != nil {
				b.Fatalf("mkdtemp: %v", err)
			}
			defer os.RemoveAll(dir)
			for i := 0; i < b.N; i++ {
				sockPath := filepath.Join(dir, "c.sock")
				srv, err := newFakeShellServer(sockPath)
				if err != nil {
					b.Fatalf("start fake server: %v", err)
				}
				srv.echoData = payload
				runOne(b, sockPath)
				srv.Close()
				os.Remove(sockPath)
			}
		})

		b.Run("warm/"+sc.name, func(b *testing.B) {
			dir, err := os.MkdirTemp("", "wsb")
			if err != nil {
				b.Fatalf("mkdtemp: %v", err)
			}
			defer os.RemoveAll(dir)
			sockPath := filepath.Join(dir, "c.sock")
			srv, err := newFakeShellServer(sockPath)
			if err != nil {
				b.Fatalf("start fake server: %v", err)
			}
			defer srv.Close()
			srv.echoData = payload

			// Warm-up attach (don't measure).
			runOne(b, sockPath)

			b.ResetTimer()
			b.ReportAllocs()
			b.SetBytes(int64(sc.size))
			for i := 0; i < b.N; i++ {
				runOne(b, sockPath)
			}
		})
	}
}

// runOne performs a single runShellSession against sockPath, discarding
// stdout/stderr via os.DevNull. It fails the benchmark on any error so
// regressions in the fake handshake don't silently inflate ns/op.
func runOne(b *testing.B, sockPath string) {
	b.Helper()
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		b.Fatalf("open devnull: %v", err)
	}
	defer devnull.Close()

	// Sink stdout/stderr to /dev/null files so the bench measures
	// transport, not buffer growth.
	out, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatalf("open devnull out: %v", err)
	}
	defer out.Close()
	errOut, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatalf("open devnull err: %v", err)
	}
	defer errOut.Close()

	exit, err := runShellSession(
		context.Background(),
		sockPath,
		"", // no auth token — fake server doesn't enforce
		"benchvm",
		[]string{"echo", "bench"},
		nil,
		nil,
		shellSessionOptions{TTY: true, Interactive: true},
		devnull,
		out,
		errOut,
	)
	if err != nil {
		b.Fatalf("runShellSession: %v", err)
	}
	if exit != 0 {
		b.Fatalf("exit = %d, want 0", exit)
	}
}
