package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unsafe"

	"connectrpc.com/connect"
	pb "github.com/tmc/cove/proto/agentpb"
	"golang.org/x/sys/windows"
)

func TestConPTYProcess(t *testing.T) {
	mode := os.Getenv("COVE_CONPTY_PROCESS")
	if mode == "" {
		return
	}
	switch mode {
	case "large output":
		fmt.Fprint(os.Stdout, strings.Repeat("Z", 192*1024))
		fmt.Fprintln(os.Stdout, "conpty-tail-complete")
	case "exit":
		fmt.Fprintln(os.Stdout, "conpty-output")
		fmt.Fprintln(os.Stderr, "conpty-stderr")
		os.Exit(7)
	case "input", "resize":
		fmt.Fprintln(os.Stdout, "conpty-ready")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			os.Exit(81)
		}
		if mode == "resize" {
			var info windows.ConsoleScreenBufferInfo
			if err := windows.GetConsoleScreenBufferInfo(windows.Handle(os.Stdout.Fd()), &info); err != nil {
				os.Exit(82)
			}
			fmt.Fprintf(os.Stdout, "conpty-size=%dx%d\n", info.Window.Right-info.Window.Left+1, info.Window.Bottom-info.Window.Top+1)
		}
		fmt.Fprintf(os.Stdout, "conpty-input=%s\n", strings.TrimSpace(line))
	case "descendant":
		exe, err := os.Executable()
		if err != nil {
			os.Exit(84)
		}
		child := exec.Command(exe, "-test.run=^TestConPTYProcess$")
		child.Env = append(os.Environ(), "COVE_CONPTY_PROCESS=wait")
		if err := child.Start(); err != nil {
			os.Exit(85)
		}
		fmt.Fprintf(os.Stdout, "conpty-descendant=%d;\n", child.Process.Pid)
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
			os.Exit(86)
		}
	case "wait":
		fmt.Fprintln(os.Stdout, "conpty-ready")
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(83)
	}
	os.Exit(0)
}

func conPTYTestCommand(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestConPTYProcess$")
	cmd.Env = append(os.Environ(), "COVE_CONPTY_PROCESS="+mode)
	return cmd
}

func checkConPTYSupport(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, errConPTYUnsupported) {
		t.Skip(err)
	}
}

func TestRunConPTYStream(t *testing.T) {
	tests := []struct {
		name, mode, input string
		code              int32
		want              []string
	}{
		{"output and exit", "exit", "", 7, []string{"conpty-output", "conpty-stderr"}},
		{"initial input", "input", "hello\r\n", 0, []string{"conpty-input=hello"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var output bytes.Buffer
			code, err := runConPTY(ctx, &pb.ExecRequest{Tty: true, Stdin: []byte(tt.input)}, conPTYTestCommand(t, tt.mode), func(p []byte) error { _, err := output.Write(p); return err }, nil)
			checkConPTYSupport(t, err)
			if err != nil {
				t.Fatal(err)
			}
			if code != tt.code {
				t.Fatalf("exit code = %d, want %d; output %q", code, tt.code, output.String())
			}
			for _, want := range tt.want {
				if !strings.Contains(output.String(), want) {
					t.Errorf("output %q missing %q", output.String(), want)
				}
			}
		})
	}
}

func TestRunConPTYAttachResizeInput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	frames := make(chan *pb.ExecAttachRequest, 2)
	frames <- &pb.ExecAttachRequest{Request: &pb.ExecAttachRequest_Resize{Resize: &pb.ResizeRequest{Rows: 35, Cols: 100}}}
	frames <- &pb.ExecAttachRequest{Request: &pb.ExecAttachRequest_Stdin{Stdin: &pb.StdinChunk{Data: []byte("resized\r\n")}}}
	receive := func() (*pb.ExecAttachRequest, error) {
		select {
		case r := <-frames:
			return r, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	var output bytes.Buffer
	code, err := runConPTY(ctx, &pb.ExecRequest{Tty: true}, conPTYTestCommand(t, "resize"), func(p []byte) error { _, err := output.Write(p); return err }, receive)
	checkConPTYSupport(t, err)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit = %d; output %q", code, output.String())
	}
	for _, want := range []string{"conpty-size=100x35", "conpty-input=resized"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("output %q missing %q", output.String(), want)
		}
	}
}

