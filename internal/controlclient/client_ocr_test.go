package controlclient

import (
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestClientOCRAllText(t *testing.T) {
	tests := []struct {
		name string
		resp *controlpb.ControlResponse
		want string
	}{
		{
			name: "typed",
			resp: &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_OcrText{
				OcrText: &controlpb.OCRTextResponse{Text: "typed text"},
			}},
			want: "typed text",
		},
		{name: "data fallback", resp: &controlpb.ControlResponse{Success: true, Data: "plain text"}, want: "plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return tt.resp
			})
			got, err := New(sock).OCRAllText()
			if err != nil {
				t.Fatalf("OCRAllText: %v", err)
			}
			if got != tt.want {
				t.Fatalf("OCRAllText = %q, want %q", got, tt.want)
			}
			ocr := (<-reqc).GetOcr()
			if ocr.GetAction() != "all-text" {
				t.Fatalf("ocr action = %q, want all-text", ocr.GetAction())
			}
		})
	}
}

func TestClientOCRClickText(t *testing.T) {
	reqc := make(chan *controlpb.ControlRequest, 1)
	sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
		reqc <- req
		return &controlpb.ControlResponse{Success: true}
	})
	if err := New(sock).OCRClickText("Install", 2*time.Second); err != nil {
		t.Fatalf("OCRClickText: %v", err)
	}
	ocr := (<-reqc).GetOcr()
	if ocr.GetAction() != "click" || ocr.GetText() != "Install" || ocr.GetTimeout() != "2s" {
		t.Fatalf("ocr = %+v", ocr)
	}
}

func TestClientOCRErrors(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Client) error
		want string
	}{
		{"all text", func(c *Client) error { _, err := c.OCRAllText(); return err }, "ocr all-text: no text"},
		{"click text", func(c *Client) error { return c.OCRClickText("OK", time.Second) }, "ocr click-text: no text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sock := serveControlClientTest(t, func(*controlpb.ControlRequest) *controlpb.ControlResponse {
				return &controlpb.ControlResponse{Success: false, Error: "no text"}
			})
			if err := tt.run(New(sock)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want substring %q", err, tt.want)
			}
		})
	}
}
