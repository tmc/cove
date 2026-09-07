package restore

import (
	"bytes"
	"context"
	"crypto/sha512"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/tmc/apple/x/img4"
	"github.com/tmc/apple/x/iosrestore"
)

const emptyLocalPolicy = "\x30\x14\x16\x04IM4P\x16\x04lpol\x16\x031.0\x04\x01\x00"

// EmptyLocalPolicy returns a fresh copy of the empty recovery policy IM4P.
func EmptyLocalPolicy() []byte {
	return []byte(emptyLocalPolicy)
}

// LocalPolicySigningRequest constructs the recovery-stage local-policy request.
// NextTicket must match the current device and the selected build's next-stage
// components. Its SHA-384 hash binds the policy to that exact AP ticket. This
// requests LocalBoot=false; restored's volume-bound LocalBoot=true flow is separate.
// No SEP nonce or UniqueBuildID is sent in the local-policy request.
func LocalPolicySigningRequest(identity map[string]any, device SigningDevice, nextTicket []byte, components []string) (map[string]any, error) {
	_, want, err := APSigningRequest(identity, device, components)
	if err != nil {
		return nil, err
	}
	if err := MatchTicket(nextTicket, want); err != nil {
		return nil, fmt.Errorf("match next-stage ticket: %w", err)
	}
	request, err := newSigningRequest()
	if err != nil {
		return nil, err
	}
	policyHash := sha512.Sum384([]byte(emptyLocalPolicy))
	nextHash := sha512.Sum384(nextTicket)
	maps.Copy(request, map[string]any{
		"@ApImg4Ticket": true, "Ap,LocalBoot": false,
		"ApECID": device.ECID, "ApBoardID": device.BoardID, "ApChipID": device.ChipID,
		"ApSecurityDomain": *want.SecurityDomain,
		"ApProductionMode": device.ProductionMode, "ApSecurityMode": device.SecurityMode,
		"ApNonce":              slices.Clone(device.APNonce),
		"Ap,LocalPolicy":       map[string]any{"Digest": policyHash[:], "Trusted": true},
		"Ap,NextStageIM4MHash": nextHash[:],
	})
	return request, nil
}

// SignLocalPolicy requests a recovery-stage local-policy ticket over trusted
// HTTPS and checks its device, policy digest and next-stage binding. The preceding
// AP ticket must match current observations. An optional returned BNCH or snon
// must match those observations too. These are consistency checks, not signature
// verification. The caller must retain and use the same next-stage ticket.
func SignLocalPolicy(ctx context.Context, client *http.Client, identity map[string]any, device SigningDevice, nextTicket []byte, components []string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, err := LocalPolicySigningRequest(identity, device, nextTicket, components)
	if err != nil {
		return nil, err
	}
	return signPolicyRequest(ctx, client, request, device.SEPNonce)
}

func signPolicyRequest(ctx context.Context, client *http.Client, request map[string]any, sepNonce []byte) (map[string]any, error) {
	response, err := iosrestore.Tickets(ctx, apSigningClient(client), "", request)
	if err != nil {
		return nil, err
	}
	ticket, ok := response["ApImg4Ticket"].([]byte)
	if !ok || len(ticket) == 0 {
		return nil, fmt.Errorf("local-policy response has no ApImg4Ticket data")
	}
	if err := matchLocalPolicy(ticket, request, sepNonce); err != nil {
		return nil, fmt.Errorf("match local-policy response: %w", err)
	}
	return response, nil
}

func matchLocalPolicy(ticket []byte, request map[string]any, sepNonce []byte) error {
	manifest, err := img4.ParseManifest(ticket)
	if err != nil {
		return err
	}
	for _, field := range []struct{ tag, key string }{
		{"ECID", "ApECID"}, {"BORD", "ApBoardID"}, {"CHIP", "ApChipID"}, {"SDOM", "ApSecurityDomain"},
		{"CPRO", "ApProductionMode"}, {"CSEC", "ApSecurityMode"}, {"lobo", "Ap,LocalBoot"},
	} {
		if manifest.Properties[field.tag] != request[field.key] {
			return fmt.Errorf("local-policy %s does not match request", field.tag)
		}
	}
	nsih, ok := manifest.Properties["nsih"].([]byte)
	if !ok || !bytes.Equal(nsih, request["Ap,NextStageIM4MHash"].([]byte)) {
		return fmt.Errorf("local-policy nsih does not match next-stage ticket")
	}
	digest, ok := manifest.Images["lpol"]["DGST"].([]byte)
	want := request["Ap,LocalPolicy"].(map[string]any)["Digest"].([]byte)
	if !ok || !bytes.Equal(digest, want) {
		return fmt.Errorf("local-policy digest does not match empty policy")
	}
	for _, field := range []struct{ tag, key string }{
		{"ronh", "Ap,RecoveryOSPolicyNonceHash"}, {"vuid", "Ap,VolumeUUID"},
	} {
		if want, present := request[field.key]; present {
			value, ok := manifest.Properties[field.tag].([]byte)
			if !ok || !bytes.Equal(value, want.([]byte)) {
				return fmt.Errorf("local-policy %s does not match request", field.tag)
			}
		}
	}
	apNonce, _ := request["ApNonce"].([]byte)
	for _, field := range []struct {
		tag   string
		nonce []byte
	}{{"BNCH", apNonce}, {"snon", sepNonce}} {
		if value, present := manifest.Properties[field.tag]; present {
			nonce, ok := value.([]byte)
			if !ok || len(field.nonce) == 0 || !bytes.Equal(nonce, field.nonce) {
				return fmt.Errorf("local-policy %s does not match observed nonce", field.tag)
			}
		}
	}
	return nil
}
