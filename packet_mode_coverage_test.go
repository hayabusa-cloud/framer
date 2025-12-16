package framer_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

type fixedReader struct {
	b   []byte
	off int
}

func (r *fixedReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

type shortOKWriter struct{}

func (shortOKWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return len(p) - 1, nil
}

type wbReader struct{}

func (wbReader) Read([]byte) (int, error) { return 0, iox.ErrWouldBlock }

type wbWriter2 struct{}

func (wbWriter2) Write([]byte) (int, error) { return 0, iox.ErrWouldBlock }

func TestPacketRead_TooLongWhenExceedingReadLimit(t *testing.T) {
	// Provide a large datagram; configure UDP (packet) mode and a small read limit.
	payload := bytes.Repeat([]byte{'P'}, 64)
	r := fr.NewReader(&fixedReader{b: payload}, fr.WithReadUDP(), fr.WithReadLimit(8))
	buf := make([]byte, len(payload))
	if _, err := r.Read(buf); !errors.Is(err, fr.ErrTooLong) {
		t.Fatalf("err=%v want ErrTooLong", err)
	}
}

func TestPacketWrite_ShortWriteNilMapsToIoErrShortWrite(t *testing.T) {
	w := fr.NewWriter(shortOKWriter{}, fr.WithWriteUDP())
	if _, err := w.Write([]byte("abc")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err=%v want io.ErrShortWrite", err)
	}
}

func TestPacketWrite_WouldBlockPropagates(t *testing.T) {
	w := fr.NewWriter(wbWriter2{}, fr.WithWriteUDP(), fr.WithNonblock())
	if _, err := w.Write([]byte("xyz")); !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("err=%v want ErrWouldBlock", err)
	}
}

func TestPacketRead_WouldBlockPropagates(t *testing.T) {
	r := fr.NewReader(wbReader{}, fr.WithReadUDP(), fr.WithNonblock())
	buf := make([]byte, 16)
	if _, err := r.Read(buf); !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("err=%v want ErrWouldBlock", err)
	}
}
