package framer_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
)

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func frameHeader56BE(n int) []byte {
	// Stream framing: first byte is 0xFF (framePayloadMaxLen8Bits+2), followed by 7 bytes
	// encoding the payload length in big-endian (low 56 bits).
	u := uint64(n)
	h := make([]byte, 8)
	h[0] = 0xFF
	for i := 0; i < 7; i++ {
		h[7-i] = byte(u)
		u >>= 8
	}
	return h
}

func TestReader_WriteTo_Stream_ConservativeCap_ErrTooLong(t *testing.T) {
	// When ReadLimit==0, Reader.WriteTo allocates a conservative 64KiB scratch buffer.
	// A message larger than that cap must fail with ErrTooLong before attempting payload reads.
	hdr := frameHeader56BE(64*1024 + 1)
	r := fr.NewReader(bytes.NewReader(hdr), fr.WithReadTCP()).(*fr.Reader)

	n, err := r.WriteTo(io.Discard)
	if !errors.Is(err, fr.ErrTooLong) || n != 0 {
		t.Fatalf("want (0, ErrTooLong), got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_Stream_ZeroLengthMessage_Skips(t *testing.T) {
	// One zero-length framed message (header=0), then EOF.
	r := fr.NewReader(bytes.NewReader([]byte{0}), fr.WithReadTCP()).(*fr.Reader)

	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if dst.Len() != 0 {
		t.Fatalf("dst=%q", dst.String())
	}
}

func TestReader_WriteTo_Stream_PropagatesNonSemanticError(t *testing.T) {
	boom := errors.New("boom")
	r := fr.NewReader(errReader{err: boom}, fr.WithReadTCP()).(*fr.Reader)

	n, err := r.WriteTo(io.Discard)
	if !errors.Is(err, boom) || n != 0 {
		t.Fatalf("want (0, boom), got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_Stream_ReadLimitPositive_AllocatesScratchBuffer(t *testing.T) {
	// Cover the ReadLimit>0 allocation path for the internal scratch buffer.
	wire := []byte{1, 'a'}
	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithReadLimit(16)).(*fr.Reader)

	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if dst.String() != "a" {
		t.Fatalf("dst=%q", dst.String())
	}
}

func TestReader_WriteTo_Stream_UnexpectedEOF_DuringPayload(t *testing.T) {
	// Declares 3 bytes payload, only provides 2, then EOF.
	wire := []byte{3, 'a', 'b'}
	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP()).(*fr.Reader)

	n, err := r.WriteTo(io.Discard)
	if !errors.Is(err, io.ErrUnexpectedEOF) || n != 0 {
		t.Fatalf("want (0, io.ErrUnexpectedEOF), got (%d, %v)", n, err)
	}
}

type dstErrWriter struct{ err error }

func (w dstErrWriter) Write([]byte) (int, error) { return 0, w.err }

func TestReader_WriteTo_Stream_DstError_Propagates(t *testing.T) {
	boom := errors.New("boom")
	// One message "a" in stream wire.
	r := fr.NewReader(bytes.NewReader([]byte{1, 'a'}), fr.WithReadTCP()).(*fr.Reader)

	n, err := r.WriteTo(dstErrWriter{err: boom})
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("want (0, boom), got (%d, %v)", n, err)
	}
}
