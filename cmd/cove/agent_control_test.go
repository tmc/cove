package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	agentstate "github.com/tmc/cove/internal/agent"
	pb "github.com/tmc/cove/proto/agentpb"
	agentpbconnect "github.com/tmc/cove/proto/agentpbconnect"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestAgentMountVolumesResponseSuccess(t *testing.T) {
	resp := agentMountVolumesResponse([]map[string]interface{}{
		{"tag": "src", "mountPoint": "/Volumes/src", "mounted": true},
	})
	if !resp.Success {
		t.Fatalf("resp.Success = false, want true")
	}
	if resp.Error != "" {
		t.Fatalf("resp.Error = %q, want empty", resp.Error)
	}
}

func TestAgentMountVolumesResponseFailure(t *testing.T) {
	resp := agentMountVolumesResponse([]map[string]interface{}{
		{"tag": "src", "mountPoint": "/Volumes/src", "error": "mount failed"},
	})
	if resp.Success {
		t.Fatalf("resp.Success = true, want false")
	}
}

func TestAgentSSHDArgsByGuestOS(t *testing.T) {
	tests := []struct {
		name      string
		action    string
		linuxMode bool
		want      []string
	}{
		{
			name:   "macos status",
			action: "status",
			want:   []string{"systemsetup", "-getremotelogin"},
		},
		{
			name:      "linux status",
			action:    "status",
			linuxMode: true,
			want:      []string{"systemctl", "show", "-p", "ActiveState", "--value", "ssh"},
		},
		{
			name:      "linux on",
			action:    "on",
			linuxMode: true,
			want:      []string{"systemctl", "enable", "--now", "ssh.service", "ssh.socket"},
		},
		{
			name:      "linux off",
			action:    "off",
			linuxMode: true,
			want:      []string{"systemctl", "disable", "--now", "ssh.service", "ssh.socket"},
		},
		{
			name:      "linux start",
			action:    "start",
			linuxMode: true,
			want:      []string{"systemctl", "start", "ssh.service", "ssh.socket"},
		},
		{
			name:      "linux stop",
			action:    "stop",
			linuxMode: true,
			want:      []string{"systemctl", "stop", "ssh.service", "ssh.socket"},
		},
		{
			name:      "linux enable",
			action:    "enable",
			linuxMode: true,
			want:      []string{"systemctl", "enable", "--now", "ssh.service", "ssh.socket"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := agentSSHDArgs(tt.action, tt.linuxMode)
			if err != nil {
				t.Fatalf("agentSSHDArgs() error = %v", err)
			}
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Fatalf("agentSSHDArgs() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentSSHDLinuxOffAndStopMentionSocket(t *testing.T) {
	for _, action := range []string{"off", "stop"} {
		t.Run(action, func(t *testing.T) {
			got, err := agentSSHDArgs(action, true)
			if err != nil {
				t.Fatalf("agentSSHDArgs(%q) error = %v", action, err)
			}
			joined := strings.Join(got, " ")
			for _, want := range []string{"ssh.service", "ssh.socket"} {
				if !strings.Contains(joined, want) {
					t.Fatalf("agentSSHDArgs(%q) = %q, missing %q", action, got, want)
				}
			}
		})
	}
}

func TestAgentSSHDArgsRejectsUnknown(t *testing.T) {
	if _, err := agentSSHDArgs("bogus", true); err == nil {
		t.Fatal("agentSSHDArgs() error = nil, want error")
	}
}

func TestGuestDir(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/etc/hosts", "/etc"},
		{"/var/log/system.log", "/var/log"},
		{"/file", "/"},
		{"file", "."},
		{"", "."},
	}
	for _, tt := range tests {
		if got := guestDir(tt.in); got != tt.want {
			t.Errorf("guestDir(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResponseTextValidUTF8(t *testing.T) {
	if got := responseText([]byte("ok")); got != "ok" {
		t.Fatalf("responseText(valid) = %q, want ok", got)
	}
	got := responseText([]byte{0xff, 'o', 'k'})
	if !utf8.ValidString(got) {
		t.Fatalf("responseText returned invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, "ok") {
		t.Fatalf("responseText(%v) = %q, want replacement plus payload", []byte{0xff, 'o', 'k'}, got)
	}
}

func TestIsUserPathOrHome(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"", false},
		{"/Users/me/Desktop/a.txt", true},
		{"/Users/me/Downloads", true},
		{"/Users/me/.bashrc", true},
		{"/Users/Shared/foo", false},
		{"/Users/Shared", false},
		{"~/Desktop/a.txt", true},
		{"~/Documents", true},
		{"/Volumes/share/foo", true},
		{"/Volumes/Macintosh HD/etc/hosts", false},
		{"/var/log/system.log", false},
		{"/tmp/test.txt", false},
		{"/home/ubuntu/file.txt", true},
		{"/home/ubuntu", true},
		{"/etc/hosts", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := isUserPathOrHome(tt.path); got != tt.want {
				t.Errorf("isUserPathOrHome(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestTargetUserForPath(t *testing.T) {
	s := &ControlServer{
		consoleUserOverride: func() (string, int, error) { return "console", 501, nil },
	}
	tests := []struct {
		path string
		want string
	}{
		{"/Users/alice/Desktop/file.txt", "alice"},
		{"/Users/bob/Downloads", "bob"},
		{"/Users/carol/.zshrc", "carol"},
		{"/Users/Shared/notes.txt", ""},
		{"/Users/Shared", ""},
		{"/Users/dave", "dave"},
		{"~/Desktop/a.txt", "console"},
		{"~", "console"},
		{"/home/ubuntu/test.txt", "ubuntu"},
		{"/tmp/scratch.txt", ""},
		{"/var/log/install.log", ""},
		{"/etc/hosts", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := s.targetUserForPath(tt.path); got != tt.want {
				t.Errorf("targetUserForPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestHandleAgentCopyInputValidation(t *testing.T) {
	s := &ControlServer{}
	tests := []struct {
		name    string
		cmd     *controlpb.AgentCopyCommand
		wantErr string
	}{
		{
			name:    "empty command",
			cmd:     &controlpb.AgentCopyCommand{},
			wantErr: "host_path and guest_path required",
		},
		{
			name:    "missing guest path",
			cmd:     &controlpb.AgentCopyCommand{HostPath: "/tmp/foo"},
			wantErr: "host_path and guest_path required",
		},
		{
			name:    "missing host path",
			cmd:     &controlpb.AgentCopyCommand{GuestPath: "/tmp/foo"},
			wantErr: "host_path and guest_path required",
		},
		{
			name: "nonexistent host path to guest",
			cmd: &controlpb.AgentCopyCommand{
				HostPath:  "/nonexistent-host-file-12345",
				GuestPath: "/tmp/file.txt",
				ToGuest:   true,
			},
			wantErr: "stat /nonexistent-host-file-12345",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := s.handleAgentCopy(tt.cmd)
			if resp.Success {
				t.Fatalf("handleAgentCopy() success = true, want failure with %q", tt.wantErr)
			}
			if !strings.Contains(resp.Error, tt.wantErr) {
				t.Fatalf("handleAgentCopy() error = %q, want error containing %q", resp.Error, tt.wantErr)
			}
		})
	}
}

func TestAgentCopyRouting(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		linuxGuest bool
		want       agentstate.Route
	}{
		{name: "desktop routes to user", path: "/Users/alice/Desktop/test.txt", want: agentstate.RouteUser},
		{name: "downloads routes to user", path: "/Users/alice/Downloads", want: agentstate.RouteUser},
		{name: "documents routes to user", path: "/Users/alice/Documents/doc.pdf", want: agentstate.RouteUser},
		{name: "tilde desktop routes to user", path: "~/Desktop/test.txt", want: agentstate.RouteUser},
		{name: "virtiofs volume routes to user", path: "/Volumes/My Shared Files/test.txt", want: agentstate.RouteUser},
		{name: "tmp routes to daemon", path: "/tmp/scratch.txt", want: agentstate.RouteDaemon},
		{name: "system var routes to daemon", path: "/var/log/install.log", want: agentstate.RouteDaemon},
		{name: "system volume routes to daemon", path: "/Volumes/Macintosh HD/etc/hosts", want: agentstate.RouteDaemon},
		{name: "linux guest forces daemon", path: "/Users/alice/Desktop/test.txt", linuxGuest: true, want: agentstate.RouteDaemon},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentstate.RouteFor("cp", tt.path, tt.linuxGuest); got != tt.want {
				t.Errorf("RouteFor(cp, %q, linux=%v) = %v, want %v", tt.path, tt.linuxGuest, got, tt.want)
			}
		})
	}
}

func TestExpandGuestHome(t *testing.T) {
	s := &ControlServer{
		consoleUserOverride: func() (string, int, error) {
			return "alice", 501, nil
		},
	}

	// macOS tests (linuxMode = false)
	oldLinux := linuxMode
	linuxMode = false
	defer func() { linuxMode = oldLinux }()

	tests := []struct {
		in   string
		want string
	}{
		{"~", "/Users/alice"},
		{"~/Desktop/file.txt", "/Users/alice/Desktop/file.txt"},
		{"/tmp/test.txt", "/tmp/test.txt"},
		{"/Users/bob/data.txt", "/Users/bob/data.txt"},
	}
	for _, tt := range tests {
		if got := s.expandGuestHome(tt.in); got != tt.want {
			t.Errorf("expandGuestHome(%q) [macOS] = %q, want %q", tt.in, got, tt.want)
		}
	}

	// Linux tests (linuxMode = true)
	linuxMode = true
	sLinux := &ControlServer{
		consoleUserOverride: func() (string, int, error) {
			return "ubuntu", 1000, nil
		},
	}
	linuxTests := []struct {
		in   string
		want string
	}{
		{"~", "/home/ubuntu"},
		{"~/file.txt", "/home/ubuntu/file.txt"},
		{"/tmp/test.txt", "/tmp/test.txt"},
		{"/home/dev/test.txt", "/home/dev/test.txt"},
	}
	for _, tt := range linuxTests {
		if got := sLinux.expandGuestHome(tt.in); got != tt.want {
			t.Errorf("expandGuestHome(%q) [Linux] = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHandleAgentCopyDirFromGuestOverwrite(t *testing.T) {
	s := &ControlServer{}
	existingDir := t.TempDir()

	// Destination exists and overwrite is false -> error
	ctx := context.Background()
	resp := s.handleAgentCopyDirFromGuest(ctx, nil, nil, "/guest/dir", existingDir, false)
	if resp == nil || resp.Success {
		t.Fatalf("handleAgentCopyDirFromGuest with overwrite=false succeeded, want error")
	}
	wantMsg := fmt.Sprintf(`destination %q already exists (use -f to overwrite)`, existingDir)
	if resp.Error != wantMsg {
		t.Fatalf("handleAgentCopyDirFromGuest error = %q, want %q", resp.Error, wantMsg)
	}
}

func TestHandleAgentCopyTrailingSlash(t *testing.T) {
	s := &ControlServer{}
	cmd := &controlpb.AgentCopyCommand{
		HostPath:  "/nonexistent/file.txt",
		GuestPath: "/tmp/",
		ToGuest:   true,
	}
	resp := s.handleAgentCopy(cmd)
	if resp.Success {
		t.Fatal("handleAgentCopy succeeded for nonexistent host file")
	}
	if cmd.GuestPath != "/tmp/file.txt" {
		t.Fatalf("cmd.GuestPath = %q, want /tmp/file.txt", cmd.GuestPath)
	}

	cmd2 := &controlpb.AgentCopyCommand{
		HostPath:  "/tmp/out/",
		GuestPath: "/remote/dir/file.txt",
		ToGuest:   false,
	}
	_ = s.handleAgentCopy(cmd2)
	wantHost := filepath.Join("/tmp/out", "file.txt")
	if cmd2.HostPath != wantHost {
		t.Fatalf("cmd2.HostPath = %q, want %q", cmd2.HostPath, wantHost)
	}
}

type mockTarCopyOutHandler struct {
	agentpbconnect.UnimplementedAgentHandler
	tarData []byte
}

func (m *mockTarCopyOutHandler) Exec(context.Context, *connect.Request[pb.ExecRequest]) (*connect.Response[pb.ExecResponse], error) {
	return connect.NewResponse(&pb.ExecResponse{ExitCode: 0}), nil
}

func (m *mockTarCopyOutHandler) CopyOut(_ context.Context, _ *connect.Request[pb.CopyOutRequest], stream *connect.ServerStream[pb.CopyOutChunk]) error {
	if err := stream.Send(&pb.CopyOutChunk{
		Content: &pb.CopyOutChunk_Init{Init: &pb.CopyOutInit{Mode: 0644, TotalSize: uint64(len(m.tarData))}},
	}); err != nil {
		return err
	}
	return stream.Send(&pb.CopyOutChunk{
		Content: &pb.CopyOutChunk_Data{Data: m.tarData},
	})
}

func TestHandleAgentCopyDirFromGuestExtract(t *testing.T) {
	srcDir := t.TempDir()
	subDir := filepath.Join(srcDir, "myfolder")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "hello.txt"), []byte("hello from guest\n"), 0644); err != nil {
		t.Fatal(err)
	}
	tarCmd := exec.Command("tar", "cf", "-", "-C", srcDir, "myfolder")
	tarData, err := tarCmd.Output()
	if err != nil {
		t.Fatalf("tar error: %v", err)
	}

	handler := &mockTarCopyOutHandler{tarData: tarData}
	mux := http.NewServeMux()
	p, h := agentpbconnect.NewAgentHandler(handler)
	mux.Handle(p, h)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h2c.NewHandler(mux, &http2.Server{})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close() })

	client, err := agentstate.NewAgentClientWithDial(func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", ln.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	destDir := filepath.Join(t.TempDir(), "extracted")
	s := &ControlServer{}
	resp := s.handleAgentCopyDirFromGuest(context.Background(), client, nil, "/guest/myfolder", destDir, true)
	if resp == nil || !resp.Success {
		t.Fatalf("handleAgentCopyDirFromGuest failed: %v", resp)
	}

	data, err := os.ReadFile(filepath.Join(destDir, "hello.txt"))
	if err != nil {
		t.Fatalf("read extracted file: %v", err)
	}
	if string(data) != "hello from guest\n" {
		t.Fatalf("extracted data = %q, want 'hello from guest\\n'", data)
	}
}

func TestHandleAgentCopyUnresolvedHome(t *testing.T) {
	s := &ControlServer{consoleUserOverride: func() (string, int, error) { return "", 0, nil }}
	for _, guestPath := range []string{"~", "~/Downloads/file"} {
		resp := s.handleAgentCopy(&controlpb.AgentCopyCommand{HostPath: "file", GuestPath: guestPath, ToGuest: true})
		if resp.Success || !strings.Contains(resp.Error, "cannot resolve guest home") {
			t.Fatalf("copy to %q = %v", guestPath, resp)
		}
	}
}
