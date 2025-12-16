package framer_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

type packetFinalEOFReader struct {
	b    []byte
	done bool
}

func (r *packetFinalEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, r.b)
	return n, io.EOF
}

type onceErrReader struct {
	err  error
	done bool
}

func (r *onceErrReader) Read([]byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return 0, r.err
}

type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }

func TestForward_SeqPacket_EOFWithFinalMessage_ThenEOFNextCall(t *testing.T) {
	payload := []byte("pkt")
	src := &packetFinalEOFReader{b: payload}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, src, fr.WithProtocol(fr.SeqPacket))

	n, err := fwd.ForwardOnce()
	if err != nil || n != len(payload) {
		t.Fatalf("want (%d, nil), got (%d, %v)", len(payload), n, err)
	}
	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatalf("dst=%q want=%q", dst.Bytes(), payload)
	}

	n2, err2 := fwd.ForwardOnce()
	if n2 != 0 || !errors.Is(err2, io.EOF) {
		t.Fatalf("want (0, io.EOF), got (%d, %v)", n2, err2)
	}
}

func TestForward_SeqPacket_ImmediateEOF(t *testing.T) {
	src := bytes.NewReader(nil)
	fwd := fr.NewForwarder(io.Discard, src, fr.WithProtocol(fr.SeqPacket))

	n, err := fwd.ForwardOnce()
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("want (0, io.EOF), got (%d, %v)", n, err)
	}
}

func TestForward_SeqPacket_ReadWouldBlock_Propagates(t *testing.T) {
	fwd := fr.NewForwarder(io.Discard, wbReader{}, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n, err := fwd.ForwardOnce()
	if n != 0 || !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("want (0, ErrWouldBlock), got (%d, %v)", n, err)
	}
}

func TestForward_SeqPacket_ReadError_Propagates(t *testing.T) {
	boom := errors.New("boom")
	fwd := fr.NewForwarder(io.Discard, &onceErrReader{err: boom}, fr.WithProtocol(fr.SeqPacket))

	n, err := fwd.ForwardOnce()
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("want (0, boom), got (%d, %v)", n, err)
	}
}

func TestForward_SeqPacket_DstError_Propagates(t *testing.T) {
	boom := errors.New("boom")
	src := bytes.NewReader([]byte("x"))
	fwd := fr.NewForwarder(failWriter{err: boom}, src, fr.WithProtocol(fr.SeqPacket))

	n, err := fwd.ForwardOnce()
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("want (0, boom), got (%d, %v)", n, err)
	}
}
