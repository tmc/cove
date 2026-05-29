package main

import "github.com/tmc/cove/internal/vmconfig"

type SharedFolderEntry = vmconfig.SharedFolderEntry

func LoadSharedFolders(vmDirectory string) []SharedFolderEntry {
	return vmconfig.LoadSharedFolders(vmDirectory)
}

func saveSharedFolders(vmDirectory string, folders []SharedFolderEntry) error {
	if err := denyAppleAppSandboxHostAccess("shared-folder mutation"); err != nil {
		return err
	}
	return vmconfig.SaveSharedFolders(vmDirectory, folders)
}

func uniqueTag(base string, existing []SharedFolderEntry) string {
	return vmconfig.UniqueSharedFolderTag(base, existing)
}

func sanitizeSharedFolderTag(base string) string {
	return vmconfig.SanitizeSharedFolderTag(base)
}
