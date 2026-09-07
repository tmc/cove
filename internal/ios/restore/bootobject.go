package restore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/tmc/apple/x/iosrestore"
)

const maxPersonalizedObject = 512 << 20

func (d *SessionData) sendBootObject(ctx context.Context, conn net.Conn, message map[string]any, dataType string) (err error) {
	name := dataType
	stream := dataType == "PersonalizedBootObjectV3" || dataType == "SourceBootObjectV4"
	if stream {
		arguments, ok := message["Arguments"].(map[string]any)
		if !ok {
			return fmt.Errorf("boot object Arguments is not a dictionary")
		}
		name, ok = arguments["ImageName"].(string)
		if !ok || name == "" {
			return fmt.Errorf("boot object ImageName is not a nonempty string")
		}
	}
	switch dataType {
	case "SystemImageRootHash":
		name = "SystemVolume"
	case "SystemImageCanonicalMetadata":
		name = "Ap,SystemVolumeCanonicalMetadata"
	}
	if d.Object == nil {
		return fmt.Errorf("restore boot object source is required")
	}
	source, err := d.Object(ctx, message, name)
	if err != nil {
		return err
	}
	if source == nil {
		return fmt.Errorf("restore boot object source returned no reader")
	}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { source.Close(); close(closed) })
	defer func() {
		if stop() {
			err = errors.Join(err, source.Close())
		} else {
			<-closed
		}
		if cause := ctx.Err(); cause != nil && !errors.Is(err, cause) {
			err = errors.Join(err, cause)
		}
	}()
	var reader io.Reader = source
	special := name == "__GlobalManifest__" || name == "__RestoreVersion__" || name == "__SystemVersion__"
	if dataType != "SourceBootObjectV4" && !special {
		if d.Identity == nil {
			return fmt.Errorf("restore build identity selector is required")
		}
		identity, err := d.Identity(ctx, message)
		if err != nil {
			return err
		}
		_, want, err := APSigningRequest(identity, d.Device, []string{name})
		if err != nil {
			return err
		}
		ticket, ok := d.APResponse["ApImg4Ticket"].([]byte)
		if !ok {
			return fmt.Errorf("boot object AP response has no ticket data")
		}
		if err := MatchTicket(ticket, want); err != nil {
			return fmt.Errorf("match boot object ticket: %w", err)
		}
		payload, err := io.ReadAll(io.LimitReader(source, maxPersonalizedObject+1))
		if err != nil {
			return err
		}
		if len(payload) > maxPersonalizedObject {
			return fmt.Errorf("personalized boot object exceeds 512 MiB")
		}
		info, _ := identity["Info"].(map[string]any)
		image, err := Personalize(name, payload, d.APResponse, info, d.Parameters)
		if err != nil {
			return err
		}
		if !stream {
			return iosrestore.Send(conn, map[string]any{dataType + "File": image})
		}
		reader = bytes.NewReader(image)
	}
	return streamBootObject(ctx, conn, reader, dataType == "SourceBootObjectV4", d.Asset)
}

func streamBootObject(ctx context.Context, conn net.Conn, reader io.Reader, source bool, asset func(context.Context, map[string]any) error) error {
	var buffer [8192]byte
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := io.ReadFull(reader, buffer[:])
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return fmt.Errorf("read boot object: %w", err)
		}
		if n > 0 {
			if err := iosrestore.Send(conn, map[string]any{"FileData": buffer[:n]}); err != nil {
				return err
			}
			if first && source && bytes.HasPrefix(buffer[:n], []byte("AEA1")) {
				if err := bootObjectAsset(ctx, conn, asset, 3*time.Second); err != nil {
					return err
				}
			}
			first = false
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return iosrestore.Send(conn, map[string]any{"FileDataDone": true})
}

type countedReader struct {
	io.Reader
	n int
}

func (r *countedReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += n
	return n, err
}

func bootObjectAsset(ctx context.Context, conn net.Conn, asset func(context.Context, map[string]any) error, wait time.Duration) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(wait)
	original, hasDeadline := ctx.Deadline()
	if hasDeadline && original.Before(deadline) {
		deadline = original
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.SetReadDeadline(original)) }()
	reader := &countedReader{Reader: conn}
	message, err := iosrestore.Receive(reader)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		var timeout net.Error
		if reader.n == 0 && errors.As(err, &timeout) && timeout.Timeout() {
			return nil
		}
		return fmt.Errorf("read AEA asset request: %w", err)
	}
	if message["MsgType"] != "URLAsset" {
		return fmt.Errorf("unexpected AEA asset request type")
	}
	if asset == nil {
		return fmt.Errorf("AEA URLAsset handler is required")
	}
	// The optional read timeout must not limit HTTP work or a nested service.
	if err := conn.SetReadDeadline(original); err != nil {
		return err
	}
	return asset(ctx, message)
}
