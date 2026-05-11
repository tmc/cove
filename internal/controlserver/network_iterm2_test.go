package controlserver

import (
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestNetworkBridgeITerm2ProxyResponses(t *testing.T) {
	_ = t.TempDir()
	tests := []struct {
		name string
		run  func(*NetworkBridge) *controlResponse
		want controlResponse
	}{
		{
			name: "status stopped",
			run: func(b *NetworkBridge) *controlResponse {
				return responseOf(b.ITerm2ProxyStatusResponse())
			},
			want: controlResponse{success: true, data: "stopped", message: "stopped"},
		},
		{
			name: "stop not running",
			run: func(b *NetworkBridge) *controlResponse {
				return responseOf(b.StopITerm2ProxyResponse())
			},
			want: controlResponse{success: true, data: "iterm2 proxy not running", message: "iterm2 proxy not running"},
		},
		{
			name: "start without host",
			run: func(b *NetworkBridge) *controlResponse {
				return responseOf(b.StartITerm2Proxy(0))
			},
			want: controlResponse{err: "iterm2 proxy: control server not configured"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b NetworkBridge
			if got := *tt.run(&b); got != tt.want {
				t.Fatalf("response = %+v, want %+v", got, tt.want)
			}
		})
	}
}

type controlResponse struct {
	success bool
	data    string
	message string
	err     string
}

func responseOf(r *controlpb.ControlResponse) *controlResponse {
	out := &controlResponse{success: r.GetSuccess(), data: r.GetData(), err: r.GetError()}
	if msg := r.GetMessage(); msg != nil {
		out.message = msg.GetMessage()
	}
	return out
}
