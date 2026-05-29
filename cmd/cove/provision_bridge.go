package main

import (
	"fmt"
	"os"

	"github.com/tmc/cove/internal/provision"
)

// Provision types are defined in internal/provision and re-exported here.
type ProvisionConfig = provision.ProvisionConfig
type InjectOptions = provision.InjectOptions
type ProvisionManifest = provision.ProvisionManifest
type ProvisionManifestFile = provision.ProvisionManifestFile
type stagingFingerprint = provision.StagingFingerprint

func stageFile(stagingDir, relativePath string, data []byte, mode os.FileMode, owner string, manifest *ProvisionManifest) error {
	if err := provision.StageFile(stagingDir, relativePath, data, mode, owner, manifest); err != nil {
		return err
	}
	if verbose {
		fmt.Printf("  staged: %s\n", relativePath)
	}
	return nil
}

func writeManifest(stagingDir string, manifest *ProvisionManifest) error {
	return provision.WriteManifest(stagingDir, manifest)
}

func readManifest(stagingDir string) (*ProvisionManifest, error) {
	return provision.ReadManifest(stagingDir)
}

func makeStagingFingerprint(opts InjectOptions) stagingFingerprint {
	return provision.MakeStagingFingerprint(opts)
}

func writeStagingFingerprint(stagingDir string, fp stagingFingerprint) error {
	return provision.WriteStagingFingerprint(stagingDir, fp)
}

func stagingMatchesOptions(stagingDir string, opts InjectOptions) (bool, error) {
	return provision.StagingMatchesOptions(stagingDir, opts)
}

func readStagingFingerprint(stagingDir string) (stagingFingerprint, bool) {
	return provision.ReadStagingFingerprint(stagingDir)
}

func validateUsername(username string) error {
	return provision.ValidateUsername(username)
}

func shellEscape(s string) string {
	return provision.ShellEscape(s)
}

func manifestNeedsRootProvisioning(manifest *ProvisionManifest) bool {
	return provision.ManifestNeedsRootProvisioning(manifest, autoLoginLaunchDaemonRelativePath)
}

func manifestIncludesAgent(manifest *ProvisionManifest) bool {
	return provision.ManifestIncludesAgent(manifest, agentBinaryName, agentLaunchDaemonLabel, agentLaunchAgentLabel)
}

func manifestIncludesLoginScreenCredentials(manifest *ProvisionManifest) bool {
	return provision.ManifestIncludesLoginScreenCredentials(manifest)
}

func rootWheelVerifyTargets(manifest *ProvisionManifest, mountPoint string) []string {
	return provision.RootWheelVerifyTargets(manifest, mountPoint)
}
