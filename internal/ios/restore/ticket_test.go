package restore

import (
	"bytes"
	"encoding/asn1"
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"testing"
)

func ticketDER(class, tag int, parts ...[]byte) []byte {
	b, err := asn1.Marshal(asn1.RawValue{Class: class, Tag: tag, IsCompound: true, Bytes: bytes.Join(parts, nil)})
	if err != nil {
		panic(err)
	}
	return b
}
func ticketSet(parts ...[]byte) []byte {
	sort.Slice(parts, func(i, j int) bool { return bytes.Compare(parts[i], parts[j]) < 0 })
	return ticketDER(0, 17, parts...)
}
func ticketProperty(name string, value any) []byte {
	var data []byte
	if raw, ok := value.(asn1.RawValue); ok {
		data = raw.FullBytes
	} else {
		if n, ok := value.(uint64); ok {
			value = new(big.Int).SetUint64(n)
		}
		var err error
		data, err = asn1.Marshal(value)
		if err != nil {
			panic(err)
		}
	}
	return ticketDER(3, int(binary.BigEndian.Uint32([]byte(name))), ticketDER(0, 16, append([]byte{22, 4}, name...), data))
}
func testTicket(properties map[string]any, digests map[string][]byte) []byte {
	var fields [][]byte
	for name, value := range properties {
		fields = append(fields, ticketProperty(name, value))
	}
	groups := [][]byte{ticketProperty("MANP", asn1.RawValue{FullBytes: ticketSet(fields...)})}
	for name, digest := range digests {
		groups = append(groups, ticketProperty(name, asn1.RawValue{FullBytes: ticketSet(ticketProperty("DGST", digest))}))
	}
	body := ticketSet(ticketProperty("MANB", asn1.RawValue{FullBytes: ticketSet(groups...)}))
	return ticketDER(0, 16, []byte{22, 4, 'I', 'M', '4', 'M'}, []byte{2, 1, 0}, body, []byte{4, 0}, []byte{48, 0})
}
func TestMatchTicket(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(map[string]any, map[string][]byte, *TicketRequirements)
		want   string
	}{
		{name: "match"},
		{name: "ECID", change: func(p map[string]any, _ map[string][]byte, _ *TicketRequirements) { p["ECID"] = uint64(17) }, want: "ECID"},
		{name: "missing board", change: func(p map[string]any, _ map[string][]byte, _ *TicketRequirements) { delete(p, "BORD") }, want: "BORD"},
		{name: "wrong board", change: func(p map[string]any, _ map[string][]byte, _ *TicketRequirements) { p["BORD"] = uint64(1) }, want: "BORD"},
		{name: "wrong chip", change: func(p map[string]any, _ map[string][]byte, _ *TicketRequirements) { p["CHIP"] = uint64(2) }, want: "CHIP"},
		{name: "chip type", change: func(p map[string]any, _ map[string][]byte, _ *TicketRequirements) { p["CHIP"] = []byte{3} }, want: "CHIP"},
		{name: "changed AP nonce", change: func(p map[string]any, _ map[string][]byte, _ *TicketRequirements) { p["BNCH"] = []byte{9} }, want: "BNCH"},
		{name: "missing AP nonce", change: func(p map[string]any, _ map[string][]byte, _ *TicketRequirements) { delete(p, "BNCH") }, want: "BNCH"},
		{name: "changed SEP nonce", change: func(p map[string]any, _ map[string][]byte, _ *TicketRequirements) { p["snon"] = []byte{9} }, want: "snon"},
		{name: "unobserved SEP nonce", change: func(_ map[string]any, _ map[string][]byte, w *TicketRequirements) { w.SEPNonce = nil }, want: "no observed nonce"},
		{name: "SEP absent both", change: func(p map[string]any, _ map[string][]byte, w *TicketRequirements) {
			delete(p, "snon")
			w.SEPNonce = nil
		}},
		{name: "missing SEP nonce", change: func(p map[string]any, _ map[string][]byte, _ *TicketRequirements) { delete(p, "snon") }, want: "snon"},
		{name: "wrong build", change: func(_ map[string]any, d map[string][]byte, _ *TicketRequirements) { d["krnl"] = []byte{7} }, want: "build digest"},
		{name: "missing image", change: func(_ map[string]any, d map[string][]byte, _ *TicketRequirements) { delete(d, "krnl") }, want: "build digest"},
		{name: "empty expected digest", change: func(_ map[string]any, _ map[string][]byte, w *TicketRequirements) { w.ImageDigests["krnl"] = nil }, want: "build digest"},
		{name: "no expected images", change: func(_ map[string]any, _ map[string][]byte, w *TicketRequirements) { w.ImageDigests = nil }, want: "requires"},
		{name: "no observed AP nonce", change: func(_ map[string]any, _ map[string][]byte, w *TicketRequirements) { w.APNonce = nil }, want: "requires"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want := TicketRequirements{ECID: 1<<63 | 17, BoardID: 0, ChipID: 3, APNonce: []byte{1, 2}, SEPNonce: []byte{3, 4}, ImageDigests: map[string][]byte{"krnl": {5, 6}}}
			props := map[string]any{"ECID": want.ECID, "BORD": want.BoardID, "CHIP": want.ChipID, "BNCH": want.APNonce, "snon": want.SEPNonce}
			digests := map[string][]byte{"krnl": {5, 6}}
			if tt.change != nil {
				tt.change(props, digests, &want)
			}
			err := MatchTicket(testTicket(props, digests), want)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v; want %q", err, tt.want)
			}
		})
	}
}
func ExampleMatchTicket() {
	want := TicketRequirements{ECID: 1, ChipID: 3, APNonce: []byte{1, 2}, ImageDigests: map[string][]byte{"krnl": {5, 6}}}
	// This synthetic ticket has no signature. Matching only checks assertions.
	ticket := testTicket(map[string]any{"ECID": uint64(1), "BORD": uint64(0), "CHIP": uint64(3), "BNCH": []byte{1, 2}}, map[string][]byte{"krnl": {5, 6}})
	fmt.Println(MatchTicket(ticket, want))
	// Output: <nil>
}

func TestMatchTicketPolicy(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(map[string]any)
	}{
		{name: "match"},
		{name: "missing domain", change: func(p map[string]any) { delete(p, "SDOM") }},
		{name: "wrong domain", change: func(p map[string]any) { p["SDOM"] = uint64(2) }},
		{name: "wrong security", change: func(p map[string]any) { p["CSEC"] = false }},
		{name: "missing production", change: func(p map[string]any) { delete(p, "CPRO") }},
		{name: "integer production", change: func(p map[string]any) { p["CPRO"] = uint64(0) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			domain, production, security := uint64(1), false, true
			want := TicketRequirements{ECID: 1, ChipID: 3, APNonce: []byte{1}, ImageDigests: map[string][]byte{"ibss": {2}}, SecurityDomain: &domain, ProductionMode: &production, SecurityMode: &security}
			props := map[string]any{"ECID": uint64(1), "BORD": uint64(0), "CHIP": uint64(3), "BNCH": []byte{1}, "SDOM": domain, "CPRO": production, "CSEC": security}
			if tt.change != nil {
				tt.change(props)
			}
			err := MatchTicket(testTicket(props, want.ImageDigests), want)
			if (err == nil) != (tt.change == nil) {
				t.Fatalf("got %v", err)
			}
		})
	}
}
