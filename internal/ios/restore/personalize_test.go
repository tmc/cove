package restore

import (
	"bytes"
	"encoding/asn1"
	"encoding/hex"
	"fmt"
	"reflect"
	"testing"
)

const syntheticPayload = "30131604494d345016046b726e6c16000403616263"
const syntheticTicket = "30121604494d344d020100310004037369673000"

func TestComponentOptions(t *testing.T) {
	for _, tt := range []struct {
		name, component       string
		info, parameters, tss map[string]any
		tag                   string
		properties            map[string]any
	}{
		{name: "kernel", component: "KernelCache", tag: "krnl"},
		{name: "restore kernel", component: "RestoreKernelCache", tag: "rkrn"},
		{name: "OS", component: "OS", tag: "OS\x00\x00"},
		{name: "unknown", component: "FutureFirmware"},
		{name: "SEP default", component: "SEP", info: map[string]any{"RequiresNonceSlot": true}, tag: "sepi", properties: map[string]any{"snid": uint64(2)}},
		{name: "stage1 default", component: "SepStage1", info: map[string]any{"RequiresNonceSlot": true}, properties: map[string]any{"snid": uint64(2)}},
		{name: "LLB default", component: "LLB", info: map[string]any{"RequiresNonceSlot": true}, tag: "illb", properties: map[string]any{"anid": uint64(0)}},
		{name: "manifest", component: "SEP", info: map[string]any{"RequiresNonceSlot": true, "SepNonceSlotID": int64(7)}, tag: "sepi", properties: map[string]any{"snid": uint64(7)}},
		{name: "observed zero", component: "SEP", info: map[string]any{"RequiresNonceSlot": true, "SepNonceSlotID": int64(7)}, parameters: map[string]any{"SepNonceSlotID": uint64(0)}, tag: "sepi", properties: map[string]any{"snid": uint64(0)}},
		{name: "observed high", component: "LLB", info: map[string]any{"RequiresNonceSlot": true, "ApNonceSlotID": int64(7)}, parameters: map[string]any{"ApNonceSlotID": uint64(1<<63 | 17)}, tag: "illb", properties: map[string]any{"anid": uint64(1<<63 | 17)}},
		{name: "disabled", component: "SEP", info: map[string]any{"RequiresNonceSlot": false, "SepNonceSlotID": uint64(3)}, tag: "sepi"},
		{name: "other component", component: "RestoreSEP", info: map[string]any{"RequiresNonceSlot": true}, tag: "rsep"},
		{name: "TBM", component: "LLB", info: map[string]any{"RequiresNonceSlot": true}, tss: map[string]any{"LLB-TBM": map[string]any{"BNCN": []byte{1, 2, 3, 4, 5, 6, 7, 8}, "TEST": true}}, tag: "illb", properties: map[string]any{"anid": uint64(0), "BNCN": []byte{8, 7, 6, 5, 4, 3, 2, 1}, "TEST": true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := componentOptions(tt.component, tt.tss, tt.info, tt.parameters)
			if err != nil {
				t.Fatal(err)
			}
			if got.FourCC != tt.tag || !reflect.DeepEqual(got.RestoreProperties, tt.properties) {
				t.Fatalf("got %#v", got)
			}
			again, err := componentOptions(tt.component, tt.tss, tt.info, tt.parameters)
			if err != nil || !reflect.DeepEqual(got, again) {
				t.Fatal("options changed caller state")
			}
		})
	}
}
func TestPersonalizeReject(t *testing.T) {
	payload, _ := hex.DecodeString(syntheticPayload)
	ticket, _ := hex.DecodeString(syntheticTicket)
	for _, tt := range []struct {
		name                         string
		info, parameters, properties map[string]any
	}{
		{name: "duplicate snid", info: map[string]any{"RequiresNonceSlot": true}, properties: map[string]any{"snid": int64(2)}},
		{name: "negative slot", info: map[string]any{"RequiresNonceSlot": true, "SepNonceSlotID": int64(-1)}},
		{name: "bad observed slot", info: map[string]any{"RequiresNonceSlot": true, "SepNonceSlotID": int64(2)}, parameters: map[string]any{"SepNonceSlotID": "0"}},
		{name: "bad flag", info: map[string]any{"RequiresNonceSlot": 1}},
		{name: "empty TBM", properties: map[string]any{}},
		{name: "short BNCN", properties: map[string]any{"BNCN": []byte{1}}},
		{name: "integer BNCN", properties: map[string]any{"BNCN": uint64(1)}},
		{name: "bad property", properties: map[string]any{"bad": int64(1)}},
		{name: "bad value", properties: map[string]any{"TEST": "value"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tss := map[string]any{"ApImg4Ticket": ticket}
			if tt.properties != nil {
				tss["SEP-TBM"] = tt.properties
			}
			if _, err := Personalize("SEP", payload, tss, tt.info, tt.parameters); err == nil {
				t.Fatal("accepted bad policy")
			}
		})
	}
	for _, tss := range []map[string]any{nil, {"ApImg4Ticket": "ticket"}, {"ApImg4Ticket": ticket, "SEP-TBM": true}} {
		if _, err := Personalize("SEP", payload, tss, nil, nil); err == nil {
			t.Fatal("accepted bad response")
		}
	}
}
func TestPersonalizeWire(t *testing.T) {
	payload, _ := hex.DecodeString(syntheticPayload)
	ticket, _ := hex.DecodeString(syntheticTicket)
	nonce := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	got, err := Personalize("OS", payload, map[string]any{"ApImg4Ticket": ticket, "OS-TBM": map[string]any{"BNCN": nonce}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := derFields(t, got)
	im4p := derFields(t, fields[1].FullBytes)
	if string(im4p[1].Bytes) != "OS\x00\x00" {
		t.Fatalf("type %q", im4p[1].Bytes)
	}
	if !bytes.Equal(fields[2].Bytes, ticket) || fields[2].Class != 2 || fields[2].Tag != 0 {
		t.Fatal("ticket changed")
	}
	if fields[3].Class != 2 || fields[3].Tag != 1 {
		t.Fatal("bad IM4R wrapper")
	}
	im4r := derFields(t, fields[3].Bytes)
	properties := derFields(t, im4r[1].FullBytes)
	property := derFields(t, properties[0].Bytes)
	if string(property[0].Bytes) != "BNCN" || !bytes.Equal(property[1].Bytes, []byte{8, 7, 6, 5, 4, 3, 2, 1}) {
		t.Fatalf("BNCN %x", property[1].Bytes)
	}
	if !bytes.Equal(nonce, []byte{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatal("mutated nonce")
	}
}
func derFields(t *testing.T, data []byte) []asn1.RawValue {
	t.Helper()
	var outer asn1.RawValue
	rest, err := asn1.Unmarshal(data, &outer)
	if err != nil || len(rest) != 0 {
		t.Fatalf("invalid DER: %v", err)
	}
	var fields []asn1.RawValue
	for data = outer.Bytes; len(data) > 0; {
		var field asn1.RawValue
		data, err = asn1.Unmarshal(data, &field)
		if err != nil {
			t.Fatal(err)
		}
		fields = append(fields, field)
	}
	return fields
}
func ExamplePersonalize() {
	payload, _ := hex.DecodeString(syntheticPayload)
	ticket, _ := hex.DecodeString(syntheticTicket)
	image, err := Personalize("KernelCache", payload, map[string]any{"ApImg4Ticket": ticket}, nil, nil)
	fmt.Println(len(image), err)
	// Output: 51 <nil>
}
