package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/tmc/vz-macos/internal/secrets"
)

func handleSecretCommand(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printSecretUsage(os.Stderr)
		return nil
	}
	switch args[0] {
	case "probe":
		if len(args) == 2 && isHelpArg(args[1]) {
			printSecretProbeUsage(os.Stderr)
			return nil
		}
		if len(args) != 2 {
			return fmt.Errorf("usage: cove secret probe <uri>")
		}
		value, err := secrets.Resolve(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("secret resolved: %s (length: %d bytes)\n", redactSecretURI(args[1]), len(value))
		return nil
	default:
		return fmt.Errorf("unknown secret command: %s", args[0])
	}
}

func printSecretProbeUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: cove secret probe <uri>

Resolve a secret URI and print only its byte length. The secret value is never
printed.

Supported URI schemes:
  env://VAR_NAME
  file:///absolute/path
`)
}

func printSecretUsage(w io.Writer) {
	fmt.Fprintf(w, `Usage: cove secret <command> [args...]

Commands:
  probe <uri>     Resolve a secret URI and print only its byte length

Supported URI schemes:
  env://VAR_NAME
  file:///absolute/path
`)
}

func redactSecretURI(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return raw
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return strings.TrimSuffix(u.String(), "?")
}
