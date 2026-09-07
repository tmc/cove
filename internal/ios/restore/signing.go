package restore

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/tmc/apple/x/iosrestore"
	"github.com/tmc/apple/x/plist"
)

// SigningDevice contains observations from the current device connection.
// Nonces must be refreshed after a reconnect or boot stage that changes them.
type SigningDevice struct {
	ECID, BoardID, ChipID                  uint64
	APNonce, SEPNonce                      []byte
	ProductionMode, SecurityMode, InRomDFU bool
	// Nil means the device has not reported a demotion policy.
	DemotionPolicy *bool
}

// SignAP requests an AP IMG4 ticket for identity and checks its assertions for
// components against device and the selected signing build. It uses Apple's
// HTTPS TSS endpoint and rejects redirects off that HTTPS host. A supplied
// client's transport must preserve trusted TLS. This does not verify the ticket
// signature or authorize a restore.
// Separate local-policy, recovery-root, baseband and coprocessor ticket flows
// are not part of this AP request.
func SignAP(ctx context.Context, client *http.Client, identity map[string]any, device SigningDevice, components []string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request, want, err := APSigningRequest(identity, device, components)
	if err != nil {
		return nil, err
	}
	response, err := iosrestore.Tickets(ctx, apSigningClient(client), "", request)
	if err != nil {
		return nil, err
	}
	ticket, ok := response["ApImg4Ticket"].([]byte)
	if !ok || len(ticket) == 0 {
		return nil, fmt.Errorf("signing response has no ApImg4Ticket data")
	}
	if err := MatchTicket(ticket, want); err != nil {
		return nil, fmt.Errorf("match signing response: %w", err)
	}
	return response, nil
}

func apSigningClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	copy := *client
	copy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if client.CheckRedirect != nil {
			if err := client.CheckRedirect(req, via); err != nil {
				return err
			}
		}
		if req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Hostname(), "gs.apple.com") || (req.URL.Port() != "" && req.URL.Port() != "443") {
			return fmt.Errorf("signing redirect leaves trusted HTTPS endpoint")
		}
		if len(via) >= 10 {
			return fmt.Errorf("too many signing redirects")
		}
		return nil
	}
	return &copy
}

