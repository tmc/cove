package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestParseUserAuditArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want userAuditOptions
	}{
		{
			name: "positional vm before flags",
			args: []string{"work", "--user", "alice", "--json"},
			want: userAuditOptions{VM: "work", User: "alice", JSON: true},
		},
		{
			name: "vm flag before positional",
			args: []string{"-vm", "work", "-user", "alice"},
			want: userAuditOptions{VM: "work", User: "alice"},
		},
		{
			name: "flags after positional",
			args: []string{"work", "-json", "-user", "alice"},
			want: userAuditOptions{VM: "work", User: "alice", JSON: true},
		},
		{
			name: "active vm allowed",
			args: []string{"-user", "alice"},
			want: userAuditOptions{User: "alice"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUserAuditArgs(commandTestEnv(), tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseUserAuditArgs() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseUserAuditArgsErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing user", []string{"work"}, "-user is required"},
		{"vm mismatch", []string{"work", "-vm", "other", "-user", "alice"}, `-vm "other" does not match positional VM "work"`},
		{"bad user", []string{"work", "-user", "bad/name"}, "invalid username"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseUserAuditArgs(commandTestEnv(), tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("parseUserAuditArgs() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestCollectUserAuditDarwin(t *testing.T) {
	agent := &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{
		{Stdout: "Darwin\n"},
		{Stdout: strings.Join([]string{
			"exists\ttrue",
			"uid\t501",
			"gid\t20",
			"home\t/Users/alice",
			"shell\t/bin/zsh",
			"groups\tstaff admin",
			"admin\ttrue",
			"sudo\ttrue",
			"home_exists\ttrue",
			"ssh_authorized_keys\ttrue",
			"launch_agent\t/Users/alice/Library/LaunchAgents/com.example.plist",
			"keychain\t/Users/alice/Library/Keychains/login.keychain-db",
			"cove_file\t/private/var/db/.vz-provisioned",
			"residue\thome|/Users/alice",
			"residue\tssh_authorized_keys|/Users/alice/.ssh/authorized_keys",
			"",
		}, "\n")},
	}}
	report, err := collectUserAudit(agent, "alice", "mac")
	if err != nil {
		t.Fatal(err)
	}
	if report.VM != "mac" || report.User != "alice" || report.GuestOS != guestOSDarwin {
		t.Fatalf("report identity = %+v", report)
	}
	if !report.Exists || !report.Admin || !report.Sudo || !report.HomeExists || !report.SSHAuthorizedKeys {
		t.Fatalf("report booleans = %+v", report)
	}
	if !reflect.DeepEqual(report.Groups, []string{"staff", "admin"}) {
		t.Fatalf("groups = %#v", report.Groups)
	}
	if len(report.Residue) != 2 || report.Residue[1].Kind != "ssh_authorized_keys" {
		t.Fatalf("residue = %#v", report.Residue)
	}
	if len(agent.args) != 2 || !reflect.DeepEqual(agent.args[0], []string{"uname", "-s"}) {
		t.Fatalf("agent args = %#v", agent.args)
	}
	if got := strings.Join(agent.args[1], " "); !strings.Contains(got, "/bin/sh -lc") {
		t.Fatalf("audit command = %#v", agent.args[1])
	}
	if !strings.Contains(agent.args[1][2], "dscl") || !strings.Contains(agent.args[1][2], "alice") {
		t.Fatalf("darwin audit script missing expected content:\n%s", agent.args[1][2])
	}
}

func TestCollectUserAuditLinux(t *testing.T) {
	agent := &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{
		{Stdout: "Linux\n"},
		{Stdout: strings.Join([]string{
			"exists\ttrue",
			"uid\t1000",
			"gid\t1000",
			"home\t/home/dev",
			"shell\t/bin/bash",
			"groups\tdev sudo",
			"admin\ttrue",
			"sudo\ttrue",
			"home_exists\ttrue",
			"ssh_authorized_keys\tfalse",
			"residue\tsystemd_user|/home/dev/.config/systemd/user",
			"",
		}, "\n")},
	}}
	report, err := collectUserAudit(agent, "dev", "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if report.GuestOS != guestOSLinux || report.UID != "1000" || !report.Admin || report.SSHAuthorizedKeys {
		t.Fatalf("report = %+v", report)
	}
	if len(report.Residue) != 1 || report.Residue[0].Kind != "systemd_user" {
		t.Fatalf("residue = %#v", report.Residue)
	}
	if !strings.Contains(agent.args[1][2], "getent passwd") {
		t.Fatalf("linux audit script missing getent:\n%s", agent.args[1][2])
	}
}

func TestWriteUserAuditOutputs(t *testing.T) {
	report := userAuditReport{
		VM:         "mac",
		User:       "alice",
		GuestOS:    guestOSDarwin,
		Exists:     true,
		Groups:     []string{"staff", "admin"},
		Home:       "/Users/alice",
		HomeExists: true,
		Residue:    []userAuditResidue{{Kind: "home", Path: "/Users/alice"}},
	}
	var text bytes.Buffer
	if err := writeUserAuditText(&text, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"vm:", "mac", "groups:", "staff, admin", "home"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("text output missing %q:\n%s", want, text.String())
		}
	}
	var js bytes.Buffer
	if err := writeUserAuditJSON(&js, report); err != nil {
		t.Fatal(err)
	}
	var decoded userAuditReport
	if err := json.Unmarshal(js.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.User != "alice" || len(decoded.Residue) != 1 {
		t.Fatalf("decoded json = %+v", decoded)
	}
}
