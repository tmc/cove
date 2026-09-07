package restore

import (
	"bytes"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"testing"

	"github.com/tmc/apple/x/plist"
)

func nextPolicyTicket(device SigningDevice) []byte {
	return testTicket(map[string]any{
		"ECID": device.ECID, "BORD": device.BoardID, "CHIP": device.ChipID,
		"SDOM": uint64(1), "CPRO": device.ProductionMode, "CSEC": device.SecurityMode,
		"BNCH": device.APNonce, "snon": device.SEPNonce,
	}, map[string][]byte{"ibss": {4, 5, 6}})
}

func policyTicket(request map[string]any, change func(map[string]any, map[string][]byte)) []byte {
	props := map[string]any{"ECID": request["ApECID"], "BORD": request["ApBoardID"], "CHIP": request["ApChipID"],
		"SDOM": request["ApSecurityDomain"], "CPRO": request["ApProductionMode"], "CSEC": request["ApSecurityMode"],
		"lobo": request["Ap,LocalBoot"], "nsih": request["Ap,NextStageIM4MHash"]}
	for tag, key := range map[string]string{"ronh": "Ap,RecoveryOSPolicyNonceHash", "vuid": "Ap,VolumeUUID"} {
		if value, ok := request[key]; ok {
			props[tag] = value
		}
	}
	digests := map[string][]byte{"lpol": request["Ap,LocalPolicy"].(map[string]any)["Digest"].([]byte)}
	if change != nil {
		change(props, digests)
	}
	return testTicket(props, digests)
}

func TestLocalPolicySigningRequest(t *testing.T) {
	build, device := signingFixture()
	ticket := nextPolicyTicket(device)
	request, err := LocalPolicySigningRequest(build, device, ticket, []string{"iBSS"})
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(request))
	for key := range request {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	wantKeys := []string{"@ApImg4Ticket", "@HostPlatformInfo", "@UUID", "@VersionInfo", "Ap,LocalBoot", "Ap,LocalPolicy", "Ap,NextStageIM4MHash", "ApBoardID", "ApChipID", "ApECID", "ApNonce", "ApProductionMode", "ApSecurityDomain", "ApSecurityMode"}
	slices.Sort(wantKeys)
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("request keys %v", keys)
	}
	if request["ApECID"] != device.ECID || request["Ap,LocalBoot"] != false || request["ApProductionMode"] != false {
		t.Fatal("incorrect device or local boot policy")
	}
	payload := EmptyLocalPolicy()
	if hex.EncodeToString(payload) != "30141604494d345016046c706f6c1603312e30040100" {
		t.Fatal("incorrect empty IM4P")
	}
	policyHash, nextHash := sha512.Sum384(payload), sha512.Sum384(ticket)
	if !bytes.Equal(request["Ap,LocalPolicy"].(map[string]any)["Digest"].([]byte), policyHash[:]) || !bytes.Equal(request["Ap,NextStageIM4MHash"].([]byte), nextHash[:]) {
		t.Fatal("incorrect SHA-384 binding")
	}
	payload[0] = 0
	request["ApNonce"].([]byte)[0] = 0
	request["Ap,NextStageIM4MHash"].([]byte)[0] ^= 1
	if EmptyLocalPolicy()[0] != 0x30 || device.APNonce[0] != 7 || !bytes.Equal(ticket, nextPolicyTicket(device)) {
		t.Fatal("request or payload aliases caller inputs")
	}
}

func TestLocalPolicyRejectsStaleAPTicket(t *testing.T) {
	build, device := signingFixture()
	ticket := nextPolicyTicket(device)
	device.APNonce = []byte{99}
	if _, err := LocalPolicySigningRequest(build, device, ticket, []string{"iBSS"}); err == nil {
		t.Fatal("accepted stale AP ticket")
	}
}

func TestMatchLocalPolicy(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(map[string]any, map[string][]byte)
		ok     bool
	}{
		{name: "no nonce properties", ok: true},
		{name: "matching optional nonces", ok: true, change: func(p map[string]any, _ map[string][]byte) { p["BNCH"] = []byte{7, 8}; p["snon"] = []byte{9, 10} }},
		{name: "wrong AP nonce", change: func(p map[string]any, _ map[string][]byte) { p["BNCH"] = []byte{99} }},
		{name: "wrong SEP nonce", change: func(p map[string]any, _ map[string][]byte) { p["snon"] = []byte{99} }},
		{name: "wrong next stage", change: func(p map[string]any, _ map[string][]byte) { p["nsih"] = make([]byte, 48) }},
		{name: "missing next stage", change: func(p map[string]any, _ map[string][]byte) { delete(p, "nsih") }},
		{name: "wrong policy", change: func(_ map[string]any, d map[string][]byte) { d["lpol"] = []byte{99} }},
		{name: "wrong device", change: func(p map[string]any, _ map[string][]byte) { p["ECID"] = uint64(1) }},
		{name: "wrong device type", change: func(p map[string]any, _ map[string][]byte) { p["ECID"] = []byte{1} }},
		{name: "wrong local boot", change: func(p map[string]any, _ map[string][]byte) { p["lobo"] = true }},
		{name: "missing local boot", change: func(p map[string]any, _ map[string][]byte) { delete(p, "lobo") }},
		{name: "wrong security", change: func(p map[string]any, _ map[string][]byte) { p["CSEC"] = false }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			build, device := signingFixture()
			request, err := LocalPolicySigningRequest(build, device, nextPolicyTicket(device), []string{"iBSS"})
			if err != nil {
				t.Fatal(err)
			}
			err = matchLocalPolicy(policyTicket(request, tt.change), request, device.SEPNonce)
			if (err == nil) != tt.ok {
				t.Fatalf("got %v, want success %v", err, tt.ok)
			}
		})
	}
}

func localPolicyServer(t *testing.T, reject bool) *http.Client {
	t.Helper()
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
		if request["Ap,LocalPolicy"] == nil || request["iBSS"] != nil || request["SepNonce"] != nil || request["UniqueBuildID"] != nil {
			t.Errorf("incorrect local-policy wire request")
			w.WriteHeader(400)
			return
		}
		response := map[string]any{"ApImg4Ticket": policyTicket(request, func(p map[string]any, _ map[string][]byte) {
			if reject {
				p["nsih"] = []byte{99}
			}
		})}
		data, err = plist.Marshal(response, plist.FormatXML)
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Fprint(w, "STATUS=0&MESSAGE=SUCCESS&REQUEST_STRING=")
		w.Write(data)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	return &http.Client{Transport: signingTransport{target, http.DefaultTransport}}
}

func TestSignLocalPolicy(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			build, device := signingFixture()
			response, err := SignLocalPolicy(context.Background(), localPolicyServer(t, reject), build, device, nextPolicyTicket(device), []string{"iBSS"})
			if (err != nil) != reject || (response == nil) != reject {
				t.Fatalf("got %v, %v", response, err)
			}
		})
	}
}

func ExampleEmptyLocalPolicy() {
	fmt.Println(len(EmptyLocalPolicy()))
	// Output: 22
}

func ExampleLocalPolicySigningRequest() {
	build, device := signingFixture()
	request, err := LocalPolicySigningRequest(build, device, nextPolicyTicket(device), []string{"iBSS"})
	fmt.Println(request["Ap,LocalBoot"], err)
	// Output: false <nil>
}

func ExampleSignLocalPolicy() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := SignLocalPolicy(ctx, nil, nil, SigningDevice{}, nil, nil)
	fmt.Println(err)
	// Output: context canceled
}
