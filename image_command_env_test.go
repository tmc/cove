package main

import (
	"bytes"
	"strings"
)

func imageTestEnv() commandEnv {
	return commandEnv{
		Stdin:  strings.NewReader(""),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
}

func imageTestEnvWithStdout(out *bytes.Buffer) commandEnv {
	env := imageTestEnv()
	env.Stdout = out
	return env
}
