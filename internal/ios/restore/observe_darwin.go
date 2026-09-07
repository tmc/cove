//go:build darwin

package restore

import (
	"context"
	"fmt"
	"slices"

	"github.com/tmc/apple/x/irecovery"
)

// ObserveRecovery reads signing inputs from an already-open recovery connection.
// It requires the intended ECID, known board/security flags, Image4 support and
// an AP nonce. USB observations do not authenticate the device. Demotion policy
// remains unknown because these descriptors do not supply it.
func ObserveRecovery(ctx context.Context, conn *irecovery.Conn, ecid uint64) (SigningDevice, error) {
	if ecid == 0 {
		return SigningDevice{}, fmt.Errorf("observed signing ECID is required")
	}
	if conn == nil {
		return SigningDevice{}, fmt.Errorf("recovery connection is required")
	}
	info, err := conn.ReadInfo(ctx)
	if err != nil {
		return SigningDevice{}, err
	}
	return signingObservation(info, ecid)
}
func signingObservation(info irecovery.Info, ecid uint64) (SigningDevice, error) {
	if ecid == 0 || info.Device.ECID != ecid {
		return SigningDevice{}, fmt.Errorf("observed recovery ECID does not match target")
	}
	if info.Device.CPID == 0 || info.BoardID == nil || info.CPFM == nil || info.IBFL == nil || len(info.APNonce) == 0 {
		return SigningDevice{}, fmt.Errorf("recovery signing observation is incomplete")
	}
	if *info.IBFL&4 == 0 {
		return SigningDevice{}, fmt.Errorf("recovery device does not report Image4 support")
	}
	if info.Device.Mode != "dfu" && info.Device.Mode != "recovery" {
		return SigningDevice{}, fmt.Errorf("unexpected recovery mode %q", info.Device.Mode)
	}
	return SigningDevice{ECID: ecid, BoardID: uint64(*info.BoardID), ChipID: uint64(info.Device.CPID), APNonce: slices.Clone(info.APNonce), SEPNonce: slices.Clone(info.SEPNonce), ProductionMode: *info.CPFM&2 != 0, SecurityMode: *info.CPFM&1 != 0, InRomDFU: info.Device.Mode == "dfu"}, nil
}
