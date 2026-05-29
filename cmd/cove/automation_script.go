package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

func isVZScriptAutomationFile(path string, data []byte) bool {
	if strings.EqualFold(filepath.Ext(path), ".vzscript") {
		return true
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return !strings.HasPrefix(line, "<")
	}
	return false
}

func unsupportedAutomationScriptError(path string) error {
	return fmt.Errorf("unsupported automation format for %s: use vzscript; legacy angle-bracket boot command scripts are no longer supported", path)
}
