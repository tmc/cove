package rfb

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"io"
	"net"
	"testing"
)

func TestClientHandshakeAndReadRawUpdate(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveOneRawUpdate(serverConn)
	}()

	c, err := NewClient(context.Background(), clientConn)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if got, want := c.Size(), imagePoint(2, 1); got != want {
		t.Fatalf("Size() = %v, want %v", got, want)
	}
	img, err := c.ReadUpdate(context.Background())
	if err != nil {
		t.Fatalf("ReadUpdate: %v", err)
	}
	if got, want := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA), (color.NRGBA{R: 0x33, G: 0x22, B: 0x11, A: 0xff}); got != want {
		t.Fatalf("pixel 0 = %#v, want %#v", got, want)
	}
	if got, want := color.NRGBAModel.Convert(img.At(1, 0)).(color.NRGBA), (color.NRGBA{R: 0x66, G: 0x55, B: 0x44, A: 0xff}); got != want {
		t.Fatalf("pixel 1 = %#v, want %#v", got, want)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestClientInputEvents(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveInputEvents(serverConn)
	}()

	c, err := NewClient(context.Background(), clientConn)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Key(0xff0d, true); err != nil {
		t.Fatalf("Key down: %v", err)
	}
	if err := c.Key(0xff0d, false); err != nil {
		t.Fatalf("Key up: %v", err)
	}
	if err := c.KeyPress('A'); err != nil {
		t.Fatalf("KeyPress: %v", err)
	}
	if err := c.Pointer(7, -1, 1); err != nil {
		t.Fatalf("Pointer: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestClientRejectsAuthOnlyServer(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	serverDone := make(chan error, 1)
	go func() {
		defer serverConn.Close()
		if _, err := io.WriteString(serverConn, protocolVersion); err != nil {
			return
		}
		var clientVersion [12]byte
		if _, err := io.ReadFull(serverConn, clientVersion[:]); err != nil {
			return
		}
		_, err := serverConn.Write([]byte{1, 2})
		serverDone <- err
	}()

	if _, err := NewClient(context.Background(), clientConn); err == nil {
		t.Fatal("NewClient succeeded")
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func serveOneRawUpdate(conn net.Conn) error {
	if err := serveHandshake(conn, 2, 1); err != nil {
		return err
	}
	if err := readExactMessage(conn, []byte{
		msgFramebufferUpdateRequest,
		0,
		0, 0,
		0, 0,
		0, 2,
		0, 1,
	}); err != nil {
		return err
	}
	msg := bytes.NewBuffer(nil)
	msg.Write([]byte{serverFramebufferUpdate, 0, 0, 1})
	writeU16(msg, 0)
	writeU16(msg, 0)
	writeU16(msg, 2)
	writeU16(msg, 1)
	writeU32(msg, encodingRaw)
	msg.Write([]byte{
		0x11, 0x22, 0x33, 0x00,
		0x44, 0x55, 0x66, 0x00,
	})
	_, err := conn.Write(msg.Bytes())
	return err
}

func serveInputEvents(conn net.Conn) error {
	if err := serveHandshake(conn, 4, 3); err != nil {
		return err
	}
	if err := readExactMessage(conn, []byte{msgKeyEvent, 1, 0, 0, 0, 0, 0xff, 0x0d}); err != nil {
		return err
	}
	if err := readExactMessage(conn, []byte{msgKeyEvent, 0, 0, 0, 0, 0, 0xff, 0x0d}); err != nil {
		return err
	}
	if err := readExactMessage(conn, []byte{msgKeyEvent, 1, 0, 0, 0, 0, 0, 'A'}); err != nil {
		return err
	}
	if err := readExactMessage(conn, []byte{msgKeyEvent, 0, 0, 0, 0, 0, 0, 'A'}); err != nil {
		return err
	}
	return readExactMessage(conn, []byte{msgPointerEvent, 1, 0, 3, 0, 0})
}

func TestKeysymForRune(t *testing.T) {
	for _, tt := range []struct {
		r    rune
		want uint32
		ok   bool
	}{
		{r: 'a', want: 'a', ok: true},
		{r: 'A', want: 'A', ok: true},
		{r: '\n', want: 0xff0d, ok: true},
		{r: '\t', want: 0xff09, ok: true},
		{r: 'é', want: 0xe9, ok: true},
		{r: 'Ω', ok: false},
	} {
		got, ok := keysymForRune(tt.r)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("keysymForRune(%q) = %#x, %v, want %#x, %v", tt.r, got, ok, tt.want, tt.ok)
		}
	}
}

func serveHandshake(conn net.Conn, width, height uint16) error {
	if _, err := io.WriteString(conn, protocolVersion); err != nil {
		return err
	}
	var clientVersion [12]byte
	if _, err := io.ReadFull(conn, clientVersion[:]); err != nil {
		return err
	}
	if _, err := conn.Write([]byte{1, securityNone}); err != nil {
		return err
	}
	var selected [1]byte
	if _, err := io.ReadFull(conn, selected[:]); err != nil {
		return err
	}
	if selected[0] != securityNone {
		return errUnexpectedMessage
	}
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return err
	}
	var clientInit [1]byte
	if _, err := io.ReadFull(conn, clientInit[:]); err != nil {
		return err
	}
	init := bytes.NewBuffer(nil)
	writeU16(init, uint32(width))
	writeU16(init, uint32(height))
	init.Write([]byte{
		32, 24, 0, 1,
		0, 255,
		0, 255,
		0, 255,
		16, 8, 0,
		0, 0, 0,
	})
	writeU32(init, 4)
	init.WriteString("qemu")
	if _, err := conn.Write(init.Bytes()); err != nil {
		return err
	}
	if err := readExactMessage(conn, []byte{
		msgSetPixelFormat,
		0, 0, 0,
		32, 24, 0, 1,
		0, 255,
		0, 255,
		0, 255,
		16, 8, 0,
		0, 0, 0,
	}); err != nil {
		return err
	}
	return readExactMessage(conn, []byte{
		msgSetEncodings,
		0,
		0, 1,
		0, 0, 0, encodingRaw,
	})
}

var errUnexpectedMessage = &protocolError{"unexpected message"}

type protocolError struct {
	msg string
}

func (e *protocolError) Error() string {
	return e.msg
}

func readExactMessage(r io.Reader, want []byte) error {
	got := make([]byte, len(want))
	if _, err := io.ReadFull(r, got); err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return &protocolError{"unexpected message bytes"}
	}
	return nil
}

func writeU16(w io.Writer, v uint32) {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], uint16(v))
	_, _ = w.Write(buf[:])
}

func writeU32(w io.Writer, v uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	_, _ = w.Write(buf[:])
}

func imagePoint(x, y int) image.Point {
	return image.Point{X: x, Y: y}
}
