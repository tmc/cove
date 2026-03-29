package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseRuntimeUSBRequest(t *testing.T) {
	req, err := parseRuntimeUSBRequest([]byte(`{
		"action":"ATTACH-PASSTHROUGH",
		"controller_index":1,
		"service_id":17
	}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if req.Action != string(runtimeUSBActionAttachPassthrough) {
		t.Fatalf("action = %q", req.Action)
	}
	if req.ControllerIndex != 1 {
		t.Fatalf("controller_index = %d", req.ControllerIndex)
	}
	if req.ServiceID != 17 {
		t.Fatalf("service_id = %d", req.ServiceID)
	}
	if req.DeviceIndex != nil {
		t.Fatalf("device_index = %#v", req.DeviceIndex)
	}
}

func TestParseRuntimeUSBRequestValidation(t *testing.T) {
	tests := []struct {
		name string
		req  runtimeUSBRequest
		want string
	}{
		{
			name: "list",
			req:  runtimeUSBRequest{Action: "list"},
		},
		{
			name: "attach mass storage missing path",
			req:  runtimeUSBRequest{Action: string(runtimeUSBActionAttachMassStorage)},
			want: "path required for attach-mass-storage",
		},
		{
			name: "attach passthrough missing ids",
			req:  runtimeUSBRequest{Action: string(runtimeUSBActionAttachPassthrough)},
			want: "service_id or location_id required for attach-passthrough",
		},
		{
			name: "detach missing device index",
			req:  runtimeUSBRequest{Action: string(runtimeUSBActionDetach)},
			want: "device_index required for detach",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.validate()
			if tt.want == "" {
				if err != nil {
					t.Fatalf("validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validate error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestRuntimeUSBControlResponseJSON(t *testing.T) {
	idx := 0
	payload := runtimeUSBResponse{
		OK:     true,
		Action: string(runtimeUSBActionList),
		List: &runtimeUSBListResponse{
			Controllers: []runtimeUSBControllerInfo{
				{
					Index:       0,
					Kind:        "VZXHCIController",
					Description: "USB controller",
					DeviceCount: 1,
					Devices: []runtimeUSBDeviceInfo{
						{
							Index:           &idx,
							ControllerIndex: 0,
							Kind:            "VZUSBMassStorageDevice",
							UUID:            "01234567-89AB-CDEF-0123-456789ABCDEF",
							Path:            "/tmp/disk.img",
							ReadOnly:        true,
						},
					},
				},
			},
		},
	}

	resp := runtimeUSBControlResponse(payload)
	if !resp.Success {
		t.Fatalf("success = false: %#v", resp)
	}

	var got runtimeUSBResponse
	if err := json.Unmarshal([]byte(resp.Data), &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !got.OK || got.Action != string(runtimeUSBActionList) || got.List == nil {
		t.Fatalf("response = %#v", got)
	}
	if len(got.List.Controllers) != 1 || got.List.Controllers[0].DeviceCount != 1 {
		t.Fatalf("response controllers = %#v", got.List.Controllers)
	}
}

func TestHandleRuntimeUSBJSONRequestWithoutVM(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handleRuntimeUSBJSONRequest([]byte(`{"action":"list"}`))
	if resp.Success {
		t.Fatalf("success = true: %#v", resp)
	}
	if !strings.Contains(resp.Error, "vm not configured") {
		t.Fatalf("error = %q", resp.Error)
	}

	var got runtimeUSBResponse
	if err := json.Unmarshal([]byte(resp.Data), &got); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if got.OK || got.Action != string(runtimeUSBActionList) {
		t.Fatalf("response = %#v", got)
	}
}

func TestNormalizeRuntimeUSBAction(t *testing.T) {
	if got := normalizeRuntimeUSBAction("attach-storage"); got != string(runtimeUSBActionAttachMassStorage) {
		t.Fatalf("attach-storage => %q", got)
	}
	if got := normalizeRuntimeUSBAction("detach-device"); got != string(runtimeUSBActionDetach) {
		t.Fatalf("detach-device => %q", got)
	}
}

func TestRuntimeUSBInvalidJSON(t *testing.T) {
	if _, err := parseRuntimeUSBRequest([]byte(`{"controller_index":1}`)); err == nil {
		t.Fatal("expected parse error for missing action")
	}
}
