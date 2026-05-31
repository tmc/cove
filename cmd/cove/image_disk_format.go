package main

import (
	"strings"

	"github.com/tmc/cove/internal/diskimages2"
)

var retrieveDiskImageInfo = diskimages2.RetrieveInfo

func detectImageDiskFormat(path string) string {
	info, err := retrieveDiskImageInfo(path)
	if err != nil || info == nil {
		return string(diskImageFormatRaw)
	}
	if format, ok := knownImageDiskFormat(info.Raw["Image Format"]); ok {
		return format
	}
	return string(diskImageFormatRaw)
}

func normalizeImageDiskFormat(value string) string {
	if format, ok := knownImageDiskFormat(value); ok {
		return format
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func knownImageDiskFormat(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(diskImageFormatRaw):
		return string(diskImageFormatRaw), true
	case string(diskImageFormatASIF):
		return string(diskImageFormatASIF), true
	default:
		return "", false
	}
}
