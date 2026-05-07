package main

import (
	"fmt"
	"image"

	"github.com/tmc/vz-macos/internal/controlclient"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type ControlClient struct {
	*controlclient.Client
}

func NewControlClient(socketPath string) *ControlClient {
	return &ControlClient{Client: controlclient.New(socketPath)}
}

func (c *ControlClient) sendRequest(req *controlpb.ControlRequest) (*controlpb.ControlResponse, error) {
	return c.SendRequest(req)
}

func (c *ControlClient) sendKeyEvent(keyCode uint16, keyDown bool, modifiers uint, useCGEvent bool) error {
	return c.SendKeyEvent(keyCode, keyDown, modifiers, useCGEvent)
}

func (c *ControlClient) DetectScreen() (image.Image, ScreenState, error) {
	img, err := c.Screenshot()
	if err != nil {
		return nil, ScreenStateUnknown, fmt.Errorf("screenshot failed: %w", err)
	}
	return img, DetectScreenState(img), nil
}

const (
	KeyCodeS      = controlclient.KeyCodeS
	KeyCodeD      = controlclient.KeyCodeD
	KeyCodeF      = controlclient.KeyCodeF
	KeyCodeH      = controlclient.KeyCodeH
	KeyCodeG      = controlclient.KeyCodeG
	KeyCodeZ      = controlclient.KeyCodeZ
	KeyCodeX      = controlclient.KeyCodeX
	KeyCodeC      = controlclient.KeyCodeC
	KeyCodeV      = controlclient.KeyCodeV
	KeyCodeB      = controlclient.KeyCodeB
	KeyCodeW      = controlclient.KeyCodeW
	KeyCodeE      = controlclient.KeyCodeE
	KeyCodeR      = controlclient.KeyCodeR
	KeyCodeY      = controlclient.KeyCodeY
	KeyCodeT      = controlclient.KeyCodeT
	KeyCodeN      = controlclient.KeyCodeN
	KeyCodeM      = controlclient.KeyCodeM
	KeyCodeO      = controlclient.KeyCodeO
	KeyCodeU      = controlclient.KeyCodeU
	KeyCodeI      = controlclient.KeyCodeI
	KeyCodeP      = controlclient.KeyCodeP
	KeyCodeL      = controlclient.KeyCodeL
	KeyCodeJ      = controlclient.KeyCodeJ
	KeyCodeK      = controlclient.KeyCodeK
	KeyCodePeriod = controlclient.KeyCodePeriod
)
