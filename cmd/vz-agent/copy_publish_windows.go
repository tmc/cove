package main

import "fmt"

func publishAgentCopy(stage, dest string, overwrite bool) error {
	return fmt.Errorf("directory copy publication is not supported on windows")
}
