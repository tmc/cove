package restore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/apple/x/iosrestore"
	"github.com/tmc/apple/x/plist"
	"github.com/tmc/cove/internal/ios/firmware"
)

func objectIdentity(behavior, variant, filename string) map[string]any {
	return map[string]any{
		"Info":     map[string]any{"DeviceClass": "vresearch101ap", "RestoreBehavior": behavior, "Variant": variant, "MacOSVariant": "Customer"},
		"Manifest": map[string]any{"OS": map[string]any{"Info": map[string]any{"Path": filename}, "Digest": []byte("original signing digest")}},
	}
}

func objectBundle(t *testing.T, identities ...map[string]any) *firmware.Bundle {
	t.Helper()
	list := make([]any, len(identities))
	for i := range identities {
		list[i] = identities[i]
	}
	manifest, err := plist.Marshal(map[string]any{"BuildIdentities": list}, plist.FormatXML)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	write := func(name string, data []byte) {
		t.Helper()
		filename := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	hashes := make(map[string]string)
	payload, _ := hex.DecodeString(syntheticPayload)
	for name, data := range map[string][]byte{
		"ibss.im4p":           payload,
		"BuildManifest.plist": manifest,
		"primary.dmg":         []byte("patched primary payload"), "recovery.dmg": []byte("recovery payload"), "override.dmg": []byte("TSS override payload"),
		"RestoreVersion.plist": []byte("restore version"), "SystemVersion.plist": []byte("system version"),
		"Firmware/Manifests/restore/Customer/apticket.vresearch101ap.im4m": []byte("primary global manifest"),
	} {
		name = "iphone_Restore/" + name
		write("attempt-test/work/"+name, data)
		hashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	write("attempt-test/records.json", []byte("[]"))
	write("attempt-test/patch.log", nil)
	receipt, err := json.Marshal(map[string]any{
		"schemaVersion": 1, "stage": "patched", "attempt": "attempt-test", "tree": "attempt-test/work",
		"patcher": map[string]any{"sourceCommit": firmware.SourceCommit}, "outputs": hashes,
		"recordsSHA256": fmt.Sprintf("%x", sha256.Sum256([]byte("[]"))), "logSHA256": fmt.Sprintf("%x", sha256.Sum256(nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	write("patched.json", receipt)
	b, err := firmware.OpenPatched(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func TestBundleObjectsSelection(t *testing.T) {
	for _, tt := range []struct {
		name, behavior, kind      string
		recovery, includeRecovery bool
		want                      string
	}{
		{"V3 primary", "Erase", "PersonalizedBootObjectV3", true, true, "primary.dmg"},
		{"V4 primary", "Erase", "SourceBootObjectV4", false, true, "primary.dmg"},
		{"V4 recovery", "Erase", "SourceBootObjectV4", true, true, "recovery.dmg"},
		{"build recovery", "Erase", "BuildIdentityDict", true, true, "recovery.dmg"},
		{"erase policy", "Erase", "RecoveryOSLocalPolicy", false, true, "recovery.dmg"},
		{"update policy", "Update", "RecoveryOSLocalPolicy", true, false, "primary.dmg"},
		{"absent recovery", "Erase", "SourceBootObjectV4", true, false, ""},
		{"absent policy", "Erase", "RecoveryOSLocalPolicy", false, false, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			variant := "Darwin Cloud Customer Erase Install (IPSW)"
			if tt.behavior == "Update" {
				variant = "Customer Upgrade Install (IPSW)"
			}
			identities := []map[string]any{objectIdentity(tt.behavior, variant, "primary.dmg")}
			if tt.includeRecovery {
				identities = append(identities, objectIdentity("Erase", "macOS Customer", "recovery.dmg"))
			}
			objects, err := NewBundleObjects(context.Background(), objectBundle(t, identities...), "vresearch101ap", tt.behavior, nil)
			if err != nil {
				t.Fatal(err)
			}
			message := map[string]any{"DataType": tt.kind, "Arguments": map[string]any{"IsRecoveryOS": tt.recovery}}
			got, err := objects.Identity(context.Background(), message)
			if tt.want == "" {
				if err == nil {
					t.Fatal("invented missing recovery identity")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			component := got["Manifest"].(map[string]any)["OS"].(map[string]any)
			if component["Info"].(map[string]any)["Path"] != tt.want {
				t.Fatal(got)
			}
			component["Digest"].([]byte)[0] = 0
			again, err := objects.Identity(context.Background(), message)
			if err != nil {
				t.Fatal(err)
			}
			if string(again["Manifest"].(map[string]any)["OS"].(map[string]any)["Digest"].([]byte)) != "original signing digest" {
				t.Fatal("identity alias changed signing digest")
			}
		})
	}
}

func TestBundleObjectsInvalidSelection(t *testing.T) {
	for _, tt := range []struct {
		name, behavior, model, variant string
		duplicate                      bool
		wantError                      bool
	}{
		{"missing erase", "Erase", "vresearch101ap", "Other", false, true},
		{"wrong model", "Erase", "other", "Erase Install (IPSW)", false, true},
		{"ambiguous", "Erase", "vresearch101ap", "Erase Install (IPSW)", true, true},
		{"update fallback", "Update", "vresearch101ap", "Other", false, false},
		{"invalid behavior", "Repair", "vresearch101ap", "Other", false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			identity := objectIdentity(tt.behavior, tt.variant, "primary.dmg")
			identities := []map[string]any{identity}
			if tt.duplicate {
				identities = append(identities, identity)
			}
			_, err := NewBundleObjects(context.Background(), objectBundle(t, identities...), tt.model, tt.behavior, nil)
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestBundleObjectsPaths(t *testing.T) {
	for _, tt := range []struct {
		name, image, manifestPath string
		override                  any
		want                      string
	}{
		{"manifest", "OS", "primary.dmg", nil, "patched primary payload"},
		{"override", "OS", "primary.dmg", "override.dmg", "TSS override payload"},
		{"escape", "OS", "../primary.dmg", nil, ""},
		{"override escape", "OS", "primary.dmg", "../override.dmg", ""},
		{"absolute", "OS", "/primary.dmg", nil, ""},
		{"backslash", "OS", "a\\primary.dmg", nil, ""},
		{"uncatalogued", "OS", "absent", nil, ""},
		{"bad override", "OS", "primary.dmg", true, ""},
		{"empty override", "OS", "primary.dmg", "", ""},
		{"unknown component", "absent", "primary.dmg", nil, ""},
		{"global", "__GlobalManifest__", "primary.dmg", nil, "primary global manifest"},
		{"restore version", "__RestoreVersion__", "primary.dmg", nil, "restore version"},
		{"system version", "__SystemVersion__", "primary.dmg", nil, "system version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := make(map[string]any)
			if tt.override != nil {
				response["OS"] = map[string]any{"Path": tt.override}
			}
			objects, err := NewBundleObjects(context.Background(), objectBundle(t, objectIdentity("Erase", "Erase Install (IPSW)", tt.manifestPath)), "vresearch101ap", "Erase", response)
			if err != nil {
				t.Fatal(err)
			}
			if entry, ok := response["OS"].(map[string]any); ok {
				entry["Path"] = "mutated"
			}
			// Metadata ignores recovery selection even when no recovery identity exists.
			message := map[string]any{"DataType": "PersonalizedBootObjectV3", "Arguments": map[string]any{"IsRecoveryOS": true}}
			f, err := objects.Object(context.Background(), message, tt.image)
			if tt.want == "" {
				if err == nil {
					f.Close()
					t.Fatal("accepted invalid object")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(f)
			f.Close()
			if err != nil || string(got) != tt.want {
				t.Fatalf("data = %q, error = %v", got, err)
			}
		})
	}
}

func TestBundleObjectsSession(t *testing.T) {
	b := objectBundle(t, objectIdentity("Erase", "Erase Install (IPSW)", "primary.dmg"), objectIdentity("Erase", "macOS Customer", "recovery.dmg"))
	objects, err := NewBundleObjects(context.Background(), b, "vresearch101ap", "Erase", nil)
	if err != nil {
		t.Fatal(err)
	}
	data := &SessionData{Identity: objects.Identity, Object: objects.Object}
	for _, tt := range []struct{ kind, name, want string }{
		{"SourceBootObjectV4", "OS", "recovery payload"},
		{"SourceBootObjectV4", "__GlobalManifest__", "primary global manifest"},
		{"BuildIdentityDict", "", "recovery.dmg"},
	} {
		t.Run(tt.kind+tt.name, func(t *testing.T) {
			host, peer := net.Pipe()
			defer host.Close()
			defer peer.Close()
			host.SetDeadline(time.Now().Add(3 * time.Second))
			peer.SetDeadline(time.Now().Add(3 * time.Second))
			done := make(chan error, 1)
			go func() {
				done <- data.Handle(context.Background(), host, map[string]any{"DataType": tt.kind, "Arguments": map[string]any{"IsRecoveryOS": true, "ImageName": tt.name}})
			}()
			m, err := iosrestore.Receive(peer)
			if err != nil {
				t.Fatal(err)
			}
			if tt.kind == "BuildIdentityDict" {
				identity := m["BuildIdentityDict"].(map[string]any)
				if identity["Manifest"].(map[string]any)["OS"].(map[string]any)["Info"].(map[string]any)["Path"] != tt.want {
					t.Fatal(m)
				}
			} else {
				if !bytes.Equal(m["FileData"].([]byte), []byte(tt.want)) {
					t.Fatal(m)
				}
				m, err = iosrestore.Receive(peer)
				if err != nil || m["FileDataDone"] != true {
					t.Fatalf("final = %v, error = %v", m, err)
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBundleObjectsPersonalized(t *testing.T) {
	build, device := signingFixture()
	info := build["Info"].(map[string]any)
	info["DeviceClass"], info["RestoreBehavior"], info["Variant"] = "vresearch101ap", "Erase", "Erase Install (IPSW)"
	build["Manifest"].(map[string]any)["iBSS"].(map[string]any)["Info"].(map[string]any)["Path"] = "ibss.im4p"
	response := map[string]any{"ApImg4Ticket": nextPolicyTicket(device)}
	objects, err := NewBundleObjects(context.Background(), objectBundle(t, build), "vresearch101ap", "Erase", response)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := hex.DecodeString(syntheticPayload)
	want, err := Personalize("iBSS", payload, response, info, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := &SessionData{Identity: objects.Identity, Object: objects.Object, APResponse: response, Device: device}
	host, peer := net.Pipe()
	defer host.Close()
	defer peer.Close()
	host.SetDeadline(time.Now().Add(3 * time.Second))
	peer.SetDeadline(time.Now().Add(3 * time.Second))
	done := make(chan error, 1)
	go func() {
		done <- data.Handle(context.Background(), host, map[string]any{"DataType": "PersonalizedBootObjectV3", "Arguments": map[string]any{"ImageName": "iBSS"}})
	}()
	m, err := iosrestore.Receive(peer)
	if err != nil || !bytes.Equal(m["FileData"].([]byte), want) {
		t.Fatalf("personalized response = %v, error = %v", m, err)
	}
	m, err = iosrestore.Receive(peer)
	if err != nil || m["FileDataDone"] != true {
		t.Fatalf("final response = %v, error = %v", m, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestBundleObjectsInvalidRequest(t *testing.T) {
	identity := objectIdentity("Erase", "Erase Install (IPSW)", "primary.dmg")
	identity["Info"].(map[string]any)["MacOSVariant"] = "../Customer"
	objects, err := NewBundleObjects(context.Background(), objectBundle(t, identity), "vresearch101ap", "Erase", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []map[string]any{
		{"DataType": "BuildIdentityDict"},
		{"DataType": "SourceBootObjectV4", "Arguments": map[string]any{"IsRecoveryOS": "true"}},
		{"DataType": "unknown"},
	} {
		if _, err := objects.Identity(context.Background(), message); err == nil {
			t.Fatalf("accepted request %v", message)
		}
	}
	if f, err := objects.Object(context.Background(), nil, "__GlobalManifest__"); err == nil {
		f.Close()
		t.Fatal("accepted invalid metadata path")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := objects.Identity(ctx, map[string]any{"DataType": "KernelCache"}); err == nil {
		t.Fatal("accepted canceled identity request")
	}
}

func TestBundleObjectsUpdateFallback(t *testing.T) {
	b := objectBundle(t, objectIdentity("Update", "Other Update", "primary.dmg"))
	objects, err := NewBundleObjects(context.Background(), b, "vresearch101ap", "Update", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"PersonalizedBootObjectV3", "SourceBootObjectV4", "BuildIdentityDict", "RecoveryOSLocalPolicy"} {
		_, err := objects.Identity(context.Background(), map[string]any{"DataType": kind, "Arguments": map[string]any{}})
		if (err == nil) != (kind == "PersonalizedBootObjectV3") {
			t.Fatalf("%s selection error = %v", kind, err)
		}
	}
}

func ExampleNewBundleObjects() {
	_, err := NewBundleObjects(context.Background(), nil, "vresearch101ap", "Erase", nil)
	fmt.Println(err)
	// Output: bundle, device class and Erase or Update behavior are required
}

func ExampleBundleObjects_Identity() {
	var objects BundleObjects
	_, err := objects.Identity(context.Background(), map[string]any{"DataType": "BuildIdentityDict"})
	fmt.Println(err)
	// Output: bundle object provider is not initialized
}

func ExampleBundleObjects_Object() {
	var objects BundleObjects
	_, err := objects.Object(context.Background(), nil, "OS")
	fmt.Println(err)
	// Output: bundle object provider is not initialized
}
