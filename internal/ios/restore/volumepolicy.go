package restore

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// VolumePolicySigningRequest builds the LocalBoot=true request from restored's
// Arguments dictionary. Identity must be the selected recovery OS build for an
// erase restore, or the selected update build for an update. Device supplies the
// independently observed ECID, board/chip IDs and security flags; AP/SEP nonces
// are not sent. The two supplied hashes must contain 48 bytes, and VolumeUUID
// must be a 32-digit hex or canonical hyphenated UUID string. Inputs are copied.
func VolumePolicySigningRequest(identity map[string]any, device SigningDevice, arguments map[string]any) (map[string]any, error) {
	if device.ECID == 0 || device.ChipID == 0 {
		return nil, fmt.Errorf("volume policy requires observed ECID and chip ID")
	}
	numbers := make(map[string]uint64)
	for _, key := range []string{"ApBoardID", "ApChipID", "ApSecurityDomain"} {
		n, err := manifestNumber(identity[key])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		numbers[key] = n
	}
	if numbers["ApBoardID"] != device.BoardID || numbers["ApChipID"] != device.ChipID {
		return nil, fmt.Errorf("volume policy build does not match observed board and chip IDs")
	}
	request, err := newSigningRequest()
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"Ap,NextStageIM4MHash", "Ap,RecoveryOSPolicyNonceHash"} {
		value, ok := arguments[key].([]byte)
		if !ok || len(value) != sha512.Size384 {
			return nil, fmt.Errorf("%s must contain 48 bytes", key)
		}
		request[key] = slices.Clone(value)
	}
	uuid, ok := arguments["Ap,VolumeUUID"].(string)
	if !ok {
		return nil, fmt.Errorf("Ap,VolumeUUID must be a UUID string")
	}
	if len(uuid) == 36 {
		if uuid[8] != '-' || uuid[13] != '-' || uuid[18] != '-' || uuid[23] != '-' {
			return nil, fmt.Errorf("invalid Ap,VolumeUUID hyphens")
		}
		uuid = strings.ReplaceAll(uuid, "-", "")
	}
	if len(uuid) != 32 {
		return nil, fmt.Errorf("Ap,VolumeUUID must contain 32 hex digits")
	}
	volume, err := hex.DecodeString(uuid)
	if err != nil {
		return nil, fmt.Errorf("decode Ap,VolumeUUID: %w", err)
	}
	policyHash := sha512.Sum384([]byte(emptyLocalPolicy))
	maps.Copy(request, map[string]any{
		"@ApImg4Ticket": true, "Ap,LocalBoot": true,
		"ApECID": device.ECID, "ApBoardID": device.BoardID, "ApChipID": device.ChipID,
		"ApSecurityDomain": numbers["ApSecurityDomain"],
		"ApProductionMode": device.ProductionMode, "ApSecurityMode": device.SecurityMode,
		"Ap,LocalPolicy": map[string]any{"Digest": policyHash[:], "Trusted": true},
		"Ap,VolumeUUID":  volume,
	})
	return request, nil
}

// VolumePolicyResponse signs and personalizes the empty local policy, returning
// the Ap,LocalPolicy dictionary for restored's data-request service. The caller
// must bind arguments to the selected device's current restore request, choose
// the appropriate build identity and send the response on that request's service.
// Hashes are compared with the ticket's nsih/ronh/vuid assertions; they are not
// recomputed from a preceding AP ticket in this flow. Signature authentication,
// service routing and the complete restore lifecycle remain caller responsibilities.
func VolumePolicyResponse(ctx context.Context, client *http.Client, identity map[string]any, device SigningDevice, arguments map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, ok := identity["Info"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("volume policy build Info is not a dictionary")
	}
	request, err := VolumePolicySigningRequest(identity, device, arguments)
	if err != nil {
		return nil, err
	}
	response, err := signPolicyRequest(ctx, client, request, nil)
	if err != nil {
		return nil, err
	}
	data, err := Personalize("Ap,LocalPolicy", EmptyLocalPolicy(), response, info, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Ap,LocalPolicy": data}, nil
}
