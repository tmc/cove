package restore

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/tmc/apple/x/iosrestore"
	"github.com/tmc/apple/x/plist"
)

// SessionHandlers handles restored data requests and optional notifications.
// Data exclusively owns its service connection for the duration of the call but
// must not close it. Concurrent requests use separate connections and may call
// Data concurrently. Handlers must honor context cancellation. Event is called
// serially for progress, logs, checkpoints, AsyncWait and accepted baseband status.
type SessionHandlers struct {
	Data  func(context.Context, net.Conn, map[string]any) error
	Event func(context.Context, map[string]any) error
}

// RunRestored verifies control's ECID, sends StartRestore with explicit options,
// and serves requests until a successful final status has been acknowledged.
// It owns and closes control and connections returned by dial. Dial must connect
// to a port on the same selected device, honor ctx, and never select a replacement.
// Data-port connection and write failures are not retried. ASR requests without
// DataPort use port 12345; other synchronous requests use control. Asynchronous
// requests require a separate port and at most 16 may run concurrently.
//
// The caller must record durable restore intent before calling, select complete
// restore options and supply handlers for the required data types. This protocol
// loop does not perform DFU preparation, FDR setup or complete-attempt recovery.
func RunRestored(ctx context.Context, control net.Conn, ecid uint64, options map[string]any, dial func(context.Context, uint16) (net.Conn, error), handlers SessionHandlers) (err error) {
	if control == nil {
		return fmt.Errorf("restored control connection is required")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	var workers sync.WaitGroup
	defer func() {
		if err != nil {
			cancel(err)
		}
		err = errors.Join(err, control.Close())
		workers.Wait()
		if cause := context.Cause(ctx); cause != nil && !errors.Is(err, cause) {
			err = errors.Join(err, cause)
		}
		cancel(nil)
	}()
	if ecid == 0 || len(options) == 0 || dial == nil || handlers.Data == nil {
		return fmt.Errorf("restored session requires ECID, options, dialer and data handler")
	}
	encoded, err := plist.Marshal(options, plist.FormatXML)
	if err != nil {
		return err
	}
	value, err := plist.ParseBytes(encoded)
	if err != nil {
		return err
	}
	info, err := iosrestore.QueryInfo(ctx, control)
	if err != nil {
		return err
	}
	if info.ECID != ecid {
		return fmt.Errorf("restored control ECID does not match target")
	}
	stop, err := watchRestoreConnection(ctx, control)
	if err != nil {
		return err
	}
	defer stop()
	if err := iosrestore.Send(control, map[string]any{
		"Request": "StartRestore", "Label": "cove", "RestoreProtocolVersion": info.ProtocolVersion, "RestoreOptions": value,
	}); err != nil {
		return fmt.Errorf("start restore: %w", err)
	}
	slots := make(chan struct{}, 16)
	for {
		message, err := iosrestore.Receive(control)
		if err != nil {
			return fmt.Errorf("receive restored message: %w", err)
		}
		kind, _ := message["MsgType"].(string)
		switch kind {
		case "DataRequestMsg", "AsyncDataRequestMsg":
			dataType, ok := message["DataType"].(string)
			if !ok || dataType == "" {
				return fmt.Errorf("restored data request has no data type")
			}
			port, err := restoreDataPort(message, dataType)
			if err != nil {
				return err
			}
			if kind == "AsyncDataRequestMsg" {
				if port == 0 {
					return fmt.Errorf("asynchronous %s requires a dedicated data port", dataType)
				}
				select {
				case slots <- struct{}{}:
				default:
					return fmt.Errorf("too many concurrent restored data requests")
				}
				workers.Add(1)
				go func() {
					defer workers.Done()
					defer func() { <-slots }()
					if err := serveRestoreData(ctx, dial, port, message, handlers.Data); err != nil {
						cancel(fmt.Errorf("serve asynchronous %s: %w", dataType, err))
					}
				}()
			} else if port != 0 {
				if err := serveRestoreData(ctx, dial, port, message, handlers.Data); err != nil {
					return fmt.Errorf("serve %s: %w", dataType, err)
				}
			} else if err := handlers.Data(ctx, control, message); err != nil {
				return fmt.Errorf("serve %s: %w", dataType, err)
			}
		case "StatusMsg":
			status, err := restoreNumber(message["Status"])
			if err != nil {
				return fmt.Errorf("invalid restored status: %w", err)
			}
			if status != 0 {
				return fmt.Errorf("restored failed with status %d", status)
			}
			workers.Wait()
			if err := context.Cause(ctx); err != nil {
				return err
			}
			if err := iosrestore.Send(control, map[string]any{"MsgType": "ReceivedFinalStatusMsg"}); err != nil {
				return fmt.Errorf("acknowledge restored final status: %w", err)
			}
			return nil
		case "BBUpdateStatusMsg":
			accepted, ok := message["Accepted"].(bool)
			if !ok || !accepted {
				return fmt.Errorf("restored baseband update was not accepted")
			}
			fallthrough
		case "PreviousRestoreLogMsg", "ProgressMsg", "CheckpointMsg", "AsyncWait":
			if handlers.Event != nil {
				if err := handlers.Event(ctx, message); err != nil {
					return err
				}
			}
		case "RestoredCrash":
			return fmt.Errorf("restored reported a crash")
		default:
			return fmt.Errorf("unsupported restored message %q", kind)
		}
	}
}

func restoreDataPort(message map[string]any, dataType string) (uint16, error) {
	if value, present := message["DataPort"]; present {
		port, err := restoreNumber(value)
		if err != nil || port == 0 || port > 65535 {
			return 0, fmt.Errorf("invalid restored data port")
		}
		return uint16(port), nil
	}
	if dataType == "SystemImageData" || dataType == "RecoveryOSASRImage" {
		return 12345, nil
	}
	return 0, nil
}

func serveRestoreData(ctx context.Context, dial func(context.Context, uint16) (net.Conn, error), port uint16, message map[string]any, handle func(context.Context, net.Conn, map[string]any) error) (err error) {
	conn, err := dial(ctx, port)
	if err != nil {
		return err
	}
	if conn == nil {
		return fmt.Errorf("restored dialer returned no connection")
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	stop, err := watchRestoreConnection(ctx, conn)
	if err != nil {
		return err
	}
	defer stop()
	return handle(ctx, conn, message)
}

func watchRestoreConnection(ctx context.Context, conn net.Conn) (func(), error) {
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()); close(done) })
	return func() {
		if !stop() {
			<-done
		}
	}, nil
}

func restoreNumber(value any) (uint64, error) {
	switch value.(type) {
	case int, int64, uint64:
		return manifestNumber(value)
	}
	return 0, fmt.Errorf("expected restore protocol integer")
}
