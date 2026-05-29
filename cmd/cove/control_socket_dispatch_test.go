package main

import (
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestControlServerHandleRawOCRMissingPayload(t *testing.T) {
	s := &ControlServer{}
	req := &controlpb.ControlRequest{Type: "ocr"}
	resp, handled := s.HandleRaw(req, nil)
	if !handled {
		t.Fatal("HandleRaw(ocr without payload) handled = false, want true")
	}
	if resp == nil || !strings.Contains(resp.Error, "missing ocr command payload") {
		t.Fatalf("resp = %+v, want missing-payload error", resp)
	}
}

func TestControlServerHandleRawUnknownTypeUnhandled(t *testing.T) {
	s := &ControlServer{}
	req := &controlpb.ControlRequest{Type: "definitely-not-a-real-type"}
	resp, handled := s.HandleRaw(req, []byte(`{}`))
	if handled {
		t.Fatalf("HandleRaw(unknown) handled = true, want false")
	}
	if resp != nil {
		t.Fatalf("resp = %+v, want nil for unhandled", resp)
	}
}

func TestControlServerHandleStreamNonStreamTypeUnhandled(t *testing.T) {
	s := &ControlServer{}
	cases := []string{"", "agent-exec", "ping", "shutdown", "snapshot"}
	for _, typ := range cases {
		t.Run(typ, func(t *testing.T) {
			req := &controlpb.ControlRequest{Type: typ}
			handled, closeConn := s.HandleStream(nil, req, nil)
			if handled {
				t.Errorf("HandleStream(%q) handled = true, want false", typ)
			}
			if closeConn {
				t.Errorf("HandleStream(%q) closeConn = true, want false", typ)
			}
		})
	}
}
