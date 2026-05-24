package main

import (
	"fmt"
	"io"

	fleetpkg "github.com/tmc/cove/internal/fleet"
	"github.com/tmc/cove/internal/imagestore"
)

func runLocalCoveCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) < 2 || args[0] != "image" {
		return fmt.Errorf("fleet local command unsupported: %v", args)
	}
	switch args[1] {
	case "push":
		if len(args) != 4 || args[3] != "-" {
			return fmt.Errorf("fleet local image push unsupported: %v", args)
		}
		ref, err := ParseImageRef(args[2])
		if err != nil {
			return err
		}
		return imagestore.WriteTar(ref, stdout, false)
	case "load":
		if len(args) != 3 || args[2] != "-" {
			return fmt.Errorf("fleet local image load unsupported: %v", args)
		}
		_, err := imagestore.ReadTar(stdin, "", false)
		return err
	default:
		return fmt.Errorf("fleet local image command unsupported: %v", args)
	}
}

func isLocalFleetRemote(remote fleetpkg.Remote) bool {
	return remote.Host == ""
}
