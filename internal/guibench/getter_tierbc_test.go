package guibench

import (
	"strings"
	"testing"
)

// TestTierBCGetters exercises the Tier-B and Tier-C getters through a
// [FakeProbe], asserting both the command each emits and the value it shapes.
// Live FDA/Apple-Events/Accessibility reads need operator hardware and are the
// gate recorded as BLOCKED; these unit tests cover everything that does not
// need a VM.
func TestTierBCGetters(t *testing.T) {
	const (
		mailDB    = "/Users/tmc/Library/Mail/V10/MailData/Envelope Index"
		sqliteKey = "sqlite3 " + mailDB + " PRAGMA wal_checkpoint(FULL);\nSELECT subject FROM messages LIMIT 1;"
		tccKey    = "sqlite3 /Library/Application Support/com.apple.TCC/TCC.db SELECT auth_value FROM access WHERE service='kTCCServiceAccessibility';"
		asKey     = "osascript -e tell application \"Safari\" to return URL of current tab of front window"
		jxaKey    = "osascript -l JavaScript -e Application('Safari').windows[0].currentTab.url()"
		axKey     = "osascript -e tell application \"System Events\" to tell process \"Safari\" to get value of UI element \"Address\" of front window"
	)

	probe := FakeProbe{
		Files: map[string]string{},
		Commands: map[string]ExecResult{
			"cat /Users/tmc/Library/Preferences/com.apple.dock.plist": {ExitCode: 0, Stdout: "binary-ish\n"},
			"cat /Users/tmc/Library/Denied":                           {ExitCode: 1, Stderr: "cat: Operation not permitted"},
			sqliteKey:                                                 {ExitCode: 0, Stdout: "0|3|3\nHello Alice\n"},
			tccKey:                                                    {ExitCode: 0, Stdout: "2\n"},
			asKey:                                                     {ExitCode: 0, Stdout: "https://example.com/\n"},
			jxaKey:                                                    {ExitCode: 0, Stdout: "https://example.com/\n"},
			axKey:                                                     {ExitCode: 0, Stdout: "example.com\n"},
		},
	}

	tests := []struct {
		name    string
		spec    GetterSpec
		params  map[string]string
		want    string
		wantErr bool
	}{
		{
			name: "protected_file ok",
			spec: GetterSpec{Kind: "protected_file", Path: "/Users/tmc/Library/Preferences/com.apple.dock.plist"},
			want: "binary-ish\n",
		},
		{
			name:    "protected_file fda denied",
			spec:    GetterSpec{Kind: "protected_file", Path: "/Users/tmc/Library/Denied"},
			wantErr: true,
		},
		{
			name:   "sqlite drops checkpoint row",
			spec:   GetterSpec{Kind: "sqlite", Path: "{DB}", Query: "SELECT subject FROM messages LIMIT 1;"},
			params: map[string]string{"DB": mailDB},
			want:   "Hello Alice",
		},
		{
			name: "tccdb default path",
			spec: GetterSpec{Kind: "tccdb", Query: "SELECT auth_value FROM access WHERE service='kTCCServiceAccessibility';"},
			want: "2",
		},
		{
			name: "applescript one-shot",
			spec: GetterSpec{Kind: "applescript", Script: "tell application \"Safari\" to return URL of current tab of front window"},
			want: "https://example.com/",
		},
		{
			name: "applescript jxa",
			spec: GetterSpec{Kind: "applescript", JXA: true, Script: "Application('Safari').windows[0].currentTab.url()"},
			want: "https://example.com/",
		},
		{
			name: "accessibility element attr",
			spec: GetterSpec{Kind: "accessibility", App: "Safari", Element: "Address", Attr: "value"},
			want: "example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.spec.Get(probe, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Get = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSQLiteCheckpointFirst asserts the sqlite getter checkpoints the WAL
// before the query (design 047 §7): the emitted SQL must begin with the
// wal_checkpoint pragma, ahead of the query, in a single invocation.
func TestSQLiteCheckpointFirst(t *testing.T) {
	var gotSQL string
	probe := execSpyProbe(func(args []string) (int, string, string, error) {
		if len(args) == 3 && args[0] == "sqlite3" {
			gotSQL = args[2]
			return 0, "0|1|1\nrow\n", "", nil
		}
		return 0, "", "", nil
	})
	spec := GetterSpec{Kind: "sqlite", Path: "/tmp/x.db", Query: "SELECT 1;"}
	if _, err := spec.Get(probe, nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	checkpoint := strings.Index(gotSQL, "wal_checkpoint")
	query := strings.Index(gotSQL, "SELECT 1;")
	if checkpoint < 0 || query < 0 {
		t.Fatalf("SQL missing checkpoint or query: %q", gotSQL)
	}
	if checkpoint > query {
		t.Fatalf("checkpoint must precede query, got SQL %q", gotSQL)
	}
}

// TestAXScriptQuoting asserts the accessibility getter quotes app and element
// names so a value with a quote cannot break out of the AppleScript literal.
func TestAXScriptQuoting(t *testing.T) {
	got := axScript(`Sa"fari`, `Ad"dress`, "value")
	if strings.Contains(got, `"Sa"fari"`) {
		t.Fatalf("app name not escaped: %q", got)
	}
	if !strings.Contains(got, `\"`) {
		t.Fatalf("expected escaped quote in %q", got)
	}
}

func TestDropCheckpointLine(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"0|3|3\nHello", "Hello"},
		{"0|3|3", ""},
		{"Hello", "Hello"},
		{"Hello\nWorld", "Hello\nWorld"},
		{"0|3|3\nrow1\nrow2", "row1\nrow2"},
		{"not|a|checkpoint", "not|a|checkpoint"},
	}
	for _, tt := range tests {
		if got := dropCheckpointLine(tt.in); got != tt.want {
			t.Fatalf("dropCheckpointLine(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// execSpyProbe is a [Probe] whose Exec delegates to fn, for asserting the exact
// command a getter emits. ReadFile/OCRAllText are unused by these tests.
type execSpyProbe func(args []string) (int, string, string, error)

func (f execSpyProbe) Exec(args []string, _ map[string]string, _ string) (int, string, string, error) {
	return f(args)
}
func (f execSpyProbe) ReadFile(string) ([]byte, error) { return nil, nil }
func (f execSpyProbe) OCRAllText() (string, error)     { return "", nil }
