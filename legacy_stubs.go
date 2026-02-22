package main

import "fmt"

func fetchLatestRestoreImage() error {
	_, err := fetchLatestRestoreImageObject()
	return err
}


func handleSetup(args []string) error {
	return fmt.Errorf("setup command not yet implemented in this build")
}

