package rfb

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLiveQEMUVNC(t *testing.T) {
	addr := os.Getenv("COVE_TEST_RFB_ADDR")
	if addr == "" {
		t.Skip("set COVE_TEST_RFB_ADDR=127.0.0.1:5901 to exercise a live QEMU VNC endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	img, err := c.ReadUpdate(ctx)
	if err != nil {
		t.Fatalf("ReadUpdate: %v", err)
	}
	if img.Bounds().Empty() {
		t.Fatalf("ReadUpdate returned empty image bounds %v", img.Bounds())
	}
}
