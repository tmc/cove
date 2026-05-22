//go:build !windows

package main

import "fmt"

func clipboardGetText() (string, error) {
	return "", fmt.Errorf("clipboard helper requires windows")
}

func clipboardSetText(string) error {
	return fmt.Errorf("clipboard helper requires windows")
}
