package restore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/tmc/apple/x/plist"
)

func recoverySigningFixture() (map[string]any, SigningDevice) {
	build, device := signingFixture()
	build["Manifest"].(map[string]any)["OS"] = map[string]any{
		"Trusted": true, "Digest": []byte{11, 12}, "Info": map[string]any{"Path": "OS.dmg"},
	}
	return build, device
}

func TestRecoverySigningSelection(t *testing.T) {
	build, device := recoverySigningFixture()
	manifest := build["Manifest"].(map[string]any)
	for _, name := range []string{
		"BasebandFirmware", "SE,UpdatePayload", "BaseSystem", "ANS", "Ap,AudioBootChime",
		"Ap,CIO", "Ap,RestoreCIO", "Ap,RestoreTMU", "Ap,TMU", "Ap,rOSLogo1", "Ap,rOSLogo2",
		"AppleLogo", "DCP", "LLB", "RecoveryMode", "RestoreANS", "RestoreDCP", "RestoreDeviceTree",
		"RestoreKernelCache", "RestoreLogo", "RestoreRamDisk", "RestoreSEP", "SEP", "ftap", "ftsp",
		"iBEC", "iBSS", "rfta", "rfts", "Diags",
	} {
		manifest[name] = map[string]any{"Trusted": true, "Digest": []byte{99}, "Info": map[string]any{"Path": "skip"}}
	}
	for _, name := range []string{"Cryptex1,SystemOS", "Ap,ExclaveOS", "FutureFTAB", "Untrusted"} {
		manifest[name] = map[string]any{"Trusted": false, "Digest": []byte{22}, "Info": map[string]any{"Path": name, "IsFTAB": true}}
	}
	manifest["EmptyTrusted"] = map[string]any{"Trusted": true, "Info": map[string]any{"Path": "empty"}}
	manifest["NoInfo"] = map[string]any{"Trusted": true}
	manifest["EmptyInfo"] = map[string]any{"Trusted": true, "Info": map[string]any{}}
	before, _ := plist.Marshal(build, plist.FormatXML)
	request, want, err := RecoverySigningRequest(build, device, []string{"OS"})
	if err != nil {
		t.Fatal(err)
	}
	included := map[string]bool{"OS": true, "Cryptex1,SystemOS": true, "Ap,ExclaveOS": true, "FutureFTAB": true, "Untrusted": true, "EmptyTrusted": true}
	for name := range manifest {
		_, present := request[name]
		if present != included[name] {
			t.Errorf("component %s present=%v, want %v", name, present, included[name])
		}
	}
	if !reflect.DeepEqual(request["OS"], map[string]any{"Trusted": true, "Digest": []byte{11, 12}}) {
		t.Fatalf("unexpected recovery component properties: %#v", request["OS"])
	}
	if digest, ok := request["EmptyTrusted"].(map[string]any)["Digest"].([]byte); !ok || len(digest) != 0 {
		t.Fatal("missing trusted empty digest")
	}
	if !bytes.Equal(want.ImageDigests["OS\x00\x00"], []byte{11, 12}) || want.ECID != device.ECID {
		t.Fatalf("incorrect requirements: %#v", want)
	}
	request["OS"].(map[string]any)["Digest"].([]byte)[0] = 99
	request["ApNonce"].([]byte)[0] = 99
	after, _ := plist.Marshal(build, plist.FormatXML)
	if !bytes.Equal(before, after) || want.ImageDigests["OS\x00\x00"][0] != 11 || device.APNonce[0] != 7 {
		t.Fatal("aliased request inputs or requirements")
	}
	if _, _, err := RecoverySigningRequest(build, device, []string{"iBEC"}); err == nil {
		t.Fatal("accepted excluded next-stage component")
	}
}

func TestRecoverySigningRules(t *testing.T) {
	build, device := recoverySigningFixture()
	entry := build["Manifest"].(map[string]any)["OS"].(map[string]any)
	entry["Info"].(map[string]any)["RestoreRequestRules"] = []any{
		map[string]any{"Conditions": map[string]any{"ApRawProductionMode": false}, "Actions": map[string]any{"EPRO": false}},
	}
	request, _, err := RecoverySigningRequest(build, device, []string{"OS"})
	if err != nil {
		t.Fatal(err)
	}
	if request["OS"].(map[string]any)["EPRO"] != false {
		t.Fatal("false-valued recovery rule did not match")
	}
	entry["Info"].(map[string]any)["RestoreRequestRules"] = "invalid"
	if _, _, err := RecoverySigningRequest(build, device, []string{"OS"}); err == nil {
		t.Fatal("accepted malformed recovery rules")
	}
}

func TestSignRecovery(t *testing.T) {
	for _, name := range []string{"match", "wrong nonce", "wrong image", "missing ticket", "server failure"} {
		t.Run(name, func(t *testing.T) {
			build, device := recoverySigningFixture()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				value, err := plist.ParseBytes(data)
				if err != nil {
					t.Error(err)
					return
				}
				request := value.(map[string]any)
				if request["ApECID"] != device.ECID || request["OS"] == nil || request["iBSS"] != nil {
					t.Errorf("incorrect recovery request: %#v", request)
				}
				props := map[string]any{"ECID": device.ECID, "BORD": device.BoardID, "CHIP": device.ChipID,
					"BNCH": device.APNonce, "snon": device.SEPNonce, "SDOM": uint64(1), "CPRO": false, "CSEC": true}
				digests := map[string][]byte{"OS\x00\x00": {11, 12}}
				if name == "wrong nonce" {
					props["BNCH"] = []byte{99}
				}
				if name == "wrong image" {
					digests["OS\x00\x00"] = []byte{99}
				}
				response := map[string]any{"ApImg4Ticket": testTicket(props, digests)}
				if name == "missing ticket" {
					delete(response, "ApImg4Ticket")
				}
				if name == "server failure" {
					fmt.Fprint(w, "STATUS=94&MESSAGE=Ineligible")
					return
				}
				data, err = plist.Marshal(response, plist.FormatXML)
				if err != nil {
					t.Error(err)
					return
				}
				fmt.Fprint(w, "STATUS=0&MESSAGE=SUCCESS&REQUEST_STRING=")
				w.Write(data)
			}))
			defer server.Close()
			target, _ := url.Parse(server.URL)
			client := &http.Client{Transport: signingTransport{target, http.DefaultTransport}}
			response, err := SignRecovery(context.Background(), client, build, device, []string{"OS"})
			if name == "match" {
				if err != nil || response == nil {
					t.Fatalf("got %v, %v", response, err)
				}
			} else if err == nil || response != nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

func ExampleRecoverySigningRequest() {
	build, device := recoverySigningFixture()
	request, want, err := RecoverySigningRequest(build, device, []string{"OS"})
	fmt.Println(request["@ApImg4Ticket"], len(want.ImageDigests), err)
	// Output: true 1 <nil>
}

func ExampleSignRecovery() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := SignRecovery(ctx, nil, nil, SigningDevice{}, nil)
	fmt.Println(err)
	// Output: context canceled
}
