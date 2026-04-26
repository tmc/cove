package main

import "testing"

func TestAgentRouteFor(t *testing.T) {
	tests := []struct {
		name       string
		op         string
		path       string
		linuxGuest bool
		want       agentRoute
	}{
		{name: "ping is daemon", op: "ping", want: routeDaemon},
		{name: "info is daemon", op: "info", want: routeDaemon},
		{name: "exec is daemon (caller asked for root)", op: "exec", want: routeDaemon},
		{name: "user-exec is user", op: "user-exec", want: routeUser},
		{name: "user-exec-stream is user", op: "user-exec-stream", want: routeUser},
		{name: "shutdown is daemon", op: "shutdown", want: routeDaemon},
		{name: "reboot is daemon", op: "reboot", want: routeDaemon},
		{name: "sshd is daemon", op: "sshd", want: routeDaemon},
		{name: "mount-volumes stays daemon (root)", op: "mount-volumes", want: routeDaemon},
		{name: "read /var stays daemon", op: "read", path: "/var/log/install.log", want: routeDaemon},
		{name: "read ~/Documents routes to user (TCC)", op: "read", path: "/Users/me/Documents/x.txt", want: routeUser},
		{name: "read ~/Library routes to user (TCC)", op: "read", path: "/Users/me/Library/Preferences/x.plist", want: routeUser},
		{name: "read ~/.ssh stays daemon (root has access)", op: "read", path: "/Users/me/.ssh/known_hosts", want: routeDaemon},
		{name: "write /Library system stays daemon", op: "write", path: "/Library/LaunchDaemons/x.plist", want: routeDaemon},
		{name: "write /Volumes virtiofs routes to user", op: "write", path: "/Volumes/share/x.txt", want: routeUser},
		{name: "write Macintosh HD stays daemon", op: "write", path: "/Volumes/Macintosh HD/etc/hosts", want: routeDaemon},
		{name: "cp ~/Desktop routes to user", op: "cp", path: "/Users/me/Desktop/screenshot.png", want: routeUser},
		{name: "cp empty path stays daemon", op: "cp", path: "", want: routeDaemon},
		{name: "linux guest forces daemon for user-exec", op: "user-exec", linuxGuest: true, want: routeDaemon},
		{name: "linux guest forces daemon for /Users TCC", op: "read", path: "/Users/me/Documents/x", linuxGuest: true, want: routeDaemon},
		{name: "unknown op defaults to daemon", op: "weird-thing", want: routeDaemon},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentRouteFor(tt.op, tt.path, tt.linuxGuest)
			if got != tt.want {
				t.Errorf("agentRouteFor(%q, %q, linux=%v) = %s, want %s",
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
			if got := isUserPath(tt.path); got != tt.want {
				t.Errorf("isUserPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
