//go:build darwin

package restore

import (
	"context"
	"fmt"
	"testing"

	"github.com/tmc/apple/x/irecovery"
)

func TestSigningObservation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*irecovery.Info)
		fail   bool
	}{
		{name: "DFU"},
		{name: "recovery", change: func(i *irecovery.Info) { i.Device.Mode = "recovery" }},
		{name: "zero flags", change: func(i *irecovery.Info) { *i.CPFM = 0 }},
		{name: "wrong ECID", change: func(i *irecovery.Info) { i.Device.ECID = 7 }, fail: true},
		{name: "missing board", change: func(i *irecovery.Info) { i.BoardID = nil }, fail: true},
		{name: "missing flags", change: func(i *irecovery.Info) { i.CPFM = nil }, fail: true},
		{name: "missing boot flags", change: func(i *irecovery.Info) { i.IBFL = nil }, fail: true},
		{name: "not Image4", change: func(i *irecovery.Info) { *i.IBFL = 0 }, fail: true},
		{name: "missing nonce", change: func(i *irecovery.Info) { i.APNonce = nil }, fail: true},
		{name: "wrong mode", change: func(i *irecovery.Info) { i.Device.Mode = "unknown" }, fail: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			board, flags, ibfl := uint32(0), uint32(3), uint32(4)
			info := irecovery.Info{Device: irecovery.Device{ECID: 1<<63 | 17, CPID: 0xfe01, Mode: "dfu"}, BoardID: &board, CPFM: &flags, IBFL: &ibfl, APNonce: []byte{1, 2}, SEPNonce: []byte{3, 4}}
			if tt.change != nil {
				tt.change(&info)
			}
			got, err := signingObservation(info, 1<<63|17)
			if tt.fail {
				if err == nil {
					t.Fatal("accepted invalid observation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.BoardID != 0 || got.DemotionPolicy != nil || got.ProductionMode != (flags&2 != 0) || got.SecurityMode != (flags&1 != 0) || got.InRomDFU != (info.Device.Mode == "dfu") {
				t.Fatalf("got %#v", got)
			}
			got.APNonce[0] = 9
			if info.APNonce[0] != 1 {
				t.Fatal("nonce aliases observation")
			}
		})
	}
}
func TestUnknownDemotionPolicy(t *testing.T) {
	for _, present := range []bool{false, true} {
		parameters := map[string]bool{}
		if present {
			parameters["ApDemotionPolicyOverride"] = false
		}
		entry := map[string]any{}
		err := applySigningRules(entry, parameters, []any{map[string]any{"Conditions": map[string]any{"ApDemotionPolicyOverride": false}, "Actions": map[string]any{"EPRO": false}}})
		if err != nil {
			t.Fatal(err)
		}
		_, applied := entry["EPRO"]
		if applied != present {
			t.Fatalf("present=%v applied=%v", present, applied)
		}
	}
}
func ExampleObserveRecovery() {
	_, err := ObserveRecovery(context.Background(), nil, 0)
	fmt.Println(err)
	// Output: observed signing ECID is required
}
