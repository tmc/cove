package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseScriptMetaInject(t *testing.T) {
	input := []byte(`# test-recipe — Test recipe with inject
# requires: login
# inject: Library/LaunchDaemons/com.test.plist test.plist 0644 root:wheel
# inject: usr/local/bin/setup.sh setup.sh 0755

guest-ping
`)
	meta := parseScriptMeta(input)
	if meta.name != "test-recipe" {
		t.Errorf("name = %q, want %q", meta.name, "test-recipe")
	}
	if len(meta.inject) != 2 {
		t.Fatalf("got %d inject directives, want 2", len(meta.inject))
	}
	// First directive: all fields.
	if meta.inject[0].guestPath != "Library/LaunchDaemons/com.test.plist" {
		t.Errorf("inject[0].guestPath = %q", meta.inject[0].guestPath)
	}
	if meta.inject[0].txtarFile != "test.plist" {
		t.Errorf("inject[0].txtarFile = %q", meta.inject[0].txtarFile)
	}
	if meta.inject[0].mode != "0644" {
		t.Errorf("inject[0].mode = %q", meta.inject[0].mode)
	}
	if meta.inject[0].owner != "root:wheel" {
		t.Errorf("inject[0].owner = %q", meta.inject[0].owner)
	}
	// Second directive: no owner.
	if meta.inject[1].guestPath != "usr/local/bin/setup.sh" {
		t.Errorf("inject[1].guestPath = %q", meta.inject[1].guestPath)
	}
	if meta.inject[1].txtarFile != "setup.sh" {
		t.Errorf("inject[1].txtarFile = %q", meta.inject[1].txtarFile)
	}
	if meta.inject[1].mode != "0755" {
		t.Errorf("inject[1].mode = %q", meta.inject[1].mode)
	}
	if meta.inject[1].owner != "" {
		t.Errorf("inject[1].owner = %q, want empty", meta.inject[1].owner)
	}
}

func TestParseScriptMetaRunsOn(t *testing.T) {
	input := []byte(`# daemon-recipe — Runs as root
# runs-on: daemon
# requires: homebrew, golang

guest-exec /bin/echo hello
`)
	meta := parseScriptMeta(input)
	if meta.runsOn != "daemon" {
		t.Errorf("runsOn = %q, want %q", meta.runsOn, "daemon")
	}
	if len(meta.requires) != 2 {
		t.Fatalf("got %d requires, want 2", len(meta.requires))
	}
	if meta.requires[0] != "homebrew" || meta.requires[1] != "golang" {
		t.Errorf("requires = %v, want [homebrew golang]", meta.requires)
	}
	if meta.desc != "Runs as root" {
		t.Errorf("desc = %q, want %q", meta.desc, "Runs as root")
	}
}

func TestParseScriptMetaEmpty(t *testing.T) {
	meta := parseScriptMeta(nil)
	if meta.name != "" || len(meta.requires) != 0 || len(meta.inject) != 0 {
		t.Errorf("empty input should produce zero-value meta, got %+v", meta)
	}

	meta = parseScriptMeta([]byte("guest-ping\n"))
	if meta.name != "" {
		t.Errorf("no-comment input: name = %q, want empty", meta.name)
	}
}

func TestParseFileMode(t *testing.T) {
	tests := []struct {
		input string
		want  os.FileMode
	}{
		{"0755", 0755},
		{"0644", 0644},
		{"0700", 0700},
		{"", 0644},      // empty -> default
		{"bogus", 0644}, // unparseable -> default
	}
	for _, tt := range tests {
		got := parseFileMode(tt.input, 0644)
		if got != tt.want {
			t.Errorf("parseFileMode(%q, 0644) = %04o, want %04o", tt.input, got, tt.want)
		}
	}
}

func TestParseScriptMetaMount(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantMounts []mountDirective
		wantReqs   []string
		wantInject int
	}{
		{
			name: "tilde ro",
			input: `# mounts-ro — Mount read-only
# mount: ~/.cache/huggingface ro

guest-ping
`,
			wantMounts: []mountDirective{{hostPath: "~/.cache/huggingface", readOnly: true}},
		},
		{
			name: "absolute rw",
			input: `# mounts-rw — Mount read-write
# mount: /tmp/data rw

guest-ping
`,
			wantMounts: []mountDirective{{hostPath: "/tmp/data", readOnly: false}},
		},
		{
			name: "default rw",
			input: `# mounts-default — Default is rw
# mount: ~/ml-explore

guest-ping
`,
			wantMounts: []mountDirective{{hostPath: "~/ml-explore", readOnly: false}},
		},
		{
			name: "multiple mounts",
			input: `# multi-mount — Multiple mounts
# mount: ~/.cache/huggingface ro
# mount: /tmp/data rw
# mount: ~/ml-explore

guest-ping
`,
			wantMounts: []mountDirective{
				{hostPath: "~/.cache/huggingface", readOnly: true},
				{hostPath: "/tmp/data", readOnly: false},
				{hostPath: "~/ml-explore", readOnly: false},
			},
		},
		{
			name: "mount with requires and inject",
			input: `# combo — All directives
# requires: homebrew, golang
# inject: usr/local/bin/setup.sh setup.sh 0755
# mount: ~/data ro

guest-ping
`,
			wantMounts: []mountDirective{{hostPath: "~/data", readOnly: true}},
			wantReqs:   []string{"homebrew", "golang"},
			wantInject: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := parseScriptMeta([]byte(tt.input))
			if len(meta.mounts) != len(tt.wantMounts) {
				t.Fatalf("got %d mounts, want %d", len(meta.mounts), len(tt.wantMounts))
			}
			for i, want := range tt.wantMounts {
				got := meta.mounts[i]
				if got.hostPath != want.hostPath {
					t.Errorf("mounts[%d].hostPath = %q, want %q", i, got.hostPath, want.hostPath)
				}
				if got.readOnly != want.readOnly {
					t.Errorf("mounts[%d].readOnly = %v, want %v", i, got.readOnly, want.readOnly)
				}
			}
			if tt.wantReqs != nil {
				if len(meta.requires) != len(tt.wantReqs) {
					t.Fatalf("got %d requires, want %d", len(meta.requires), len(tt.wantReqs))
				}
				for i, want := range tt.wantReqs {
					if meta.requires[i] != want {
						t.Errorf("requires[%d] = %q, want %q", i, meta.requires[i], want)
					}
				}
			}
			if tt.wantInject > 0 && len(meta.inject) != tt.wantInject {
				t.Errorf("got %d inject directives, want %d", len(meta.inject), tt.wantInject)
			}
		})
	}
}

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}

	tests := []struct {
		input string
		want  string
	}{
		{"~/foo", filepath.Join(home, "foo")},
		{"~", home},
		{"/abs/path", "/abs/path"},
		{"relative", "relative"},
		{"", ""},
	}
	for _, tt := range tests {
		got := expandTilde(tt.input)
		if got != tt.want {
			t.Errorf("expandTilde(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
