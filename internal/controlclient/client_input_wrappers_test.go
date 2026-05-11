package controlclient

import (
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestClientKeyWrappers(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Client) error
		want []controlpb.KeyCommand
	}{
		{
			name: "key down",
			run:  func(c *Client) error { return c.KeyDown(36) },
			want: []controlpb.KeyCommand{{KeyCode: 36, KeyDown: true}},
		},
		{
			name: "key up",
			run:  func(c *Client) error { return c.KeyUp(36) },
			want: []controlpb.KeyCommand{{KeyCode: 36}},
		},
		{
			name: "send key",
			run:  func(c *Client) error { return c.SendKey(40) },
			want: []controlpb.KeyCommand{{KeyCode: 40, KeyDown: true}, {KeyCode: 40}},
		},
		{
			name: "modified key press",
			run:  func(c *Client) error { return c.KeyPressWithModifiers(12, 1) },
			want: []controlpb.KeyCommand{
				{KeyCode: 12, KeyDown: true, Modifiers: 1, UseCgEvent: true},
				{KeyCode: 12, Modifiers: 1, UseCgEvent: true},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, len(tt.want))
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return &controlpb.ControlResponse{Success: true}
			})
			if err := tt.run(New(sock)); err != nil {
				t.Fatalf("run: %v", err)
			}
			for i, want := range tt.want {
				req := <-reqc
				if req.GetType() != "key" {
					t.Fatalf("request %d type = %q, want key", i, req.GetType())
				}
				got := req.GetKey()
				if got.GetKeyCode() != want.KeyCode || got.GetKeyDown() != want.KeyDown ||
					got.GetModifiers() != want.Modifiers || got.GetUseCgEvent() != want.UseCgEvent {
					t.Fatalf("request %d key = %+v, want %+v", i, got, want)
				}
			}
		})
	}
}

func TestClientMouseClickWrappers(t *testing.T) {
	tests := []struct {
		name     string
		run      func(*Client) error
		absolute bool
	}{
		{"normalized", func(c *Client) error { return c.SendMouseClick(0.25, 0.75) }, false},
		{"absolute", func(c *Client) error { return c.MouseClickAbsolute(120, 240) }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqc := make(chan *controlpb.ControlRequest, 3)
			sock := serveControlClientTest(t, func(req *controlpb.ControlRequest) *controlpb.ControlResponse {
				reqc <- req
				return &controlpb.ControlResponse{Success: true}
			})
			if err := tt.run(New(sock)); err != nil {
				t.Fatalf("run: %v", err)
			}
			for i, action := range []string{"move", "down", "up"} {
				req := <-reqc
				if req.GetType() != "mouse" {
					t.Fatalf("request %d type = %q, want mouse", i, req.GetType())
				}
				mouse := req.GetMouse()
				if mouse.GetAction() != action || mouse.GetAbsolute() != tt.absolute {
					t.Fatalf("request %d mouse = %+v", i, mouse)
				}
			}
		})
	}
}
