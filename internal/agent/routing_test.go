package agent

import "testing"

func TestAgentRouteFor(t *testing.T) {
	tests := []struct {
		name       string
		op         string
		path       string
		linuxGuest bool
		want       Route
	}{
		{name: "ping is daemon", op: "ping", want: RouteDaemon},
		{name: "info is daemon", op: "info", want: RouteDaemon},
		{name: "exec is daemon (caller asked for root)", op: "exec", want: RouteDaemon},
		{name: "exec /Volumes routes to user", op: "exec", path: "/Volumes/share", want: RouteUser},
		{name: "exec /var stays daemon", op: "exec", path: "/var/log/install.log", want: RouteDaemon},
		{name: "exec empty path stays daemon", op: "exec", path: "", want: RouteDaemon},
		{name: "user-exec is user", op: "user-exec", want: RouteUser},
		{name: "user-exec-stream is user", op: "user-exec-stream", want: RouteUser},
		{name: "shutdown is daemon", op: "shutdown", want: RouteDaemon},
		{name: "reboot is daemon", op: "reboot", want: RouteDaemon},
		{name: "sshd is daemon", op: "sshd", want: RouteDaemon},
		{name: "mount-volumes stays daemon (root)", op: "mount-volumes", want: RouteDaemon},
		{name: "read /var stays daemon", op: "read", path: "/var/log/install.log", want: RouteDaemon},
		{name: "read ~/Documents routes to user (TCC)", op: "read", path: "/Users/me/Documents/x.txt", want: RouteUser},
		{name: "read ~/Library routes to user (TCC)", op: "read", path: "/Users/me/Library/Preferences/x.plist", want: RouteUser},
		{name: "read ~/.ssh stays daemon (root has access)", op: "read", path: "/Users/me/.ssh/known_hosts", want: RouteDaemon},
		{name: "write /Library system stays daemon", op: "write", path: "/Library/LaunchDaemons/x.plist", want: RouteDaemon},
		{name: "write /Volumes virtiofs routes to user", op: "write", path: "/Volumes/share/x.txt", want: RouteUser},
		{name: "write Macintosh HD stays daemon", op: "write", path: "/Volumes/Macintosh HD/etc/hosts", want: RouteDaemon},
		{name: "cp ~/Desktop routes to user", op: "cp", path: "/Users/me/Desktop/screenshot.png", want: RouteUser},
		{name: "cp empty path stays daemon", op: "cp", path: "", want: RouteDaemon},
		{name: "linux guest forces daemon for user-exec", op: "user-exec", linuxGuest: true, want: RouteDaemon},
		{name: "linux guest forces daemon for /Users TCC", op: "read", path: "/Users/me/Documents/x", linuxGuest: true, want: RouteDaemon},
		{name: "unknown op defaults to daemon", op: "weird-thing", want: RouteDaemon},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RouteFor(tt.op, tt.path, tt.linuxGuest)
			if got != tt.want {
				t.Errorf("RouteFor(%q, %q, linux=%v) = %s, want %s",
					tt.op, tt.path, tt.linuxGuest, got, tt.want)
			}
		})
	}
}

func TestExtractPathOperand(t *testing.T) {
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{name: "ls volumes", argv: []string{"ls", "-1", "/Volumes/share"}, want: "/Volumes/share"},
		{name: "ls weird after separator", argv: []string{"ls", "--", "-weird"}, want: ""},
		{name: "stat documents", argv: []string{"stat", "/Users/me/Documents/a.txt"}, want: "/Users/me/Documents/a.txt"},
		{name: "cat var log", argv: []string{"cat", "/var/log/system.log"}, want: "/var/log/system.log"},
		{name: "cp prefers destination", argv: []string{"cp", "/tmp/a", "/Users/me/Desktop/a"}, want: "/Users/me/Desktop/a"},
		{name: "mv prefers destination", argv: []string{"mv", "/Users/me/Documents/a", "/tmp/a"}, want: "/tmp/a"},
		{name: "ln prefers destination", argv: []string{"ln", "-s", "/tmp/a", "/Volumes/share/a"}, want: "/Volumes/share/a"},
		{name: "test unary file", argv: []string{"test", "-e", "/Volumes/share/a"}, want: "/Volumes/share/a"},
		{name: "mkdir skips flag", argv: []string{"mkdir", "-p", "/Volumes/share/new"}, want: "/Volumes/share/new"},
		{name: "rm skips combined flag", argv: []string{"rm", "-rf", "/Users/me/Downloads/a"}, want: "/Users/me/Downloads/a"},
		{name: "tar directory wins", argv: []string{"tar", "-C", "/Volumes/share", "-cf", "/tmp/out.tar", "."}, want: "/Volumes/share"},
		{name: "tar compact directory wins", argv: []string{"tar", "-C/Volumes/share", "-cf", "/tmp/out.tar", "."}, want: "/Volumes/share"},
		{name: "tar file is conservative", argv: []string{"tar", "-cf", "/tmp/out.tar", "/Users/me/Documents"}, want: "/tmp/out.tar"},
		{name: "chmod skips flag", argv: []string{"chmod", "-R", "700", "/Users/me/Library/App"}, want: "/Users/me/Library/App"},
		{name: "unknown command", argv: []string{"launchctl", "print", "system"}, want: ""},
		{name: "empty argv", argv: nil, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractPathOperand(tt.argv); got != tt.want {
				t.Fatalf("ExtractPathOperand(%q) = %q, want %q", tt.argv, got, tt.want)
			}
		})
	}
}

