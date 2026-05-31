package main

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

type fakeUserLifecycleAgent struct {
	detect       *controlpb.AgentExecResponse
	mutate       *controlpb.AgentExecResponse
	detectArgs   [][]string
	mutateArgs   [][]string
	mutateEnv    []map[string]string
	mutateErr    error
	detectErr    error
	mutateWork   []string
	mutateTimout []time.Duration
}

func (f *fakeUserLifecycleAgent) AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	f.detectArgs = append(f.detectArgs, append([]string(nil), args...))
	if f.detectErr != nil {
		return nil, f.detectErr
	}
	if f.detect == nil {
		return &controlpb.AgentExecResponse{Stdout: "Linux\n"}, nil
	}
	return f.detect, nil
}

func (f *fakeUserLifecycleAgent) AgentDaemonExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	f.mutateArgs = append(f.mutateArgs, append([]string(nil), args...))
	cp := map[string]string{}
	for k, v := range env {
		cp[k] = v
	}
	f.mutateEnv = append(f.mutateEnv, cp)
	f.mutateWork = append(f.mutateWork, workDir)
	f.mutateTimout = append(f.mutateTimout, timeout)
	if f.mutateErr != nil {
		return nil, f.mutateErr
	}
	if f.mutate == nil {
		return &controlpb.AgentExecResponse{Stdout: "admin\tfalse\nhome\t/home/dev\nssh_authorized_keys\tfalse\n"}, nil
	}
	return f.mutate, nil
}

func TestParseUserCreateArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want userCreateOptions
	}{
		{
			name: "positional vm",
			args: []string{"vm1", "--user", "alice", "--admin", "--password", "pw", "--ssh-key", "/tmp/key", "--json"},
			want: userCreateOptions{VM: "vm1", User: "alice", Admin: true, PasswordSpec: "pw", SSHKeyPath: "/tmp/key", JSON: true},
		},
		{
			name: "flags only",
			args: []string{"-vm", "vm1", "-user", "alice", "-password", "env://PW"},
			want: userCreateOptions{VM: "vm1", User: "alice", PasswordSpec: "env://PW"},
		},
		{
			name: "flags after vm",
			args: []string{"vm1", "-password", "fd://3", "-user", "alice"},
			want: userCreateOptions{VM: "vm1", User: "alice", PasswordSpec: "fd://3"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUserCreateArgs(commandTestEnv(), tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseUserCreateArgs() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseUserCreateDeleteErrors(t *testing.T) {
	tests := []struct {
		name string
		fn   func() error
		want string
	}{
		{
			name: "create missing user",
			fn: func() error {
				_, err := parseUserCreateArgs(commandTestEnv(), []string{"vm1"})
				return err
			},
			want: "-user is required",
		},
		{
			name: "create vm mismatch",
			fn: func() error {
				_, err := parseUserCreateArgs(commandTestEnv(), []string{"vm1", "-vm", "vm2", "-user", "alice"})
				return err
			},
			want: `-vm "vm2" does not match positional VM "vm1"`,
		},
		{
			name: "delete bad user",
			fn: func() error {
				_, err := parseUserDeleteArgs(commandTestEnv(), []string{"vm1", "-user", "bad/name"})
				return err
			},
			want: "invalid username",
		},
		{
			name: "create option-shaped user",
			fn: func() error {
				_, err := parseUserCreateArgs(commandTestEnv(), []string{"vm1", "-user", "-admin"})
				return err
			},
			want: "invalid username",
		},
		{
			name: "delete owner-group-shaped user",
			fn: func() error {
				_, err := parseUserDeleteArgs(commandTestEnv(), []string{"vm1", "-user", "alice:staff"})
				return err
			},
			want: "invalid username",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestParseUserDeleteArgs(t *testing.T) {
	got, err := parseUserDeleteArgs(commandTestEnv(), []string{"vm1", "--user", "alice", "--keep-home", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	want := userDeleteOptions{VM: "vm1", User: "alice", KeepHome: true, JSON: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseUserDeleteArgs() = %+v, want %+v", got, want)
	}
}

func TestResolveUserPasswordSpecs(t *testing.T) {
	t.Setenv("COVE_TEST_USER_PASSWORD", "envpw\n")
	got, err := resolveUserPassword(commandTestEnv(), "alice", "env://COVE_TEST_USER_PASSWORD")
	if err != nil {
		t.Fatal(err)
	}
	if got != "envpw" {
		t.Fatalf("env password = %q", got)
	}

	dir := t.TempDir()
	secret := dir + "/pw"
	if err := os.WriteFile(secret, []byte("filepw\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = resolveUserPassword(commandTestEnv(), "alice", "file://"+secret)
	if err != nil {
		t.Fatal(err)
	}
	if got != "filepw" {
		t.Fatalf("file password = %q", got)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := w.WriteString("fdpw\n"); err != nil {
		t.Fatal(err)
	}
	w.Close()
	got, err = resolveUserPassword(commandTestEnv(), "alice", "fd://"+strconv.Itoa(int(r.Fd())))
	if err != nil {
		t.Fatal(err)
	}
	if got != "fdpw" {
		t.Fatalf("fd password = %q", got)
	}
}

func TestCreateGuestUserUsesDaemonEnv(t *testing.T) {
	agent := &fakeUserLifecycleAgent{
		detect: &controlpb.AgentExecResponse{Stdout: "Darwin\n"},
		mutate: &controlpb.AgentExecResponse{Stdout: strings.Join([]string{
			"admin\ttrue",
			"home\t/Users/alice",
			"ssh_authorized_keys\ttrue",
			"",
		}, "\n")},
	}
	opts := userCreateOptions{User: "alice", Admin: true}
	report, err := createGuestUser(agent, opts, "mac", "secret", "ssh-ed25519 AAAA test")
	if err != nil {
		t.Fatal(err)
	}
	if report.VM != "mac" || report.GuestOS != guestOSDarwin || !report.Admin || !report.SSHAuthorizedKeys {
		t.Fatalf("report = %+v", report)
	}
	if len(agent.mutateArgs) != 1 || !strings.Contains(agent.mutateArgs[0][2], "sysadminctl") {
		t.Fatalf("mutate args = %#v", agent.mutateArgs)
	}
	if strings.Contains(agent.mutateArgs[0][2], "secret") {
		t.Fatalf("password leaked into script:\n%s", agent.mutateArgs[0][2])
	}
	env := agent.mutateEnv[0]
	if env["COVE_USER_NAME"] != "alice" || env["COVE_USER_PASSWORD"] != "secret" || env["COVE_USER_ADMIN"] != "true" {
		t.Fatalf("env = %#v", env)
	}
}

func TestDeleteGuestUserLinux(t *testing.T) {
	agent := &fakeUserLifecycleAgent{
		detect: &controlpb.AgentExecResponse{Stdout: "Linux\n"},
		mutate: &controlpb.AgentExecResponse{Stdout: strings.Join([]string{
			"existed\ttrue",
			"deleted\ttrue",
			"home\t/home/dev",
			"home_removed\ttrue",
			"keep_home\tfalse",
			"",
		}, "\n")},
	}
	report, err := deleteGuestUser(agent, userDeleteOptions{User: "dev"}, "ubuntu")
	if err != nil {
		t.Fatal(err)
	}
	if report.VM != "ubuntu" || !report.Existed || !report.Deleted || !report.HomeRemoved || report.KeepHome {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(agent.mutateArgs[0][2], "userdel") {
		t.Fatalf("delete script missing userdel:\n%s", agent.mutateArgs[0][2])
	}
	if agent.mutateEnv[0]["COVE_USER_KEEP_HOME"] != "false" {
		t.Fatalf("env = %#v", agent.mutateEnv[0])
	}
}

func TestWriteUserMutationOutputs(t *testing.T) {
	report := userMutationReport{VM: "vm", User: "alice", GuestOS: guestOSLinux, Action: "delete", Existed: true, Deleted: true}
	var js strings.Builder
	if err := writeUserMutationJSON(&js, report); err != nil {
		t.Fatal(err)
	}
	var decoded userMutationReport
	if err := json.Unmarshal([]byte(js.String()), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Action != "delete" || !decoded.Deleted {
		t.Fatalf("decoded = %+v", decoded)
	}
	var text strings.Builder
	if err := writeUserMutationText(&text, report); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"action:", "delete", "deleted:", "yes"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("text missing %q:\n%s", want, text.String())
		}
	}
}
