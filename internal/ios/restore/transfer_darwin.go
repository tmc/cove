//go:build darwin

package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/tmc/apple/x/irecovery"
	"github.com/tmc/apple/x/plist"
)

// ComponentTransfer describes one component operation within a restore attempt.
// DFU transfers require NextMode and perform manifestation/reset. Recovery
// transfers may issue a load command, or go followed by a verified disconnect
// and recovery reconnect. Transition into restored requires a separate dispatcher.
type ComponentTransfer struct {
	Library   string
	ECID      uint64
	Identity  map[string]any
	Component string
	Payload   []byte
	Mode      string
	NextMode  string
	Command   string
}

type transferRecord struct {
	ECID               uint64    `json:"ecid"`
	Component          string    `json:"component"`
	Mode               string    `json:"mode"`
	NextMode           string    `json:"nextMode,omitempty"`
	Command            string    `json:"command,omitempty"`
	PayloadSHA256      string    `json:"payloadSHA256"`
	IdentitySHA256     string    `json:"identitySHA256"`
	PersonalizedSHA256 string    `json:"personalizedSHA256,omitempty"`
	Action             string    `json:"action"`
	Status             string    `json:"status"`
	Error              string    `json:"error,omitempty"`
	Updated            time.Time `json:"updated"`
}

type componentConnection interface {
	ReadInfo(context.Context) (irecovery.Info, error)
	Upload(context.Context, []byte) error
	FinalizeDFU(context.Context) error
	SendCommand(context.Context, string, uint8) error
	WaitDisconnected(context.Context) error
	Close() error
}

// TransferComponent owns the connection for one observed, signed component
// transfer. Directory is an exclusive attempt directory, with a durable receipt
// written before each device mutation. Any existing receipt blocks replay,
// including failed or completed attempts; reconciliation is the caller's job.
// Completion means transfer and requested manifestation/command transmission,
// not guest boot, command execution or complete restore. No retry is automatic.
// The operation has a ten-minute ceiling or the caller's earlier deadline.
func TransferComponent(ctx context.Context, directory string, plan ComponentTransfer, client *http.Client) error {
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	return transferComponent(ctx, directory, plan, func(ctx context.Context, mode string) (componentConnection, error) {
		return irecovery.WaitOpen(ctx, plan.Library, plan.ECID, mode)
	}, func(ctx context.Context, identity map[string]any, device SigningDevice) (map[string]any, error) {
		return SignAP(ctx, client, identity, device, []string{plan.Component})
	}, filepath.Join(cache, "cove", "ios", "restore-locks"))
}

