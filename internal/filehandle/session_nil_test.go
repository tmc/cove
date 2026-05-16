package filehandle

import (
	"context"
	"strings"
	"testing"
)

func TestSessionNilReceiver(t *testing.T) {
	var s *Session

	if got := s.DeviceConfiguration(); got.ID != 0 {
		t.Errorf("nil.DeviceConfiguration().ID = %d, want zero", got.ID)
	}
	if got := s.Summary(); !strings.Contains(got, "disabled") {
		t.Errorf("nil.Summary() = %q, want contains \"disabled\"", got)
	}
	if err := s.Close(); err != nil {
		t.Errorf("nil.Close() = %v, want nil", err)
	}
}

func TestSessionNilReceiverErrors(t *testing.T) {
	var s *Session

	tests := []struct {
		name string
		run  func() error
	}{
		{"ReadFrame", func() error { _, err := s.ReadFrame(); return err }},
		{"SendFrame", func() error { return s.SendFrame([]byte{0x01}) }},
		{"Pump", func() error { return s.Pump(context.Background(), nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), "not initialized") {
				t.Errorf("%s nil receiver error = %v, want \"not initialized\"", tt.name, err)
			}
		})
	}
}
