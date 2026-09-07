package restore

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/tmc/apple/x/iosrestore"
)

// RestoreImage is an already-open filesystem image for ASR. The caller retains
// ownership and must keep it open until RunRestored and its handlers return.
type RestoreImage struct {
	Reader io.ReaderAt
	Size   int64
}

// SessionData provides native handlers for root tickets, build identities,
// volume-bound local policy and ASR images. Ticket requirements must be retained
// from the corresponding signing request. Identity selects the appropriate build
// for the request and restore behavior. Inputs must remain immutable throughout
// the session. Other data types return an error and require additional handlers.
type SessionData struct {
	APTicket, RecoveryTicket             []byte
	APRequirements, RecoveryRequirements TicketRequirements
	SystemImage, RecoveryImage           RestoreImage
	Identity                             func(context.Context, map[string]any) (map[string]any, error)
	Device                               SigningDevice
	Client                               *http.Client
}

// Handle serves one request on the connection selected by RunRestored. It does
// not close the connection or images. It checks ticket assertions before sending
// them and delegates ASR to iosrestore.SendImage, without retrying writes.
func (d *SessionData) Handle(ctx context.Context, conn net.Conn, message map[string]any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d == nil || conn == nil {
		return fmt.Errorf("restore data and service connection are required")
	}
	dataType, _ := message["DataType"].(string)
	switch dataType {
	case "RootTicket", "RecoveryOSRootTicketData":
		ticket, want := d.APTicket, d.APRequirements
		if dataType == "RecoveryOSRootTicketData" {
			ticket, want = d.RecoveryTicket, d.RecoveryRequirements
		}
		if err := MatchTicket(ticket, want); err != nil {
			return fmt.Errorf("match %s: %w", dataType, err)
		}
		return iosrestore.Send(conn, map[string]any{"RootTicketData": ticket})
	case "BuildIdentityDict", "RecoveryOSLocalPolicy":
		if d.Identity == nil {
			return fmt.Errorf("restore build identity selector is required")
		}
		identity, err := d.Identity(ctx, message)
		if err != nil {
			return err
		}
		if identity == nil {
			return fmt.Errorf("restore build identity selector returned no identity")
		}
		if dataType == "BuildIdentityDict" {
			arguments, ok := message["Arguments"].(map[string]any)
			if !ok {
				return fmt.Errorf("build identity request Arguments is not a dictionary")
			}
			variant := "Erase"
			if value, present := arguments["Variant"]; present {
				var ok bool
				variant, ok = value.(string)
				if !ok || variant == "" {
					return fmt.Errorf("build identity request Variant is not a nonempty string")
				}
			}
			return iosrestore.Send(conn, map[string]any{"BuildIdentityDict": identity, "Variant": variant})
		}
		arguments, ok := message["Arguments"].(map[string]any)
		if !ok {
			return fmt.Errorf("volume policy request Arguments is not a dictionary")
		}
		response, err := VolumePolicyResponse(ctx, d.Client, identity, d.Device, arguments)
		if err != nil {
			return err
		}
		return iosrestore.Send(conn, response)
	case "SystemImageData", "RecoveryOSASRImage":
		image := d.SystemImage
		if dataType == "RecoveryOSASRImage" {
			image = d.RecoveryImage
		}
		if image.Reader == nil || image.Size <= 0 {
			return fmt.Errorf("restore filesystem image is required for %s", dataType)
		}
		return iosrestore.SendImage(ctx, conn, image.Reader, image.Size)
	default:
		return fmt.Errorf("unsupported restored data type %q", dataType)
	}
}