// APSigningRequest derives an AP IMG4 request and ticket requirements from one
// selected BuildIdentity. Components lists the images needed for the next stage;
// each must have a known ticket tag and a nonempty signing digest. The request
// includes all eligible AP entries. Inputs and returned values are independent.
func APSigningRequest(identity map[string]any, device SigningDevice, components []string) (map[string]any, TicketRequirements, error) {
	var want TicketRequirements
	if device.ECID == 0 || device.ChipID == 0 || len(device.APNonce) == 0 || len(components) == 0 {
		return nil, want, fmt.Errorf("signing requires observed ECID, chip ID, AP nonce and components")
	}
	// Copy through plist so nested dictionaries, arrays and byte values cannot
	// retain aliases to the selected manifest or caller's later mutations.
	encoded, err := plist.Marshal(identity, plist.FormatXML)
	if err != nil {
		return nil, want, fmt.Errorf("copy build identity: %w", err)
	}
	value, err := plist.ParseBytes(encoded)
	if err != nil {
		return nil, want, err
	}
	build, ok := value.(map[string]any)
	if !ok {
		return nil, want, fmt.Errorf("build identity is not a dictionary")
	}
	numbers := make(map[string]uint64)
	for _, key := range []string{"ApBoardID", "ApChipID", "ApSecurityDomain"} {
		n, err := manifestNumber(build[key])
		if err != nil {
			return nil, want, fmt.Errorf("%s: %w", key, err)
		}
		numbers[key] = n
	}
	if numbers["ApBoardID"] != device.BoardID || numbers["ApChipID"] != device.ChipID {
		return nil, want, fmt.Errorf("signing build does not match observed board and chip IDs")
	}
	buildID, ok := build["UniqueBuildID"].([]byte)
	if !ok || len(buildID) == 0 {
		return nil, want, fmt.Errorf("UniqueBuildID must contain data")
	}
	manifest, ok := build["Manifest"].(map[string]any)
	if !ok {
		return nil, want, fmt.Errorf("build Manifest is not a dictionary")
	}
	info, ok := build["Info"].(map[string]any)
	if !ok {
		return nil, want, fmt.Errorf("build Info is not a dictionary")
	}
	var uuid [16]byte
	if _, err := rand.Read(uuid[:]); err != nil {
		return nil, want, err
	}
	uuid[6] = uuid[6]&15 | 64
	uuid[8] = uuid[8]&63 | 128
	request := map[string]any{
		"@HostPlatformInfo": "mac", "@VersionInfo": "libauthinstall-1104.0.9",
		"@UUID":         fmt.Sprintf("%X-%X-%X-%X-%X", uuid[:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:]),
		"@ApImg4Ticket": true, "@BBTicket": true,
		"ApECID": device.ECID, "ApBoardID": device.BoardID, "ApChipID": device.ChipID,
		"ApSecurityDomain": numbers["ApSecurityDomain"], "UniqueBuildID": buildID,
		"ApNonce": slices.Clone(device.APNonce), "ApProductionMode": device.ProductionMode, "ApSecurityMode": device.SecurityMode,
	}
	if len(device.SEPNonce) > 0 {
		request["SepNonce"] = slices.Clone(device.SEPNonce)
	}
	for _, key := range []string{"Ap,OSLongVersion", "Ap,OSReleaseType", "Ap,ProductType", "Ap,SDKPlatform", "Ap,SikaFuse", "Ap,Target", "Ap,TargetType", "Ap,ProductMarketingVersion", "PearlCertificationRootPub", "NeRDEpoch"} {
		if v, ok := build[key]; ok {
			if s, ok := v.(string); ok && strings.HasPrefix(s, "0x") {
				n, err := manifestNumber(s)
				if err != nil {
					return nil, want, fmt.Errorf("%s: %w", key, err)
				}
				v = n
			}
			request[key] = v
		}
	}
	if _, ok := request["NeRDEpoch"]; ok {
		request["PermitNeRDPivot"] = []byte{}
	}
	if v, ok := info["RequiresUIDMode"]; ok {
		required, ok := v.(bool)
		if !ok {
			return nil, want, fmt.Errorf("RequiresUIDMode is not a boolean")
		}
		if required {
			request["UID_MODE"] = false
			request["Ap,SikaFuse"] = uint64(0)
		}
	}
	parameters := map[string]bool{"ApRawProductionMode": device.ProductionMode, "ApCurrentProductionMode": device.ProductionMode, "ApRawSecurityMode": device.SecurityMode, "ApRequiresImage4": true, "ApInRomDFU": device.InRomDFU}
	if device.DemotionPolicy != nil {
		parameters["ApDemotionPolicyOverride"] = *device.DemotionPolicy
	}
	for name, value := range manifest {
		switch name {
		case "BasebandFirmware", "SE,UpdatePayload", "BaseSystem", "Diags", "Ap,ExclaveOS":
			continue
		}
		if strings.HasPrefix(name, "Cryptex1,") {
			continue
		}
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, want, fmt.Errorf("component %s is not a dictionary", name)
		}
		raw, ok := entry["Info"]
		if !ok {
			continue
		}
		componentInfo, ok := raw.(map[string]any)
		if !ok {
			return nil, want, fmt.Errorf("component %s Info is not a dictionary", name)
		}
		trusted := false
		if value, present := entry["Trusted"]; present {
			var valid bool
			trusted, valid = value.(bool)
			if !valid {
				return nil, want, fmt.Errorf("component %s Trusted is not a boolean", name)
			}
		}
		rules, present := componentInfo["RestoreRequestRules"]
		if !present && !trusted {
			continue
		}
		if v, ok := componentInfo["IsFTAB"]; ok {
			isFTAB, ok := v.(bool)
			if !ok {
				return nil, want, fmt.Errorf("component %s IsFTAB is not a boolean", name)
			}
			if isFTAB {
				continue
			}
		}
		entry = maps.Clone(entry)
		delete(entry, "Info")
		_, hasDigest := entry["Digest"]
		if present {
			if err := applySigningRules(entry, parameters, rules); err != nil {
				return nil, want, fmt.Errorf("component %s: %w", name, err)
			}
		} else {
			entry["EPRO"] = device.ProductionMode
			entry["ESEC"] = device.SecurityMode
		}
		if trusted && !hasDigest {
			entry["Digest"] = []byte{}
		}
		if len(entry) == 0 {
			continue
		}
		if _, exists := request[name]; exists || strings.HasPrefix(name, "@") {
			return nil, want, fmt.Errorf("component %s collides with signing parameters", name)
		}
		request[name] = entry
	}
	want = TicketRequirements{ECID: device.ECID, BoardID: device.BoardID, ChipID: device.ChipID, APNonce: slices.Clone(device.APNonce), SEPNonce: slices.Clone(device.SEPNonce), ImageDigests: make(map[string][]byte)}
	domain := numbers["ApSecurityDomain"]
	want.SecurityDomain = &domain
	want.ProductionMode = &device.ProductionMode
	want.SecurityMode = &device.SecurityMode
	for _, name := range components {
		entry, ok := request[name].(map[string]any)
		if !ok {
			return nil, TicketRequirements{}, fmt.Errorf("component %s is not eligible for AP signing", name)
		}
		tag := componentFourCC[name]
		original, ok := manifest[name].(map[string]any)
		if !ok {
			return nil, TicketRequirements{}, fmt.Errorf("component %s has no manifest entry", name)
		}
		componentInfo, ok := original["Info"].(map[string]any)
		if !ok {
			return nil, TicketRequirements{}, fmt.Errorf("component %s has no manifest Info", name)
		}
		if value, ok := componentInfo["Img4PayloadType"]; ok {
			var valid bool
			tag, valid = value.(string)
			if !valid {
				return nil, TicketRequirements{}, fmt.Errorf("component %s Img4PayloadType is not a string", name)
			}
		}
		if len(tag) != 4 || strings.IndexFunc(tag, func(r rune) bool { return r > 127 }) >= 0 {
			return nil, TicketRequirements{}, fmt.Errorf("component %s has no valid ticket tag", name)
		}
		digest, ok := entry["Digest"].([]byte)
		if !ok || len(digest) == 0 {
			return nil, TicketRequirements{}, fmt.Errorf("component %s has no signing digest", name)
		}
		if _, ok := want.ImageDigests[tag]; ok {
			return nil, TicketRequirements{}, fmt.Errorf("duplicate signing component tag %s", tag)
		}
		want.ImageDigests[tag] = slices.Clone(digest)
	}
	return request, want, nil
}

