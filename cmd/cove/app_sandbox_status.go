package main

import (
	"os"
	"strings"
)

const appleAppSandboxContainerEnv = "APP_SANDBOX_CONTAINER_ID"

type appleAppSandboxStatus struct {
	Active      bool
	ContainerID string
}

var checkAppleAppSandboxEntitlement = appleAppSandboxEntitlement

func currentAppleAppSandboxStatus() appleAppSandboxStatus {
	id := os.Getenv(appleAppSandboxContainerEnv)
	if id == "" {
		id = appleAppSandboxContainerIDFromHome(os.Getenv("HOME"))
	}
	return appleAppSandboxStatus{
		Active:      id != "" || checkAppleAppSandboxEntitlement(),
		ContainerID: id,
	}
}

func appleAppSandboxContainerIDFromHome(home string) string {
	const marker = "/Library/Containers/"
	i := strings.Index(home, marker)
	if i < 0 {
		return ""
	}
	rest := home[i+len(marker):]
	id, ok := strings.CutSuffix(rest, "/Data")
	if !ok || id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}
