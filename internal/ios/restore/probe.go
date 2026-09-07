package restore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/tmc/apple/x/iosrestore"
	"github.com/tmc/apple/x/usbmux"
)

// Target requires an observed ECID. Serial optionally narrows discovery; it
// never replaces the hardware identity check.
type Target struct {
	ECID   uint64
	Serial string
}

// Device is a USB attachment whose restored service reported the requested ECID.
type Device struct {
	Attachment usbmux.Device   `json:"attachment"`
	Restore    iosrestore.Info `json:"restore"`
}

type mux interface {
	List(context.Context) ([]usbmux.Device, error)
	Dial(context.Context, uint32, uint16) (net.Conn, error)
}

// Probe finds exactly one restored service with target's identity. Each candidate
// has a three-second deadline. Incomplete probes fail discovery rather than hide
// a possible duplicate. No restore or reboot command is sent.
func Probe(ctx context.Context, client usbmux.Client, target Target) (Device, error) {
	return probe(ctx, client, target)
}

func probe(ctx context.Context, client mux, target Target) (Device, error) {
	var selected Device
	if target.ECID == 0 {
		return selected, fmt.Errorf("restore ECID is required")
	}
	devices, err := client.List(ctx)
	if err != nil {
		return selected, err
	}
	matches := 0
	var failures []error
	for _, device := range devices {
		if device.ConnectionType != "USB" || (target.Serial != "" && device.Serial != target.Serial) {
			continue
		}
		candidate, cancel := context.WithTimeout(ctx, 3*time.Second)
		conn, err := client.Dial(candidate, device.ID, 62078)
		var info iosrestore.Info
		if err == nil {
			info, err = iosrestore.QueryInfo(candidate, conn)
			err = errors.Join(err, conn.Close())
		}
		cancel()
		if err != nil {
			if errors.Is(err, iosrestore.ErrNotRestored) {
				continue
			}
			failures = append(failures, fmt.Errorf("probe USB attachment %d: %w", device.ID, err))
			continue
		}
		if info.ECID != target.ECID {
			continue
		}
		matches++
		selected = Device{Attachment: device, Restore: info}
	}
	if err := ctx.Err(); err != nil {
		return Device{}, err
	}
	if len(failures) != 0 {
		return Device{}, errors.Join(failures...)
	}
	if matches == 0 {
		return Device{}, fmt.Errorf("no restored USB device matches ECID %d", target.ECID)
	}
	if matches != 1 {
		return Device{}, fmt.Errorf("multiple restored USB devices match ECID %d", target.ECID)
	}
	return selected, nil
}
