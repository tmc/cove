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
	"strings"
	"testing"

	"github.com/tmc/apple/x/plist"
)

func signingFixture() (map[string]any, SigningDevice) {
	build := map[string]any{
		"ApBoardID": "0x90", "ApChipID": "0xFE01", "ApSecurityDomain": "0x01", "UniqueBuildID": []byte{1, 2, 3},
		"Info": map[string]any{"RequiresUIDMode": true}, "Ap,SikaFuse": "0x01", "NeRDEpoch": "0x02",
		"Manifest": map[string]any{"iBSS": map[string]any{"Trusted": true, "Digest": []byte{4, 5, 6}, "Info": map[string]any{"Path": "Firmware/iBSS.im4p", "RestoreRequestRules": []any{
			map[string]any{"Conditions": map[string]any{"ApRawProductionMode": false, "ApRequiresImage4": true}, "Actions": map[string]any{"EPRO": false, "ESEC": true}},
		}}}},
	}
	device := SigningDevice{ECID: 1<<63 | 17, BoardID: 0x90, ChipID: 0xfe01, APNonce: []byte{7, 8}, SEPNonce: []byte{9, 10}, SecurityMode: true, InRomDFU: true}
	return build, device
}
func TestAPSigningRequest(t *testing.T) {
	build, device := signingFixture()
	before, _ := plist.Marshal(build, plist.FormatXML)
	request, want, err := APSigningRequest(build, device, []string{"iBSS"})
	if err != nil {
		t.Fatal(err)
	}
	if request["ApECID"] != device.ECID || request["ApBoardID"] != device.BoardID || request["ApChipID"] != device.ChipID {
		t.Fatal("incorrect device identity")
	}
	if request["ApSecurityDomain"] != uint64(1) || request["NeRDEpoch"] != uint64(2) || request["UID_MODE"] != false || request["Ap,SikaFuse"] != uint64(0) {
		t.Fatalf("incorrect manifest policy: %#v", request)
	}
	if _, ok := request["ApSepNonce"]; ok {
		t.Fatal("untranslated SEP nonce")
	}
	if !bytes.Equal(request["SepNonce"].([]byte), device.SEPNonce) {
		t.Fatal("incorrect SEP nonce")
	}
	entry := request["iBSS"].(map[string]any)
	expected := map[string]any{"Trusted": true, "Digest": []byte{4, 5, 6}, "EPRO": false, "ESEC": true}
	if !reflect.DeepEqual(entry, expected) {
		t.Fatalf("entry %#v", entry)
	}
	if *want.SecurityDomain != 1 || *want.ProductionMode || !*want.SecurityMode {
		t.Fatal("missing ticket policy requirements")
	}
	if uuid, ok := request["@UUID"].(string); !ok || len(uuid) != 36 || uuid[14] != '4' || !strings.ContainsRune("89AB", rune(uuid[19])) {
		t.Fatalf("invalid UUID %v", request["@UUID"])
	}
	after, _ := plist.Marshal(build, plist.FormatXML)
	if !bytes.Equal(before, after) {
		t.Fatal("mutated input build")
	}
	entry["Digest"].([]byte)[0] = 99
	request["ApNonce"].([]byte)[0] = 99
	if want.ImageDigests["ibss"][0] != 4 || want.APNonce[0] != 7 || device.APNonce[0] != 7 {
		t.Fatal("request aliases requirements/device")
	}
	after, _ = plist.Marshal(build, plist.FormatXML)
	if !bytes.Equal(before, after) {
		t.Fatal("request aliases build")
	}
}
func TestAPSigningSelection(t *testing.T) {
	build, device := signingFixture()
	manifest := build["Manifest"].(map[string]any)
	entry := func(info map[string]any) map[string]any { return map[string]any{"Trusted": true, "Info": info} }
	rules := map[string]any{"RestoreRequestRules": []any{}}
	for _, name := range []string{"BasebandFirmware", "SE,UpdatePayload", "BaseSystem", "Diags", "Ap,ExclaveOS", "Cryptex1,SystemOS"} {
		manifest[name] = entry(rules)
	}
	manifest["NoRules"] = map[string]any{"Trusted": false, "Info": map[string]any{}}
	manifest["NoInfo"] = map[string]any{"Digest": []byte{1}}
	manifest["FTAB"] = entry(map[string]any{"RestoreRequestRules": []any{}, "IsFTAB": true})
	manifest["ftap"] = entry(rules)
	manifest["FutureFirmware"] = map[string]any{"Digest": []byte{2}, "Info": map[string]any{"RestoreRequestRules": []any{}, "Img4PayloadType": "futr"}}
	request, want, err := APSigningRequest(build, device, []string{"iBSS", "FutureFirmware"})
	if err != nil {
		t.Fatal(err)
	}
	for name := range manifest {
		if name != "iBSS" && name != "ftap" && name != "FutureFirmware" {
			if _, ok := request[name]; ok {
				t.Errorf("included %s", name)
			}
		}
	}
	if digest, ok := request["ftap"].(map[string]any)["Digest"].([]byte); !ok || len(digest) != 0 {
		t.Fatal("missing empty trusted digest")
	}
	if !bytes.Equal(want.ImageDigests["futr"], []byte{2}) {
		t.Fatal("manifest tag not used")
	}
	if _, _, err := APSigningRequest(build, device, []string{"ftap"}); err == nil {
		t.Fatal("accepted empty next-stage digest")
	}
}
func TestAPSigningReject(t *testing.T) {
	for _, tt := range []struct {
		name       string
		change     func(map[string]any, *SigningDevice)
		components []string
	}{
		{name: "wrong board", change: func(b map[string]any, _ *SigningDevice) { b["ApBoardID"] = "0x91" }},
		{name: "wrong chip", change: func(b map[string]any, _ *SigningDevice) { b["ApChipID"] = "0x12" }},
		{name: "invalid integer", change: func(b map[string]any, _ *SigningDevice) { b["ApChipID"] = "0xzz" }},
		{name: "negative integer", change: func(b map[string]any, _ *SigningDevice) { b["ApChipID"] = int64(-1) }},
		{name: "missing security domain", change: func(b map[string]any, _ *SigningDevice) { delete(b, "ApSecurityDomain") }},
		{name: "missing unique build", change: func(b map[string]any, _ *SigningDevice) { delete(b, "UniqueBuildID") }},
		{name: "missing observed ECID", change: func(_ map[string]any, d *SigningDevice) { d.ECID = 0 }},
		{name: "missing observed nonce", change: func(_ map[string]any, d *SigningDevice) { d.APNonce = nil }},
		{name: "duplicate component", components: []string{"iBSS", "iBSS"}},
		{name: "missing component", components: []string{"iBEC"}},
		{name: "bad UID flag", change: func(b map[string]any, _ *SigningDevice) { b["Info"].(map[string]any)["RequiresUIDMode"] = "yes" }},
		{name: "reserved name", change: func(b map[string]any, _ *SigningDevice) { m := b["Manifest"].(map[string]any); m["ApECID"] = m["iBSS"] }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			build, device := signingFixture()
			if tt.change != nil {
				tt.change(build, &device)
			}
			components := tt.components
			if components == nil {
				components = []string{"iBSS"}
			}
			if _, _, err := APSigningRequest(build, device, components); err == nil {
				t.Fatal("accepted invalid signing inputs")
			}
		})
	}
}
func TestSigningRules(t *testing.T) {
	for _, tt := range []struct {
		name                string
		conditions, actions map[string]any
		parameters          map[string]bool
		want                map[string]any
		bad                 bool
	}{
		{name: "false equality", conditions: map[string]any{"ApRawProductionMode": false}, actions: map[string]any{"EPRO": false}, parameters: map[string]bool{"ApRawProductionMode": false}, want: map[string]any{"EPRO": false}},
		{name: "mismatch", conditions: map[string]any{"ApRawProductionMode": true}, actions: map[string]any{"EPRO": true}, parameters: map[string]bool{"ApRawProductionMode": false}, want: map[string]any{}},
		{name: "sentinel", conditions: map[string]any{}, actions: map[string]any{"EPRO": int64(255)}, want: map[string]any{}},
		{name: "unknown condition", conditions: map[string]any{"Future": true}, actions: map[string]any{}, bad: true},
		{name: "integer condition", conditions: map[string]any{"ApInRomDFU": 1}, actions: map[string]any{}, bad: true},
		{name: "bad action", conditions: map[string]any{}, actions: map[string]any{"EPRO": "true"}, bad: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entry := make(map[string]any)
			err := applySigningRules(entry, tt.parameters, []any{map[string]any{"Conditions": tt.conditions, "Actions": tt.actions}})
			if tt.bad {
				if err == nil {
					t.Fatal("accepted invalid rule")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(entry, tt.want) {
				t.Fatalf("got %#v, %v", entry, err)
			}
		})
	}
}

type signingTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (s signingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != "gs.apple.com" || r.URL.Path != "/TSS/controller" {
		return nil, fmt.Errorf("unexpected signing URL %s", r.URL)
	}
	r = r.Clone(r.Context())
	r.URL.Scheme = s.target.Scheme
	r.URL.Host = s.target.Host
	return s.base.RoundTrip(r)
}
func TestSignAP(t *testing.T) {
	for _, name := range []string{"match", "nonce changed", "wrong policy", "missing ticket", "server failure"} {
		t.Run(name, func(t *testing.T) {
			build, device := signingFixture()
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
				if request["ApECID"] != device.ECID || request["ApProductionMode"] != false {
					t.Errorf("incorrect on-wire request %#v", request)
				}
				props := map[string]any{"ECID": device.ECID, "BORD": device.BoardID, "CHIP": device.ChipID, "BNCH": device.APNonce, "snon": device.SEPNonce, "SDOM": uint64(1), "CPRO": false, "CSEC": true}
				if name == "nonce changed" {
					props["BNCH"] = []byte{99}
				}
				if name == "wrong policy" {
					props["CPRO"] = true
				}
				response := map[string]any{"ApImg4Ticket": testTicket(props, map[string][]byte{"ibss": {4, 5, 6}})}
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
			response, err := SignAP(context.Background(), client, build, device, []string{"iBSS"})
			if name == "match" {
				if err != nil || len(response) == 0 {
					t.Fatalf("got %v, %v", response, err)
				}
			} else if err == nil || response != nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}
func ExampleAPSigningRequest() {
	build, device := signingFixture()
	request, requirements, err := APSigningRequest(build, device, []string{"iBSS"})
	fmt.Println(request["@ApImg4Ticket"], len(requirements.ImageDigests), err)
	// Output: true 1 <nil>
}
func ExampleSignAP() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	build, device := signingFixture()
	_, err := SignAP(ctx, nil, build, device, []string{"iBSS"})
	fmt.Println(err != nil)
	// Output: true
}

func TestAPSigningMetadataIsNotComponent(t *testing.T) {
	build, device := signingFixture()
	build["PearlCertificationRootPub"] = map[string]any{"Digest": []byte{1}}
	if _, _, err := APSigningRequest(build, device, []string{"PearlCertificationRootPub"}); err == nil {
		t.Fatal("treated metadata as component")
	}
}

func TestSigningRedirects(t *testing.T) {
	for _, tt := range []struct {
		url     string
		allowed bool
	}{
		{"https://gs.apple.com/TSS/controller?action=2", true},
		{"https://gs.apple.com:443/TSS/controller", true},
		{"http://gs.apple.com/TSS/controller", false},
		{"https://other.example/TSS/controller", false},
		{"https://gs.apple.com:8443/TSS/controller", false},
	} {
		t.Run(tt.url, func(t *testing.T) {
			called := false
			original := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { called = true; return nil }}
			client := apSigningClient(original)
			req, _ := http.NewRequest(http.MethodPost, tt.url, nil)
			err := client.CheckRedirect(req, nil)
			if (err == nil) != tt.allowed || !called {
				t.Fatalf("got %v, callback %v", err, called)
			}
			if err := original.CheckRedirect(req, nil); err != nil {
				t.Fatal("changed caller's redirect policy")
			}
		})
	}
	client := apSigningClient(nil)
	req, _ := http.NewRequest(http.MethodPost, "https://gs.apple.com", nil)
	if err := client.CheckRedirect(req, make([]*http.Request, 10)); err == nil {
		t.Fatal("no redirect limit")
	}
}

func TestAPSigningTrustedWithoutRules(t *testing.T) {
	build, device := signingFixture()
	manifest := build["Manifest"].(map[string]any)
	manifest["iBEC"] = map[string]any{"Trusted": true, "Digest": []byte{11, 12}, "Info": map[string]any{"Path": "iBEC.im4p"}}
	request, want, err := APSigningRequest(build, device, []string{"iBEC"})
	if err != nil {
		t.Fatal(err)
	}
	entry := request["iBEC"].(map[string]any)
	if entry["EPRO"] != false || entry["ESEC"] != true || !bytes.Equal(want.ImageDigests["ibec"], []byte{11, 12}) {
		t.Fatalf("got %#v, %#v", entry, want)
	}
	if _, ok := entry["Info"]; ok {
		t.Fatal("Info not removed")
	}
	manifest["iBEC"].(map[string]any)["Trusted"] = false
	if _, _, err := APSigningRequest(build, device, []string{"iBEC"}); err == nil {
		t.Fatal("included untrusted component without rules")
	}
}
