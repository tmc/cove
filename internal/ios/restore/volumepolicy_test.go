package restore

import (
	"bytes"
	"context"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/tmc/apple/x/iosrestore"
)

func volumePolicyArguments() map[string]any {
	return map[string]any{
		"Ap,NextStageIM4MHash":         bytes.Repeat([]byte{0x11}, 48),
		"Ap,RecoveryOSPolicyNonceHash": bytes.Repeat([]byte{0x22}, 48),
		"Ap,VolumeUUID":                "00112233-4455-6677-8899-AABBCCDDEEFF",
	}
}

func TestVolumePolicySigningRequest(t *testing.T) {
	build, device := signingFixture()
	arguments := volumePolicyArguments()
	request, err := VolumePolicySigningRequest(build, device, arguments)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(request))
	for key := range request {
		keys = append(keys, key)
	}
	wantKeys := []string{"@ApImg4Ticket", "@HostPlatformInfo", "@UUID", "@VersionInfo", "Ap,LocalBoot", "Ap,LocalPolicy", "Ap,NextStageIM4MHash", "Ap,RecoveryOSPolicyNonceHash", "Ap,VolumeUUID", "ApBoardID", "ApChipID", "ApECID", "ApProductionMode", "ApSecurityDomain", "ApSecurityMode"}
	slices.Sort(keys)
	slices.Sort(wantKeys)
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("request keys %v", keys)
	}
	if request["Ap,LocalBoot"] != true || request["ApECID"] != device.ECID || request["ApProductionMode"] != false || request["ApSecurityMode"] != true {
		t.Fatal("incorrect device or policy flags")
	}
	if hex.EncodeToString(request["Ap,VolumeUUID"].([]byte)) != "00112233445566778899aabbccddeeff" {
		t.Fatal("incorrect UUID wire bytes")
	}
	if !bytes.Equal(request["Ap,NextStageIM4MHash"].([]byte), arguments["Ap,NextStageIM4MHash"].([]byte)) || !bytes.Equal(request["Ap,RecoveryOSPolicyNonceHash"].([]byte), arguments["Ap,RecoveryOSPolicyNonceHash"].([]byte)) {
		t.Fatal("rehash or incorrect argument hash")
	}
	request["Ap,NextStageIM4MHash"].([]byte)[0] = 99
	request["Ap,RecoveryOSPolicyNonceHash"].([]byte)[0] = 99
	if arguments["Ap,NextStageIM4MHash"].([]byte)[0] != 0x11 || arguments["Ap,RecoveryOSPolicyNonceHash"].([]byte)[0] != 0x22 {
		t.Fatal("request aliases supplied hashes")
	}
	arguments["Ap,VolumeUUID"] = "00112233445566778899aabbccddeeff"
	device.APNonce, device.SEPNonce = nil, nil
	if _, err := VolumePolicySigningRequest(build, device, arguments); err != nil {
		t.Fatal(err)
	}
}

func TestVolumePolicyInvalidInput(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(map[string]any, *SigningDevice, map[string]any)
	}{
		{"no ECID", func(_ map[string]any, d *SigningDevice, _ map[string]any) { d.ECID = 0 }},
		{"wrong board", func(b map[string]any, _ *SigningDevice, _ map[string]any) { b["ApBoardID"] = "0x91" }},
		{"wrong chip", func(b map[string]any, _ *SigningDevice, _ map[string]any) { b["ApChipID"] = "0x1" }},
		{"missing domain", func(b map[string]any, _ *SigningDevice, _ map[string]any) { delete(b, "ApSecurityDomain") }},
		{"short next hash", func(_ map[string]any, _ *SigningDevice, a map[string]any) {
			a["Ap,NextStageIM4MHash"] = make([]byte, 47)
		}},
		{"long nonce hash", func(_ map[string]any, _ *SigningDevice, a map[string]any) {
			a["Ap,RecoveryOSPolicyNonceHash"] = make([]byte, 49)
		}},
		{"string hash", func(_ map[string]any, _ *SigningDevice, a map[string]any) { a["Ap,NextStageIM4MHash"] = "0011" }},
		{"missing nonce hash", func(_ map[string]any, _ *SigningDevice, a map[string]any) { delete(a, "Ap,RecoveryOSPolicyNonceHash") }},
		{"UUID bytes", func(_ map[string]any, _ *SigningDevice, a map[string]any) { a["Ap,VolumeUUID"] = make([]byte, 16) }},
		{"short UUID", func(_ map[string]any, _ *SigningDevice, a map[string]any) { a["Ap,VolumeUUID"] = "0011" }},
		{"UUID wrong hyphens", func(_ map[string]any, _ *SigningDevice, a map[string]any) {
			a["Ap,VolumeUUID"] = "0011223-34455-6677-8899-AABBCCDDEEFF"
		}},
		{"UUID invalid hex", func(_ map[string]any, _ *SigningDevice, a map[string]any) {
			a["Ap,VolumeUUID"] = "ZZ112233445566778899aabbccddeeff00"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			build, device := signingFixture()
			args := volumePolicyArguments()
			tt.change(build, &device, args)
			if _, err := VolumePolicySigningRequest(build, device, args); err == nil {
				t.Fatal("accepted invalid volume policy")
			}
		})
	}
}

