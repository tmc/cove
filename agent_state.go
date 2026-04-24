package main

import (
	"strings"
	"time"

	agentstate "github.com/tmc/vz-macos/internal/agent"
)

const (
	vmAgentPlatformMacOS = agentstate.PlatformMacOS
	vmAgentPlatformLinux = agentstate.PlatformLinux

	vmAgentSourceInstall   = agentstate.SourceInstall
	vmAgentSourceProvision = agentstate.SourceProvision
	vmAgentSourceUpgrade   = agentstate.SourceUpgrade
	vmAgentSourceVerify    = agentstate.SourceVerify
	vmAgentSourceRuntime   = agentstate.SourceRuntime
)

func cloneVMAgentConfig(cfg *VMAgentConfig) *VMAgentConfig {
	return agentstate.CloneConfig(cfg)
}

func detectVMAgentPlatform(vmDirectory string) string {
	return agentstate.DetectPlatform(vmDirectory)
}

func vmAgentPlatform(vmDirectory string) string {
	return agentstate.Platform(vmDirectory)
}

func setVMAgentRequested(vmDirectory, platform string, requested bool, source string) error {
	return agentstate.SetRequested(vmDirectory, platform, requested, source)
}

func markVMAgentVerified(vmDirectory, platform, source string, when time.Time) error {
	return agentstate.MarkVerified(vmDirectory, platform, source, when)
}

func markCurrentVMAgentVerified(source string) error {
	if strings.TrimSpace(vmDir) == "" {
		return nil
	}
	return agentstate.MarkVerified(vmDir, agentstate.DetectPlatform(vmDir), source, time.Now())
}

func markVMAgentVerifiedForSocket(sock, source string) error {
	return agentstate.MarkVerifiedForSocket(sock, source)
}
