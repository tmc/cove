package main

import (
	"context"
	"os/user"
	"runtime"
)

func populateSystemInfo(_ context.Context, info *systemInfo) {
	info.OSVersion = runtime.GOOS
	info.KernelVersion = runtime.GOOS
}

func listLocalUsers(context.Context) ([]string, error) {
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	if u.Username == "" {
		return nil, nil
	}
	return []string{u.Username}, nil
}
