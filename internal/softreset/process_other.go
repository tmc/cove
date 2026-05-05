//go:build !darwin && !linux

package softreset

import (
	"context"
	"fmt"
)

func ListProcesses(context.Context) ([]Process, error) {
	return nil, fmt.Errorf("process listing unsupported")
}
