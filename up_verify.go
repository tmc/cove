package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type provisioningVerifierClient interface {
	AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error)
}

type provisionedGuestUser struct {
	UID  int
	Home string
}

func verifyProvisionedGuestUser(client provisioningVerifierClient, user string) (provisionedGuestUser, error) {
	user = strings.TrimSpace(user)
	if user == "" {
		return provisionedGuestUser{}, fmt.Errorf("provisioning verification requires a user")
	}
	resp, err := client.AgentExecTypedTimeout([]string{
		"/usr/bin/dscl",
		".",
		"-read",
		"/Users/" + user,
		"UniqueID",
		"NFSHomeDirectory",
	}, nil, "", 30*time.Second)
	if err != nil {
		return provisionedGuestUser{}, provisioningUserMissingError(user)
	}
	if resp.GetExitCode() != 0 {
		return provisionedGuestUser{}, provisioningUserMissingError(user)
	}
	info, err := parseDSCLUserRecord(resp.GetStdout())
	if err != nil {
		return provisionedGuestUser{}, provisioningUserMissingError(user)
	}
	return info, nil
}

func parseDSCLUserRecord(out string) (provisionedGuestUser, error) {
	var info provisionedGuestUser
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "UniqueID":
			uid, err := strconv.Atoi(value)
			if err != nil || uid <= 0 {
				return provisionedGuestUser{}, fmt.Errorf("invalid unique id")
			}
			info.UID = uid
		case "NFSHomeDirectory":
			info.Home = value
		}
	}
	if info.UID == 0 || info.Home == "" {
		return provisionedGuestUser{}, fmt.Errorf("missing user fields")
	}
	return info, nil
}

func provisioningUserMissingError(user string) error {
	return fmt.Errorf("provisioning reported success but user %s was not created in the guest — check /var/log/vz-provision.log inside the VM", user)
}
