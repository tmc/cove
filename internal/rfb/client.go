package rfb

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"net"
	"strings"
	"time"
)

const (
	protocolVersion = "RFB 003.008\n"

	securityNone = 1

	msgSetPixelFormat           = 0
	msgSetEncodings             = 2
	msgFramebufferUpdateRequest = 3
	msgKeyEvent                 = 4
	msgPointerEvent             = 5

	serverFramebufferUpdate = 0

	encodingRaw = 0
)

type Client struct {
	conn   net.Conn
	width  uint16
	height uint16
}

func Dial(ctx context.Context, addr string) (*Client, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial rfb: %w", err)
	}
	c, err := NewClient(ctx, conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

func NewClient(ctx context.Context, conn net.Conn) (*Client, error) {
	c := &Client{conn: conn}
	if err := c.withDeadline(ctx, c.handshake); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) Size() image.Point {
	return image.Point{X: int(c.width), Y: int(c.height)}
}

func (c *Client) ReadUpdate(ctx context.Context) (image.Image, error) {
	var img image.Image
	err := c.withDeadline(ctx, func() error {
		var err error
		img, err = c.readUpdate()
		return err
	})
	if err != nil {
		return nil, err
	}
	return img, nil
}

func (c *Client) RequestUpdate(incremental bool) error {
	buf := []byte{
		msgFramebufferUpdateRequest,
		boolByte(incremental),
		0, 0,
		0, 0,
		byte(c.width >> 8), byte(c.width),
		byte(c.height >> 8), byte(c.height),
	}
	if _, err := c.conn.Write(buf); err != nil {
		return fmt.Errorf("request framebuffer update: %w", err)
	}
	return nil
}

func (c *Client) Key(key uint32, down bool) error {
	buf := make([]byte, 8)
	buf[0] = msgKeyEvent
	buf[1] = boolByte(down)
	binary.BigEndian.PutUint32(buf[4:], key)
	if _, err := c.conn.Write(buf); err != nil {
		return fmt.Errorf("send key event: %w", err)
	}
	return nil
}

func (c *Client) KeyPress(key uint32) error {
	if err := c.Key(key, true); err != nil {
		return err
	}
	return c.Key(key, false)
}

func (c *Client) TypeText(text string) error {
	for _, r := range text {
		key, ok := keysymForRune(r)
		if !ok {
			return fmt.Errorf("unsupported rfb text rune %q", r)
		}
		if err := c.KeyPress(key); err != nil {
			return err
		}
		time.Sleep(30 * time.Millisecond)
	}
	return nil
}

func (c *Client) Pointer(x, y int, buttons uint8) error {
	buf := make([]byte, 6)
	buf[0] = msgPointerEvent
	buf[1] = buttons
	binary.BigEndian.PutUint16(buf[2:], uint16(clamp(x, 0, int(c.width)-1)))
	binary.BigEndian.PutUint16(buf[4:], uint16(clamp(y, 0, int(c.height)-1)))
	if _, err := c.conn.Write(buf); err != nil {
		return fmt.Errorf("send pointer event: %w", err)
	}
	return nil
}

func keysymForRune(r rune) (uint32, bool) {
	switch r {
	case '\n', '\r':
		return 0xff0d, true
	case '\t':
		return 0xff09, true
	case '\b':
		return 0xff08, true
	case 0x1b:
		return 0xff1b, true
	}
	if r >= 0x20 && r <= 0x7e {
		return uint32(r), true
	}
	if r >= 0xa0 && r <= 0xff {
		return uint32(r), true
	}
	return 0, false
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) handshake() error {
	version := make([]byte, len(protocolVersion))
	if _, err := io.ReadFull(c.conn, version); err != nil {
		return fmt.Errorf("read protocol version: %w", err)
	}
	if !strings.HasPrefix(string(version), "RFB 003.") {
		return fmt.Errorf("unsupported rfb protocol version %q", strings.TrimSpace(string(version)))
	}
	if _, err := io.WriteString(c.conn, protocolVersion); err != nil {
		return fmt.Errorf("write protocol version: %w", err)
	}
	securityCount := []byte{0}
	if _, err := io.ReadFull(c.conn, securityCount); err != nil {
		return fmt.Errorf("read security type count: %w", err)
	}
	if securityCount[0] == 0 {
		reason, err := readReason(c.conn)
		if err != nil {
			return err
		}
		return fmt.Errorf("rfb server rejected connection: %s", reason)
	}
	securityTypes := make([]byte, int(securityCount[0]))
	if _, err := io.ReadFull(c.conn, securityTypes); err != nil {
		return fmt.Errorf("read security types: %w", err)
	}
	if !hasByte(securityTypes, securityNone) {
		return fmt.Errorf("rfb server does not offer no-auth security")
	}
	if _, err := c.conn.Write([]byte{securityNone}); err != nil {
		return fmt.Errorf("select no-auth security: %w", err)
	}
	var securityResult [4]byte
	if _, err := io.ReadFull(c.conn, securityResult[:]); err != nil {
		return fmt.Errorf("read security result: %w", err)
	}
	if result := binary.BigEndian.Uint32(securityResult[:]); result != 0 {
		reason, err := readReason(c.conn)
		if err != nil {
			return err
		}
		return fmt.Errorf("rfb security failed: %s", reason)
	}
	if _, err := c.conn.Write([]byte{1}); err != nil {
		return fmt.Errorf("write client init: %w", err)
	}
	if err := c.readServerInit(); err != nil {
		return err
	}
	if err := c.setPixelFormat(); err != nil {
		return err
	}
	if err := c.setEncodings(); err != nil {
		return err
	}
	return nil
}

func (c *Client) readServerInit() error {
	header := make([]byte, 24)
	if _, err := io.ReadFull(c.conn, header); err != nil {
		return fmt.Errorf("read server init: %w", err)
	}
	c.width = binary.BigEndian.Uint16(header[0:2])
	c.height = binary.BigEndian.Uint16(header[2:4])
	nameLen := binary.BigEndian.Uint32(header[20:24])
	if nameLen > 1<<20 {
		return fmt.Errorf("rfb desktop name too large: %d bytes", nameLen)
	}
	if _, err := io.CopyN(io.Discard, c.conn, int64(nameLen)); err != nil {
		return fmt.Errorf("read desktop name: %w", err)
	}
	return nil
}

func (c *Client) setPixelFormat() error {
	buf := make([]byte, 20)
	buf[0] = msgSetPixelFormat
	buf[4] = 32
	buf[5] = 24
	buf[6] = 0
	buf[7] = 1
	binary.BigEndian.PutUint16(buf[8:], 255)
	binary.BigEndian.PutUint16(buf[10:], 255)
	binary.BigEndian.PutUint16(buf[12:], 255)
	buf[14] = 16
	buf[15] = 8
	buf[16] = 0
	if _, err := c.conn.Write(buf); err != nil {
		return fmt.Errorf("set pixel format: %w", err)
	}
	return nil
}

func (c *Client) setEncodings() error {
	buf := make([]byte, 8)
	buf[0] = msgSetEncodings
	binary.BigEndian.PutUint16(buf[2:], 1)
	binary.BigEndian.PutUint32(buf[4:], encodingRaw)
	if _, err := c.conn.Write(buf); err != nil {
		return fmt.Errorf("set encodings: %w", err)
	}
	return nil
}

func (c *Client) readUpdate() (image.Image, error) {
	if err := c.RequestUpdate(false); err != nil {
		return nil, err
	}
	var header [4]byte
	if _, err := io.ReadFull(c.conn, header[:]); err != nil {
		return nil, fmt.Errorf("read framebuffer update header: %w", err)
	}
	if header[0] != serverFramebufferUpdate {
		return nil, fmt.Errorf("unexpected rfb server message %d", header[0])
	}
	rectCount := binary.BigEndian.Uint16(header[2:])
	img := image.NewNRGBA(image.Rect(0, 0, int(c.width), int(c.height)))
	for i := 0; i < int(rectCount); i++ {
		if err := c.readRectangle(img); err != nil {
			return nil, err
		}
	}
	return img, nil
}

func (c *Client) readRectangle(img *image.NRGBA) error {
	var header [12]byte
	if _, err := io.ReadFull(c.conn, header[:]); err != nil {
		return fmt.Errorf("read framebuffer rectangle: %w", err)
	}
	x := int(binary.BigEndian.Uint16(header[0:2]))
	y := int(binary.BigEndian.Uint16(header[2:4]))
	w := int(binary.BigEndian.Uint16(header[4:6]))
	h := int(binary.BigEndian.Uint16(header[6:8]))
	encoding := int32(binary.BigEndian.Uint32(header[8:12]))
	if encoding != encodingRaw {
		return fmt.Errorf("unsupported rfb encoding %d", encoding)
	}
	if x < 0 || y < 0 || w < 0 || h < 0 || x+w > int(c.width) || y+h > int(c.height) {
		return fmt.Errorf("rfb rectangle outside framebuffer: %dx%d at %d,%d", w, h, x, y)
	}
	pixels := make([]byte, w*h*4)
	if _, err := io.ReadFull(c.conn, pixels); err != nil {
		return fmt.Errorf("read raw framebuffer rectangle: %w", err)
	}
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			i := (py*w + px) * 4
			img.SetNRGBA(x+px, y+py, color.NRGBA{
				R: pixels[i+2],
				G: pixels[i+1],
				B: pixels[i],
				A: 255,
			})
		}
	}
	return nil
}

func (c *Client) withDeadline(ctx context.Context, fn func() error) error {
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set rfb deadline: %w", err)
		}
		defer c.conn.SetDeadline(time.Time{})
	}
	if err := fn(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}

func readReason(r io.Reader) (string, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", fmt.Errorf("read rfb failure reason length: %w", err)
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n > 1<<20 {
		return "", fmt.Errorf("rfb failure reason too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("read rfb failure reason: %w", err)
	}
	return string(buf), nil
}

func hasByte(buf []byte, b byte) bool {
	for _, v := range buf {
		if v == b {
			return true
		}
	}
	return false
}

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
