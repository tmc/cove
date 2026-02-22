package utils

import (
	"golang.org/x/sys/unix"
)

// Already exists

// savedTermios stores the original terminal settings for restoration
var savedTermios *unix.Termios
