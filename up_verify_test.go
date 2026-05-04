package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type fakeProvisioningVerifierClient struct {
	args []string
	resp *controlpb.AgentExecResponse
	err  error
}

func (f *fakeProvisioningVerifierClient) AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	f.args = append([]string(nil), args...)
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func TestVerifyProvisionedGuestUserSuccess(t *testing.T) {
	client := &fakeProvisioningVerifierClient{resp: &controlpb.AgentExecResponse{
		ExitCode: 0,
		Stdout:   "UniqueID: 501\nNFSHomeDirectory: /Users/me\n",
	}}
	got, err := verifyProvisionedGuestUser(client, "me")
	if err != nil {
		t.Fatalf("verifyProvisionedGuestUser: %v", err)
	}
	if got.UID != 501 || got.Home != "/Users/me" {
		t.Fatalf("user = uid %d home %q, want uid 501 home /Users/me", got.UID, got.Home)
	}
	wantArgs := []string{"/usr/bin/dscl", ".", "-read", "/Users/me", "UniqueID", "NFSHomeDirectory"}
	if strings.Join(client.args, "\x00") != strings.Join(wantArgs, "\x00") {
		t.Fatalf("args = %#v, want %#v", client.args, wantArgs)
	}
}

func TestVerifyProvisionedGuestUserMissing(t *testing.T) {
	tests := []struct {
		name string
		resp *controlpb.AgentExecResponse
		err  error
	}{
		{name: "exec error", err: errors.New("agent unavailable")},
		{name: "exit nonzero", resp: &controlpb.AgentExecResponse{ExitCode: 1, Stderr: "No such key"}},
		{name: "empty fields", resp: &controlpb.AgentExecResponse{ExitCode: 0, Stdout: "UniqueID: \nNFSHomeDirectory: \n"}},
		{name: "missing home", resp: &controlpb.AgentExecResponse{ExitCode: 0, Stdout: "UniqueID: 501\n"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeProvisioningVerifierClient{resp: tt.resp, err: tt.err}
			_, err := verifyProvisionedGuestUser(client, "me")
			if err == nil {
				t.Fatal("verifyProvisionedGuestUser succeeded; want error")
			}
			want := "provisioning reported success but user me was not created in the guest"
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %q, want %q", err, want)
			}
			if !strings.Contains(err.Error(), "/var/log/vz-provision.log") {
				t.Fatalf("error = %q, want vz-provision log path", err)
			}
		})
	}
}
