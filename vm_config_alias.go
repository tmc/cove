package main

import "github.com/tmc/vz-macos/internal/vmconfig"

type VMConfig = vmconfig.Config
type VMAgentConfig = vmconfig.AgentConfig
type VolumeMount = vmconfig.VolumeMount

var LoadVMConfig = vmconfig.Load
var SaveVMConfig = vmconfig.Save
