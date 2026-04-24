package main

import (
	"github.com/tmc/vz-macos/internal/bytefmt"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

type VMConfig = vmconfig.Config
type VMAgentConfig = vmconfig.AgentConfig
type VolumeMount = vmconfig.VolumeMount
type VMInfo = vmconfig.Info
type vmconfigHardware = vmconfig.Hardware
type vmconfigHardwareExplicit = vmconfig.HardwareExplicit

var LoadVMConfig = vmconfig.Load
var SaveVMConfig = vmconfig.Save
var formatByteSize = bytefmt.Size
var vmconfigApplyHardware = vmconfig.ApplyHardware
var vmconfigSetHardware = vmconfig.SetHardware
var vmconfigSetPostInstallRecipes = vmconfig.SetPostInstallRecipes
var vmconfigSetVolumes = vmconfig.SetVolumes
var vmconfigInfoFor = vmconfig.InfoFor
var vmconfigList = vmconfig.List
var vmconfigResolveDir = vmconfig.ResolveDir
var vmconfigEnsureDir = vmconfig.EnsureDir

var GetVMBaseDir = vmconfig.BaseDir
var GetTemplateDir = vmconfig.TemplateDir
var GetCacheDir = vmconfig.CacheDir
var GetCurrentVMLink = vmconfig.CurrentLink
var GetVMPath = vmconfig.Path

var isSubdir = vmconfig.IsSubdir

var VMFiles = vmconfig.Files
var VMFilesRequired = vmconfig.RequiredFiles
var ValidateVM = vmconfig.Validate
var ListOrphanVMs = vmconfig.ListOrphans
var GetActiveVM = vmconfig.ActiveName
var SetActiveVM = vmconfig.SetActive
var UnsetActiveVM = vmconfig.UnsetActive
var MigrateIfNeeded = vmconfig.MigrateIfNeeded

var ensureVMAlias = vmconfig.EnsureAlias
var hasSuspendStateAt = vmconfig.HasSuspendState
var detectOSType = vmconfig.DetectOSType