func TestRouteForExec(t *testing.T) {
	tests := []struct {
		name       string
		argv       []string
		linuxGuest bool
		want       Route
	}{
		{name: "macos volumes routes user", argv: []string{"ls", "/Volumes/share"}, want: RouteUser},
		{name: "macos etc routes daemon", argv: []string{"ls", "/etc/os-release"}, want: RouteDaemon},
		{name: "macos var routes daemon", argv: []string{"cat", "/var/log/install.log"}, want: RouteDaemon},
		{name: "macos desktop destination routes user", argv: []string{"cp", "/tmp/a", "/Users/me/Desktop/a"}, want: RouteUser},
		{name: "unknown no path routes daemon", argv: []string{"launchctl", "print", "system"}, want: RouteDaemon},
		{name: "empty routes daemon", argv: nil, want: RouteDaemon},
		{name: "linux forces daemon", argv: []string{"ls", "/Volumes/share"}, linuxGuest: true, want: RouteDaemon},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RouteForExec(tt.argv, tt.linuxGuest); got != tt.want {
				t.Fatalf("RouteForExec(%q, linux=%v) = %s, want %s", tt.argv, tt.linuxGuest, got, tt.want)
			}
		})
	}
}

func TestIsUserPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"", false},
		{"/Users/me", false},
		{"/Users/me/Documents/file.txt", true},
		{"/Users/me/Library/Preferences/x.plist", true},
		{"/Users/me/Desktop/screenshot.png", true},
		{"/Users/me/Downloads", true},
		{"/Users/me/.ssh/known_hosts", false},
		{"/Users/me/Public/share.txt", false},
		{"/", false},
		{"/etc/os-release", false},
		{"/var/log/system.log", false},
		{"/Library/Preferences/x.plist", false},
		{"/private/var/db/.AppleSetupDone", false},
		{"/Applications/Safari.app", false},
		{"/opt/homebrew/bin/brew", false},
		{"/System/Library/Foo", false},
		{"/usr/local/bin/cove", false},
		{"/sbin/mount", false},
		{"/bin/ls", false},
		{"/dev/disk0", false},
		{"/tmp/x", false},
		{"/Volumes/share", true},
		{"/Volumes/share/file.txt", true},
		{"/Volumes/My Shared Files", true},
		{"/Volumes/My Shared Files/ml-explore/file.txt", true},
		{"/Volumes/Macintosh HD/etc/hosts", false},
		{"/Volumes/Macintosh HD - Data/Users/me", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsUserPath(tt.path); got != tt.want {
				t.Errorf("IsUserPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestRouteForSharedFoldersMount(t *testing.T) {
	tests := []struct {
		name string
		op   string
		path string
	}{
		{"read", "read", "/Volumes/My Shared Files/ml-explore/file.txt"},
		{"write", "write", "/Volumes/My Shared Files/ml-explore/file.txt"},
		{"copy", "cp", "/Volumes/My Shared Files/ml-explore/file.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RouteFor(tt.op, tt.path, false); got != RouteUser {
				t.Fatalf("RouteFor(%q, %q, false) = %v, want %v", tt.op, tt.path, got, RouteUser)
			}
		})
	}
}

func TestRouteForExecSharedFoldersMount(t *testing.T) {
	args := []string{"ls", "-1", "/Volumes/My Shared Files/ml-explore"}
	if got := RouteForExec(args, false); got != RouteUser {
		t.Fatalf("RouteForExec(%v, false) = %v, want %v", args, got, RouteUser)
	}
}
