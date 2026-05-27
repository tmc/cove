package main

import "os"

const appleAppSandboxContainerEnv = "APP_SANDBOX_CONTAINER_ID"

type appleAppSandboxStatus struct {
	Active      bool
	ContainerID string
}

func currentAppleAppSandboxStatus() appleAppSandboxStatus {
	id := os.Getenv(appleAppSandboxContainerEnv)
	return appleAppSandboxStatus{
		Active:      id != "",
		ContainerID: id,
	}
}
