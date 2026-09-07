//go:build darwin

package ios

import (
	"context"
	"github.com/tmc/apple/x/irecovery"
	"github.com/tmc/apple/x/usbmux"
)

// Discovery keeps normal/restored usbmux attachments separate from raw USB
// DFU/recovery endpoints. An empty libusb path omits raw USB enumeration.
type Discovery struct {
	Devices         []usbmux.Device    `json:"devices"`
	Recovery        []irecovery.Device `json:"recovery,omitempty"`
	RecoveryChecked bool               `json:"recoveryChecked"`
	Errors          []string           `json:"errors,omitempty"`
}

// DiscoverDevices inspects host device endpoints without opening a service,
// claiming an interface, starting a VM or sending a restore request.
func DiscoverDevices(ctx context.Context, libusb string) Discovery {
	report := Discovery{Devices: []usbmux.Device{}}
	devices, err := (usbmux.Client{}).List(ctx)
	if err != nil {
		report.Errors = append(report.Errors, err.Error())
	} else {
		report.Devices = devices
	}
	if libusb != "" {
		report.RecoveryChecked = true
		report.Recovery, err = irecovery.Discover(ctx, libusb)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
		}
	}
	return report
}
