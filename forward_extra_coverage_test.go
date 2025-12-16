package framer_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
)

func TestForward_Stream_ZeroLengthMessage(t *testing.T) {
	wire := []byte{0}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, bytes.NewReader(wire), fr.WithProtocol(fr.BinaryStream))

	n, err := fwd.ForwardOnce()
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if !bytes.Equal(dst.Bytes(), wire) {
		t.Fatalf("dst=%v want=%v", dst.Bytes(), wire)
	}
}

func TestForward_Stream_HeaderTooLargeForInternalBuffer_ReturnsIoErrShortBuffer(t *testing.T) {
	// Default internal buffer is 64KiB when ReadLimit==0.
	hdr := frameHeader56BE(64*1024 + 1)
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, bytes.NewReader(hdr), fr.WithProtocol(fr.BinaryStream))

	n, err := fwd.ForwardOnce()
	if !errors.Is(err, io.ErrShortBuffer) || n != 0 {
		t.Fatalf("want (0, io.ErrShortBuffer), got (%d, %v)", n, err)
	}
}

type bogusCountReader struct{ done bool }

func (r *bogusCountReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	// Violate io.Reader contract deliberately to exercise defensive ErrTooLong handling.
	return len(p) + 1, nil
}

func TestForward_SeqPacket_ErrTooLong_DefensivePropagation(t *testing.T) {
	src := &bogusCountReader{}
	fwd := fr.NewForwarder(io.Discard, src, fr.WithProtocol(fr.SeqPacket), fr.WithReadLimit(8))

	n, err := fwd.ForwardOnce()
	if !errors.Is(err, fr.ErrTooLong) || n != 9 {
		t.Fatalf("want (9, ErrTooLong), got (%d, %v)", n, err)
	}
}

type errMoreReader struct{ done bool }

func (r *errMoreReader) Read([]byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return 0, fr.ErrMore
}

func TestForward_SeqPacket_ErrMore_Propagates(t *testing.T) {
	src := &errMoreReader{}
	fwd := fr.NewForwarder(io.Discard, src, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n, err := fwd.ForwardOnce()
	if !errors.Is(err, fr.ErrMore) || n != 0 {
		t.Fatalf("want (0, ErrMore), got (%d, %v)", n, err)
	}
}

func TestForward_Stream_HeaderWouldBlock_Propagates(t *testing.T) {
	fwd := fr.NewForwarder(io.Discard, wbReader{}, fr.WithProtocol(fr.BinaryStream), fr.WithNonblock())

	n, err := fwd.ForwardOnce()
	if !errors.Is(err, fr.ErrWouldBlock) || n != 0 {
		t.Fatalf("want (0, ErrWouldBlock), got (%d, %v)", n, err)
	}
}

func TestForward_Stream_DstErrMore_Propagates(t *testing.T) {
	// Destination returns ErrMore during framing write.
	dst := &errMoreWriter{}
	src := bytes.NewReader([]byte{1, 'z'})
	fwd := fr.NewForwarder(dst, src, fr.WithProtocol(fr.BinaryStream), fr.WithNonblock())

	n, err := fwd.ForwardOnce()
	if !errors.Is(err, fr.ErrMore) {
		t.Fatalf("want ErrMore, got (%d, %v)", n, err)
	}
}
