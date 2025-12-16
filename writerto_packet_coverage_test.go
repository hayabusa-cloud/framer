package framer_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

type nErrReader struct {
	b    []byte
	err  error
	done bool
}

func (r *nErrReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, r.b)
	return n, r.err
}

type packetErrWriter struct{ err error }

func (w packetErrWriter) Write([]byte) (int, error) { return 0, w.err }

type writeToFinalEOFReader struct {
	b    []byte
	done bool
}

func (r *writeToFinalEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, r.b)
	return n, io.EOF
}

func TestReader_WriteTo_Packet_CopiesUntilEOF(t *testing.T) {
	payload := bytes.Repeat([]byte{'p'}, 128)
	r := fr.NewReader(bytes.NewReader(payload), fr.WithReadUDP()).(*fr.Reader)

	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatalf("payload mismatch")
	}
}

func TestReader_WriteTo_Packet_DstZeroProgressNil_ReturnsIoErrShortWrite(t *testing.T) {
	r := fr.NewReader(bytes.NewReader([]byte("abc")), fr.WithReadUDP()).(*fr.Reader)

	n, err := r.WriteTo(&noProgressWriter{})
	if !errors.Is(err, io.ErrShortWrite) || n != 0 {
		t.Fatalf("want (0, io.ErrShortWrite), got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_Packet_DstError_Propagates(t *testing.T) {
	boom := errors.New("boom")
	r := fr.NewReader(bytes.NewReader([]byte("x")), fr.WithReadUDP()).(*fr.Reader)

	n, err := r.WriteTo(packetErrWriter{err: boom})
	if !errors.Is(err, boom) || n != 0 {
		t.Fatalf("want (0, boom), got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_Packet_ReadWouldBlock_Propagates(t *testing.T) {
	r := fr.NewReader(wbReader{}, fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)

	n, err := r.WriteTo(io.Discard)
	if !errors.Is(err, iox.ErrWouldBlock) || n != 0 {
		t.Fatalf("want (0, ErrWouldBlock), got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_Packet_DstWouldBlock_PropagatesWithProgress(t *testing.T) {
	payload := []byte("hello")
	r := fr.NewReader(bytes.NewReader(payload), fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)

	dst := &wouldBlockWriter{limit: 2}
	n, err := r.WriteTo(dst)
	if !errors.Is(err, iox.ErrWouldBlock) || n != 2 {
		t.Fatalf("want (2, ErrWouldBlock), got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_Packet_ReadReturnsErrMore_WithProgress(t *testing.T) {
	src := &nErrReader{b: []byte("xyz"), err: iox.ErrMore}
	r := fr.NewReader(src, fr.WithReadUDP()).(*fr.Reader)

	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if !errors.Is(err, iox.ErrMore) || n != 3 {
		t.Fatalf("want (3, ErrMore), got (%d, %v)", n, err)
	}
	if dst.String() != "xyz" {
		t.Fatalf("dst=%q", dst.String())
	}
}

func TestReader_WriteTo_Packet_ReadEOFWithProgress_ReturnsNil(t *testing.T) {
	payload := []byte("final")
	r := fr.NewReader(&writeToFinalEOFReader{b: payload}, fr.WithReadUDP()).(*fr.Reader)

	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("want (%d, nil), got (%d, %v)", len(payload), n, err)
	}
	if dst.String() != "final" {
		t.Fatalf("dst=%q", dst.String())
	}
}

func TestReader_WriteTo_Packet_ReadError_Propagates(t *testing.T) {
	boom := errors.New("boom")
	r := fr.NewReader(&onceErrReader{err: boom}, fr.WithReadUDP()).(*fr.Reader)

	n, err := r.WriteTo(io.Discard)
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("want (0, boom), got (%d, %v)", n, err)
	}
}