func TestRunConPTYTermination(t *testing.T) {
	for _, name := range []string{"cancel", "sender failure", "close stdin"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			frames := make(chan *pb.ExecAttachRequest, 1)
			receive := func() (*pb.ExecAttachRequest, error) {
				select {
				case r := <-frames:
					return r, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			sendErr := errors.New("test sender failed")
			var output bytes.Buffer
			ready := false
			send := func(p []byte) error {
				output.Write(p)
				if !ready && strings.Contains(output.String(), "conpty-ready") {
					ready = true
					switch name {
					case "cancel":
						cancel()
					case "sender failure":
						return sendErr
					case "close stdin":
						frames <- &pb.ExecAttachRequest{Request: &pb.ExecAttachRequest_CloseStdin{CloseStdin: &pb.CloseStdinRequest{}}}
					}
				}
				return nil
			}
			_, err := runConPTY(ctx, &pb.ExecRequest{Tty: true}, conPTYTestCommand(t, "wait"), send, receive)
			checkConPTYSupport(t, err)
			if !ready {
				t.Fatalf("child never became ready: %v; output %q", err, output.String())
			}
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatal("terminal did not terminate before deadline")
			}
			switch name {
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want context cancellation", err)
				}
			case "sender failure":
				if !errors.Is(err, sendErr) {
					t.Fatalf("error = %v, want sender error", err)
				}
			case "close stdin":
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestRunConPTYDescendants(t *testing.T) {
	for _, name := range []string{"parent exit", "cancellation"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			frames := make(chan *pb.ExecAttachRequest, 1)
			receive := func() (*pb.ExecAttachRequest, error) {
				select {
				case frame := <-frames:
					return frame, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			var output bytes.Buffer
			var child windows.Handle
			send := func(p []byte) error {
				output.Write(p)
				if child != 0 {
					return nil
				}
				_, suffix, found := strings.Cut(output.String(), "conpty-descendant=")
				if !found {
					return nil
				}
				digits, _, found := strings.Cut(suffix, ";")
				if !found {
					return nil
				}
				pid, err := strconv.ParseUint(digits, 10, 32)
				if err != nil {
					return err
				}
				child, err = windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, uint32(pid))
				if err != nil {
					return err
				}
				if name == "cancellation" {
					cancel()
				} else {
					frames <- &pb.ExecAttachRequest{Request: &pb.ExecAttachRequest_Stdin{Stdin: &pb.StdinChunk{Data: []byte("exit\r\n")}}}
				}
				return nil
			}
			code, err := runConPTY(ctx, &pb.ExecRequest{Tty: true}, conPTYTestCommand(t, "descendant"), send, receive)
			checkConPTYSupport(t, err)
			if child == 0 {
				t.Fatalf("no descendant handle: %v; output %q", err, output.String())
			}
			defer windows.CloseHandle(child)
			defer windows.TerminateProcess(child, 99)
			if name == "cancellation" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error = %v, want cancellation", err)
				}
			} else if err != nil || code != 0 {
				t.Fatalf("exit = %d, error = %v; output %q", code, err, output.String())
			}
			state, err := windows.WaitForSingleObject(child, 2000)
			if err != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatalf("descendant survived terminal exit: wait = %d, error = %v", state, err)
			}
		})
	}
}

