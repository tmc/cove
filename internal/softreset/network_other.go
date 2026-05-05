//go:build !darwin && !linux

package softreset

import (
	"context"
	"fmt"
)

func ListNetworkSockets(context.Context) ([]NetworkSocket, error) {
	return nil, fmt.Errorf("network socket listing unsupported")
}
