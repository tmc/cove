package main

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestReadAtFullOrZeroFullRead(t *testing.T) {
	r := bytes.NewReader([]byte("abcdef"))
	buf := make([]byte, 4)
	if err := readAtFullOrZero(r, buf, 1); err != nil {
		t.Fatalf("err = %v", err)
	}
	if string(buf) != "bcde" {
		t.Fatalf("buf = %q, want bcde", buf)
	}
}

func TestReadAtFullOrZeroEOFZeroFills(t *testing.T) {
	r := bytes.NewReader([]byte("abc"))
	buf := []byte{0xff, 0xff, 0xff, 0xff, 0xff}
	if err := readAtFullOrZero(r, buf, 1); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !bytes.Equal(buf, []byte{'b', 'c', 0, 0, 0}) {
		t.Fatalf("buf = %v, want bc + zero-fill", buf)
	}
}

type errReaderAt struct{ err error }

func (e errReaderAt) ReadAt(p []byte, off int64) (int, error) { return 0, e.err }

func TestReadAtFullOrZeroOtherErrorPropagates(t *testing.T) {
	want := errors.New("disk failure")
	err := readAtFullOrZero(errReaderAt{want}, make([]byte, 4), 0)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

var _ io.ReaderAt = errReaderAt{}