func TestConPTYOwnedHandleCleanup(t *testing.T) {
	p, err := startConPTY(conPTYTestCommand(t, "wait"), 24, 80)
	checkConPTYSupport(t, err)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	handles := []windows.Handle{p.process, p.job, windows.Handle(p.input.Fd()), windows.Handle(p.output.Fd())}
	if err := p.terminate(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.wait(); err != nil {
		t.Fatal(err)
	}
	p.close()
	p.close()
	for _, handle := range handles {
		var flags uint32
		ok, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation").Call(uintptr(handle), uintptr(unsafe.Pointer(&flags)))
		if ok != 0 || !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			t.Errorf("handle %d after close: error = %v, want invalid handle", handle, err)
		}
	}
	if err := p.resize(24, 80); !errors.Is(err, os.ErrClosed) {
		t.Errorf("resize closed console: %v", err)
	}
}

func TestConPTYStartupFailureCleanup(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.exe")
	attempt := func() {
		cmd := exec.Command(missing)
		cmd.Err = nil
		p, err := startConPTY(cmd, 24, 80)
		checkConPTYSupport(t, err)
		if p != nil {
			p.close()
			t.Fatal("missing executable started")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing executable error = %v", err)
		}
	}
	handleCount := func() uint32 {
		var count uint32
		proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount")
		ok, _, err := proc.Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&count)))
		if ok == 0 {
			t.Fatal(err)
		}
		return count
	}
	attempt()
	before := handleCount()
	for range 20 {
		attempt()
	}
	after := handleCount()
	// Permit incidental Go runtime handles; a leaked pipe or console per attempt exceeds this allowance.
	if after > before+4 {
		t.Fatalf("startup failure leaked handles: before %d, after %d", before, after)
	}
}

func TestRunConPTYExternalControl(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := newAgentServer()
	const id = "test-terminal"
	var output bytes.Buffer
	controlled := false
	send := func(p []byte) error {
		output.Write(p)
		if controlled || !strings.Contains(output.String(), "conpty-ready") {
			return nil
		}
		controlled = true
		if _, err := s.ResizeExecTTY(ctx, connect.NewRequest(&pb.ResizeExecTTYRequest{ExecId: id, Rows: 35, Cols: 100})); err != nil {
			return err
		}
		_, err := s.SignalExec(ctx, connect.NewRequest(&pb.SignalExecRequest{ExecId: id, Signal: 9}))
		return err
	}
	_, err := s.runConPTY(ctx, &pb.ExecRequest{Tty: true, ExecId: id}, conPTYTestCommand(t, "wait"), send, nil)
	checkConPTYSupport(t, err)
	if err != nil {
		t.Fatal(err)
	}
	if !controlled {
		t.Fatalf("child never ready; output %q", output.String())
	}
	if _, exists := s.lookupExec(id); exists {
		t.Fatal("terminal remained registered after exit")
	}
	_, err = s.SignalExec(ctx, connect.NewRequest(&pb.SignalExecRequest{ExecId: id, Signal: 9}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("signal after exit: %v", err)
	}
}

func TestRunConPTYOutputTail(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var output bytes.Buffer
	code, err := runConPTY(ctx, &pb.ExecRequest{Tty: true}, conPTYTestCommand(t, "large output"), func(p []byte) error {
		_, err := output.Write(p)
		return err
	}, nil)
	checkConPTYSupport(t, err)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := bytes.Count(output.Bytes(), []byte("Z")); got != 192*1024 {
		t.Errorf("output contains %d payload characters, want %d", got, 192*1024)
	}
	if !strings.Contains(output.String(), "conpty-tail-complete") {
		t.Error("output tail missing at terminal return")
	}
}

func TestRunConPTYDuplicateExecID(t *testing.T) {
	s := newAgentServer()
	const id = "duplicate-terminal"
	original := &activeExec{pid: 123, tty: true, ttyFD: -1}
	s.execs[id] = original
	cmd := exec.Command(filepath.Join(t.TempDir(), "missing.exe"))
	_, err := s.runConPTY(context.Background(), &pb.ExecRequest{Tty: true, ExecId: id}, cmd, func([]byte) error { return nil }, nil)
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("duplicate id error = %v, want already exists before process start", err)
	}
	if got, exists := s.lookupExec(id); !exists || got != original {
		t.Fatal("duplicate request changed existing exec registration")
	}
}