func TestVolumePolicyAssertions(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(map[string]any, map[string][]byte)
	}{
		{"wrong volume", func(p map[string]any, _ map[string][]byte) { p["vuid"] = make([]byte, 16) }},
		{"missing volume", func(p map[string]any, _ map[string][]byte) { delete(p, "vuid") }},
		{"volume type", func(p map[string]any, _ map[string][]byte) { p["vuid"] = uint64(1) }},
		{"wrong nonce hash", func(p map[string]any, _ map[string][]byte) { p["ronh"] = make([]byte, 48) }},
		{"missing nonce hash", func(p map[string]any, _ map[string][]byte) { delete(p, "ronh") }},
		{"wrong local boot", func(p map[string]any, _ map[string][]byte) { p["lobo"] = false }},
		{"unobserved AP nonce", func(p map[string]any, _ map[string][]byte) { p["BNCH"] = []byte{1} }},
		{"unobserved SEP nonce", func(p map[string]any, _ map[string][]byte) { p["snon"] = []byte{1} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			build, device := signingFixture()
			request, err := VolumePolicySigningRequest(build, device, volumePolicyArguments())
			if err != nil {
				t.Fatal(err)
			}
			if err := matchLocalPolicy(policyTicket(request, tt.change), request, nil); err == nil {
				t.Fatal("accepted mismatched volume policy")
			}
		})
	}
}

func TestVolumePolicyResponse(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			build, device := signingFixture()
			arguments := volumePolicyArguments()
			response, err := VolumePolicyResponse(context.Background(), localPolicyServer(t, reject), build, device, arguments)
			if reject {
				if err == nil || response != nil {
					t.Fatal("accepted rejected volume policy")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var wire bytes.Buffer
			if err := iosrestore.Send(&wire, response); err != nil {
				t.Fatal(err)
			}
			decoded, err := iosrestore.Receive(&wire)
			if err != nil {
				t.Fatal(err)
			}
			if len(decoded) != 1 {
				t.Fatalf("unexpected response keys %#v", decoded)
			}
			data, ok := decoded["Ap,LocalPolicy"].([]byte)
			if !ok {
				t.Fatal("missing personalized policy")
			}
			var fields []asn1.RawValue
			rest, err := asn1.Unmarshal(data, &fields)
			if err != nil || len(rest) != 0 || len(fields) != 3 {
				t.Fatalf("invalid IMG4: %v", err)
			}
			request, err := VolumePolicySigningRequest(build, device, arguments)
			if err != nil {
				t.Fatal(err)
			}
			if string(fields[0].Bytes) != "IMG4" || !bytes.Equal(fields[1].FullBytes, EmptyLocalPolicy()) || fields[2].Class != 2 || fields[2].Tag != 0 || !bytes.Equal(fields[2].Bytes, policyTicket(request, nil)) {
				t.Fatal("incorrect policy payload or ticket on service wire")
			}
		})
	}
}

func ExampleVolumePolicySigningRequest() {
	build, device := signingFixture()
	request, err := VolumePolicySigningRequest(build, device, volumePolicyArguments())
	fmt.Println(request["Ap,LocalBoot"], err)
	// Output: true <nil>
}

func ExampleVolumePolicyResponse() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := VolumePolicyResponse(ctx, nil, nil, SigningDevice{}, nil)
	fmt.Println(err)
	// Output: context canceled
}
