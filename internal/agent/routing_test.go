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
		{"/var/log/system.log", false},
		{"/Library/Preferences/x.plist", false},
		{"/private/var/db/.AppleSetupDone", false},
		{"/System/Library/Foo", false},
		{"/usr/local/bin/cove", false},
		{"/tmp/x", false},
		{"/Volumes/share", true},
		{"/Volumes/share/file.txt", true},
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
