package version

import "testing"

func TestResolveRelease(t *testing.T) {
	got := Resolve("v1.2.3", "abc12345", "2026-04-23T00:00:00Z")
	want := Info{Version: "v1.2.3", Commit: "abc12345", Date: "2026-04-23T00:00:00Z"}
	if got != want {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}

func TestFormat(t *testing.T) {
	info := Info{Version: "v1.2.3", Commit: "abc12345", Date: "2026-04-23T00:00:00Z"}
	got := Format("cove", info)
	want := "cove v1.2.3 (commit abc12345, built 2026-04-23T00:00:00Z)"
	if got != want {
		t.Fatalf("Format() = %q, want %q", got, want)
	}
}

func TestHost(t *testing.T) {
	tests := []struct {
		name string
		info Info
		want string
	}{
		{name: "release", info: Info{Version: "v1.2.3", Commit: "abc12345"}, want: "v1.2.3"},
		{name: "dev commit", info: Info{Version: "dev", Commit: "abc12345"}, want: "abc12345"},
		{name: "dev unknown", info: Info{Version: "dev", Commit: "unknown"}, want: "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Host(tt.info); got != tt.want {
				t.Fatalf("Host() = %q, want %q", got, tt.want)
			}
		})
	}
}
