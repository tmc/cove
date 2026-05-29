package main

import "github.com/tmc/cove/internal/covecli"

func runCommandsCommand(env commandEnv, _ string, args []string) int {
	return covecli.RunCommandsCommand(env.Stdout, env.Stderr, args, covecli.Inventory(commandRegistry))
}
