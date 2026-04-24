package main

import "github.com/tmc/vz-macos/internal/vmconfig"

type VMConfig = vmconfig.Config
type VMAgentConfig = vmconfig.AgentConfig
type VolumeMount = vmconfig.VolumeMount
type VMInfo = vmconfig.Info

var LoadVMConfig = vmconfig.Load
var SaveVMConfig = vmconfig.Save

var GetVMBaseDir = vmconfig.BaseDir
var GetVMPath = vmconfig.Path

var ValidateVM = vmconfig.Validate