func transferComponent(ctx context.Context, directory string, plan ComponentTransfer, connect func(context.Context, string) (componentConnection, error), sign func(context.Context, map[string]any, SigningDevice) (map[string]any, error), lockRoot string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if directory == "" || plan.Library == "" || plan.ECID == 0 || plan.Component == "" || len(plan.Payload) == 0 {
		return fmt.Errorf("component transfer requires directory, library, ECID, component and payload")
	}
	switch plan.Mode {
	case "dfu":
		if plan.Command != "" || (plan.NextMode != "dfu" && plan.NextMode != "recovery") {
			return fmt.Errorf("dfu transfer requires next mode and no command")
		}
	case "recovery":
		if plan.Command == "go" {
			if plan.NextMode != "recovery" {
				return fmt.Errorf("go requires recovery reconnect")
			}
			break
		}
		if plan.NextMode != "" {
			return fmt.Errorf("load commands must not request reconnect")
		}
		// These load commands do not transition USB modes. Do not accept arbitrary
		// boot commands under a completion contract that cannot verify their effects.
		switch plan.Command {
		case "", "devicetree", "rsepfirmware", "firmware", "ramdisk", "lpolrestore", "setpicture 0", "bgcolor 0 0 0":
		default:
			return fmt.Errorf("unsupported component load command %q", plan.Command)
		}
	default:
		return fmt.Errorf("unsupported component transfer mode %q", plan.Mode)
	}
	encoded, err := plist.Marshal(plan.Identity, plist.FormatXML)
	if err != nil {
		return err
	}
	value, err := plist.ParseBytes(encoded)
	if err != nil {
		return err
	}
	identity, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("build identity is not a dictionary")
	}
	info, ok := identity["Info"].(map[string]any)
	if !ok {
		return fmt.Errorf("build identity Info is not a dictionary")
	}
	payload := slices.Clone(plan.Payload)
	state := transferRecord{ECID: plan.ECID, Component: plan.Component, Mode: plan.Mode, NextMode: plan.NextMode, Command: plan.Command, PayloadSHA256: transferHash(payload), IdentitySHA256: transferHash(encoded), Status: "active", Action: "observe"}
	deviceLock, err := lockTransferDevice(lockRoot, plan.ECID)
	if err != nil {
		return err
	}
	defer deviceLock.Close()
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("transfer directory is not a directory")
	}
	lock, err := os.OpenFile(filepath.Join(directory, "transfer.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("lock component transfer: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	record := filepath.Join(directory, "transfer.json")
	if _, err := os.Lstat(record); err == nil {
		return fmt.Errorf("component transfer already recorded; reconcile device state before another attempt")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	save := func(action string) error {
		state.Action = action
		state.Updated = time.Now().UTC()
		return writeTransferRecord(directory, state)
	}
	if err := save("observe"); err != nil {
		return err
	}
	var conn componentConnection
	defer func() {
		if conn != nil {
			err = errors.Join(err, conn.Close())
		}
		if err != nil {
			state.Status = "failed"
			state.Error = err.Error()
			err = errors.Join(err, save(state.Action))
		}
	}()
	conn, err = connect(ctx, plan.Mode)
	if err != nil {
		return err
	}
	observed, err := conn.ReadInfo(ctx)
	if err != nil {
		return err
	}
	device, err := signingObservation(observed, plan.ECID)
	if err != nil {
		return err
	}
	if observed.Device.Mode != plan.Mode {
		return fmt.Errorf("component transfer mode changed")
	}
	response, err := sign(ctx, identity, device)
	if err != nil {
		return err
	}
	image, err := Personalize(plan.Component, payload, response, info, nil)
	if err != nil {
		return err
	}
	state.PersonalizedSHA256 = transferHash(image)
	observed, err = conn.ReadInfo(ctx)
	if err != nil {
		return err
	}
	current, err := signingObservation(observed, plan.ECID)
	if err != nil {
		return err
	}
	if !sameSigningObservation(device, current) {
		return fmt.Errorf("device observations changed while signing; no component was uploaded")
	}
	if err := save("upload"); err != nil {
		return err
	}
	if err := conn.Upload(ctx, image); err != nil {
		return err
	}
	if plan.Mode == "dfu" {
		if err := save("finalize"); err != nil {
			return err
		}
		if err := conn.FinalizeDFU(ctx); err != nil {
			return err
		}
	} else if plan.Command != "" {
		if err := save("command"); err != nil {
			return err
		}
		request := uint8(0)
		if plan.Command == "go" {
			request = 1
		}
		if err := conn.SendCommand(ctx, plan.Command, request); err != nil {
			return err
		}
		if plan.NextMode != "" {
			if err := save("disconnect"); err != nil {
				return err
			}
			if err := conn.WaitDisconnected(ctx); err != nil {
				return err
			}
		}
	}
	if plan.NextMode != "" {
		closeErr := conn.Close()
		conn = nil
		if closeErr != nil {
			return closeErr
		}
		if err := save("reconnect"); err != nil {
			return err
		}
		conn, err = connect(ctx, plan.NextMode)
		if err != nil {
			return err
		}
		observed, err = conn.ReadInfo(ctx)
		if err != nil {
			return err
		}
		current, err = signingObservation(observed, plan.ECID)
		if err != nil {
			return err
		}
		if observed.Device.Mode != plan.NextMode || current.BoardID != device.BoardID || current.ChipID != device.ChipID {
			return fmt.Errorf("reconnected device does not match transfer target")
		}
		if plan.Component == "iBSS" && strings.Contains(observed.Device.Serial, "SRTG:") {
			return fmt.Errorf("iBSS transfer did not leave ROM")
		}
	}
	closeErr := conn.Close()
	conn = nil
	if closeErr != nil {
		return closeErr
	}
	state.Status = "complete"
	return save("complete")
}
func sameSigningObservation(a, b SigningDevice) bool {
	return a.ECID == b.ECID && a.BoardID == b.BoardID && a.ChipID == b.ChipID && a.ProductionMode == b.ProductionMode && a.SecurityMode == b.SecurityMode && a.InRomDFU == b.InRomDFU && bytes.Equal(a.APNonce, b.APNonce) && bytes.Equal(a.SEPNonce, b.SEPNonce)
}
func transferHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func writeTransferRecord(directory string, state transferRecord) (err error) {
	f, err := os.CreateTemp(directory, ".transfer-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(state); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(directory, "transfer.json")); err != nil {
		return err
	}
	d, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func lockTransferDevice(root string, ecid uint64) (*os.File, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	st, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("device lock root is not a directory")
	}
	f, err := os.OpenFile(filepath.Join(root, fmt.Sprintf("%016x.lock", ecid)), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("recovery ECID %x is in use: %w", ecid, err)
	}
	return f, nil
}
