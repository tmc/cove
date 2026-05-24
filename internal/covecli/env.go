package covecli

import (
	"io"
	"log/slog"
	"os"
)

type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Logger *slog.Logger

	VM      VMSelection
	Options Options
}

type VMSelection struct {
	Name string
	Dir  string
}

type Options struct {
	Verbose  bool
	Fleet    string
	Headless bool
	GUI      bool
	Linux    bool
}

func (env Env) WithDefaultIO() Env {
	if env.Stdin == nil {
		env.Stdin = os.Stdin
	}
	if env.Stdout == nil {
		env.Stdout = os.Stdout
	}
	if env.Stderr == nil {
		env.Stderr = os.Stderr
	}
	return env
}
