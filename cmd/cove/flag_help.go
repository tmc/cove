// flag_help.go - shared handling of `-h` for flag-parsed subcommands.
//
// Go's flag package returns flag.ErrHelp from Parse when -h or -help is
// requested. Callers that propagate that error through commandError exit
// nonzero, which is wrong for the golden help path. parseFlagsOrHelp
// converts ErrHelp into a successful no-op (after printing usage to
// stdout) so commandError returns 0.

package main

import (
	"errors"
	"flag"
	"os"
)

// parseFlagsOrHelp parses args, treating -h/-help as a successful exit.
// On flag.ErrHelp it invokes fs.Usage() against stdout and returns the
// sentinel errFlagHelp, which handlers must check and translate to a
// nil return so commandError yields exit 0.
func parseFlagsOrHelp(fs *flag.FlagSet, args []string) error {
	for _, arg := range args {
		if isHelpArg(arg) {
			prev := fs.Output()
			fs.SetOutput(os.Stdout)
			if fs.Usage != nil {
				fs.Usage()
			} else {
				fs.PrintDefaults()
			}
			fs.SetOutput(prev)
			return errFlagHelp
		}
	}
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		prev := fs.Output()
		fs.SetOutput(os.Stdout)
		if fs.Usage != nil {
			fs.Usage()
		} else {
			fs.PrintDefaults()
		}
		fs.SetOutput(prev)
		return errFlagHelp
	}
	return err
}

// errFlagHelp is the sentinel returned by parseFlagsOrHelp when the
// user asked for help. Handlers should `if errors.Is(err, errFlagHelp)
// { return nil }` so commandError exits 0.
var errFlagHelp = errors.New("flag help requested")

// parseFlagsOrHelpExit is the parse-and-translate convenience: it
// returns (true, nil) when -h was handled (caller should `return nil`),
// (false, err) on a real parse error, and (false, nil) on success.
func parseFlagsOrHelpExit(fs *flag.FlagSet, args []string) (handled bool, err error) {
	if perr := parseFlagsOrHelp(fs, args); perr != nil {
		if errors.Is(perr, errFlagHelp) {
			return true, nil
		}
		return false, perr
	}
	return false, nil
}