func manifestNumber(value any) (uint64, error) {
	if s, ok := value.(string); ok {
		if !strings.HasPrefix(s, "0x") {
			return 0, fmt.Errorf("expected hexadecimal manifest integer")
		}
		n, err := strconv.ParseUint(s[2:], 16, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid manifest integer: %w", err)
		}
		return n, nil
	}
	switch n := value.(type) {
	case uint64:
		return n, nil
	case int64:
		if n >= 0 {
			return uint64(n), nil
		}
	case int:
		if n >= 0 {
			return uint64(n), nil
		}
	}
	return 0, fmt.Errorf("expected nonnegative manifest integer")
}

func applySigningRules(entry map[string]any, parameters map[string]bool, value any) error {
	rules, ok := value.([]any)
	if !ok {
		return fmt.Errorf("RestoreRequestRules is not an array")
	}
	for i, value := range rules {
		rule, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("restore rule %d is not a dictionary", i)
		}
		conditions, ok := rule["Conditions"].(map[string]any)
		if !ok {
			return fmt.Errorf("restore rule %d has no Conditions dictionary", i)
		}
		actions, ok := rule["Actions"].(map[string]any)
		if !ok {
			return fmt.Errorf("restore rule %d has no Actions dictionary", i)
		}
		matches := true
		for key, value := range conditions {
			expected, ok := value.(bool)
			if !ok {
				return fmt.Errorf("restore condition %s is not a boolean", key)
			}
			switch key {
			case "ApRawProductionMode", "ApCurrentProductionMode", "ApRawSecurityMode", "ApRequiresImage4", "ApDemotionPolicyOverride", "ApInRomDFU":
			default:
				return fmt.Errorf("unsupported restore condition %s", key)
			}
			actual, present := parameters[key]
			if !present || actual != expected {
				matches = false
			}
		}
		for key, value := range actions {
			if b, ok := value.(bool); ok {
				if matches {
					entry[key] = b
				}
				continue
			}
			// The Python reference ignores the integer sentinel 255.
			sentinel := false
			switch n := value.(type) {
			case uint64:
				sentinel = n == 255
			case int64:
				sentinel = n == 255
			case int:
				sentinel = n == 255
			}
			if sentinel {
				continue
			}
			return fmt.Errorf("restore action %s is not a boolean or 255", key)
		}
	}
	return nil
}
