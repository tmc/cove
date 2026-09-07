package restore

import (
	"bytes"
	"fmt"

	"github.com/tmc/apple/x/img4"
)

// TicketRequirements identifies the observed device and current nonces, plus
// image digests from the selected signing build's manifest. Digests are keyed by
// ticket image FourCC. They describe the signed inputs, not patched research
// payloads. At least one image digest and a nonempty APNonce are required.
type TicketRequirements struct {
	ECID         uint64
	BoardID      uint64
	ChipID       uint64
	APNonce      []byte
	SEPNonce     []byte
	ImageDigests map[string][]byte

	// Non-nil policy fields require the corresponding MANP assertion.
	SecurityDomain *uint64
	ProductionMode *bool
	SecurityMode   *bool
}

// MatchTicket checks IM4M assertions against want. It does not verify the ticket
// signature, certificate chain, source, or the digest of a payload. A matching
// result is a consistency check, not authorization to boot or restore. Callers
// must obtain want independently from the observed device and selected build,
// and repeat this check after any nonce change.
func MatchTicket(ticket []byte, want TicketRequirements) error {
	if want.ECID == 0 || want.ChipID == 0 || len(want.APNonce) == 0 || len(want.ImageDigests) == 0 {
		return fmt.Errorf("ticket matching requires ECID, chip ID, AP nonce and image digests")
	}
	manifest, err := img4.ParseManifest(ticket)
	if err != nil {
		return fmt.Errorf("parse ticket: %w", err)
	}
	if want.SecurityDomain != nil {
		value, ok := manifest.Properties["SDOM"].(uint64)
		if !ok || value != *want.SecurityDomain {
			return fmt.Errorf("ticket SDOM does not match signing security domain")
		}
	}
	for _, p := range []struct {
		name  string
		value *bool
	}{{"CPRO", want.ProductionMode}, {"CSEC", want.SecurityMode}} {
		if p.value == nil {
			continue
		}
		value, ok := manifest.Properties[p.name].(bool)
		if !ok || value != *p.value {
			return fmt.Errorf("ticket %s does not match observed security policy", p.name)
		}
	}
	for _, p := range []struct {
		name  string
		value uint64
	}{
		{"ECID", want.ECID}, {"BORD", want.BoardID}, {"CHIP", want.ChipID},
	} {
		value, ok := manifest.Properties[p.name].(uint64)
		if !ok || value != p.value {
			return fmt.Errorf("ticket %s does not match device", p.name)
		}
	}
	for _, p := range []struct {
		name  string
		value []byte
	}{
		{"BNCH", want.APNonce}, {"snon", want.SEPNonce},
	} {
		value, present := manifest.Properties[p.name]
		if len(p.value) == 0 {
			if present {
				return fmt.Errorf("ticket %s has no observed nonce", p.name)
			}
			continue
		}
		nonce, ok := value.([]byte)
		if !ok || !bytes.Equal(nonce, p.value) {
			return fmt.Errorf("ticket %s does not match current nonce", p.name)
		}
	}
	for name, digest := range want.ImageDigests {
		value, ok := manifest.Images[name]["DGST"].([]byte)
		if len(digest) == 0 || !ok || !bytes.Equal(value, digest) {
			return fmt.Errorf("ticket image %s does not match signing build digest", name)
		}
	}
	return nil
}
