package controlclient

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestClientScreenshotData(t *testing.T) {
	tests := []struct {
		name       string
		resp       *controlpb.ControlResponse
		wantData   string
		wantFormat string
		wantErr    string
		wantBase64 bool
	}{
		{
			name: "typed",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_ScreenshotResult{
					ScreenshotResult: &controlpb.ScreenshotResponse{
						ImageData: []byte("png data"),
						Format:    "png",
					},
				},
			},
			wantData:   "png data",
			wantFormat: "png",
		},
		{
			name:       "base64 fallback",
			resp:       &controlpb.ControlResponse{Success: true, Data: base64.StdEncoding.EncodeToString([]byte("legacy png"))},
			wantData:   "legacy png",
			wantFormat: "png",
			wantBase64: true,
		},
		{
			name:    "server error",
			resp:    &controlpb.ControlResponse{Success: false, Error: "capture failed"},
			wantErr: "screenshot: capture failed",
		},
		{
			name:       "bad base64",
			resp:       &controlpb.ControlResponse{Success: true, Data: "?"},
			wantErr:    "decode screenshot",
			wantBase64: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 1)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return tt.resp
			})
			c := New(sock)
			c.SetTimeout(123)
			data, format, err := c.ScreenshotData()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tt.wantErr)
				}
				var corrupt base64.CorruptInputError
				if tt.wantBase64 && !errors.As(err, &corrupt) {
					t.Fatalf("err = %v, want base64 corrupt input", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ScreenshotData: %v", err)
			}
			if string(data) != tt.wantData || format != tt.wantFormat {
				t.Fatalf("result = %q %q, want %q %q", data, format, tt.wantData, tt.wantFormat)
			}
			if got := c.Timeout(); got != 123 {
				t.Fatalf("timeout = %v, want restored 123ns", got)
			}
			req := <-reqc
			if req.GetType() != "screenshot" {
				t.Fatalf("type = %q, want screenshot", req.GetType())
			}
			s := req.GetScreenshot()
			if s.GetScale() != 1 || s.GetQuality() != 90 || s.GetFormat() != "png" {
				t.Fatalf("screenshot request = %+v", s)
			}
		})
	}
}
