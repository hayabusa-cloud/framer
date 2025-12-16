package framer_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
)

// mkWire encodes a single payload using given byte order and BinaryStream.
func mkWire(t *testing.T, order binary.ByteOrder, payload []byte) []byte {
	t.Helper()
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithByteOrder(order), fr.WithProtocol(fr.BinaryStream))
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return raw.Bytes()
}

func TestStreamRead_AllHeaderModes_BE(t *testing.T) {
	sizes := []int{5, 260, 65536}
	for _, sz := range sizes {
		payload := bytes.Repeat([]byte{0xAB}, sz)
		wire := mkWire(t, binary.BigEndian, payload)
		r := fr.NewReader(bytes.NewReader(wire), fr.WithByteOrder(binary.BigEndian), fr.WithProtocol(fr.BinaryStream))
		buf := make([]byte, sz)
		n, err := r.Read(buf)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if n != sz {
			t.Fatalf("n=%d want=%d", n, sz)
		}
		if !bytes.Equal(buf, payload) {
			t.Fatalf("payload mismatch for size %d", sz)
		}
	}
}

func TestStreamRead_Header7_LE(t *testing.T) {
	sz := 65536
	payload := bytes.Repeat([]byte{0xCD}, sz)
	wire := mkWire(t, binary.LittleEndian, payload)
	r := fr.NewReader(bytes.NewReader(wire), fr.WithByteOrder(binary.LittleEndian), fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, sz)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if n != sz || !bytes.Equal(buf, payload) {
		t.Fatalf("roundtrip failed for LE 7-byte header")
	}
}

func TestStreamRead_EOFAtMessageBoundary(t *testing.T) {
	// EOF with no bytes read = clean end of stream (no more messages).
	under := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{{b: nil, err: io.EOF}}}
	r := fr.NewReader(under, fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, 1)
	if _, err := r.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("err=%v want=%v", err, io.EOF)
	}
}

func TestStreamRead_EOFInExtendedLength(t *testing.T) {
	// Header requires 2-byte ext, but provide only header then EOF.
	hdr := []byte{254}
	under := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{{b: hdr}, {b: nil, err: io.EOF}}}
	r := fr.NewReader(under, fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, 10)
	if _, err := r.Read(buf); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v want=%v", err, io.ErrUnexpectedEOF)
	}
}

func TestStreamRead_EOFInPayload(t *testing.T) {
	payload := bytes.Repeat([]byte{'x'}, 64)
	wire := mkWire(t, binary.BigEndian, payload)
	// Truncate payload bytes to simulate EOF mid-payload.
	cut := len(wire) - 10
	under := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{{b: wire[:cut]}, {b: nil, err: io.EOF}}}
	r := fr.NewReader(under, fr.WithByteOrder(binary.BigEndian), fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, len(payload))
	n, err := r.Read(buf)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v want %v", err, io.ErrUnexpectedEOF)
	}
	if n == len(payload) {
		t.Fatalf("unexpectedly read full payload")
	}
}

func TestStreamRead_ShortDestinationBuffer(t *testing.T) {
	payload := bytes.Repeat([]byte{'y'}, 32)
	wire := mkWire(t, binary.BigEndian, payload)
	r := fr.NewReader(bytes.NewReader(wire), fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, 16) // shorter than length
	if _, err := r.Read(buf); !errors.Is(err, io.ErrShortBuffer) {
		t.Fatalf("err=%v want %v", err, io.ErrShortBuffer)
	}
}

func TestStreamRead_ReadLimitExceeded(t *testing.T) {
	payload := bytes.Repeat([]byte{'z'}, 40)
	wire := mkWire(t, binary.BigEndian, payload)
	r := fr.NewReader(bytes.NewReader(wire), fr.WithProtocol(fr.BinaryStream), fr.WithReadLimit(20))
	buf := make([]byte, len(payload))
	if _, err := r.Read(buf); !errors.Is(err, fr.ErrTooLong) {
		t.Fatalf("err=%v want ErrTooLong", err)
	}
}

func TestStreamRead_ErrorDuringHeaderPropagates(t *testing.T) {
	boom := errors.New("boom")
	under := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{{b: nil, err: boom}}}
	r := fr.NewReader(under, fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, 8)
	if _, err := r.Read(buf); !errors.Is(err, boom) {
		t.Fatalf("err=%v want boom", err)
	}
}

func TestStreamRead_ErrorDuringExtendedLengthPropagates(t *testing.T) {
	boom := errors.New("boom")
	// Header indicates 2-byte extended length, then error on ext read.
	hdr := []byte{254}
	under := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{{b: hdr}, {b: nil, err: boom}}}
	r := fr.NewReader(under, fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, 8)
	if _, err := r.Read(buf); !errors.Is(err, boom) {
		t.Fatalf("err=%v want boom", err)
	}
}

func TestStreamRead_EOFExactlyAfterHeader_ZeroLength(t *testing.T) {
	// header=0 (len=0), then EOF; should succeed with n=0, nil error.
	under := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{{b: []byte{0}}, {b: nil, err: io.EOF}}}
	r := fr.NewReader(under, fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, 1)
	n, err := r.Read(buf)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v want 0,nil", n, err)
	}
}

func TestStreamRead_EOFExactlyAfterExtLen_ZeroLength(t *testing.T) {
	// header=0xFE ext=0x0000 (len=0), then EOF; should succeed with n=0, nil.
	hdr := []byte{254, 0, 0}
	under := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{{b: hdr}, {b: nil, err: io.EOF}}}
	r := fr.NewReader(under, fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, 1)
	n, err := r.Read(buf)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v want 0,nil", n, err)
	}
}

func TestStreamRead_EOFExactlyAtEndOfPayload(t *testing.T) {
	payload := bytes.Repeat([]byte{'q'}, 17)
	wire := mkWire(t, binary.BigEndian, payload)
	under := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{{b: wire}, {b: nil, err: io.EOF}}}
	r := fr.NewReader(under, fr.WithProtocol(fr.BinaryStream), fr.WithByteOrder(binary.BigEndian))
	buf := make([]byte, len(payload))
	n, err := r.Read(buf)
	if err != nil || n != len(payload) || !bytes.Equal(buf, payload) {
		t.Fatalf("boundary EOF failed: n=%d err=%v", n, err)
	}
}

// Custom readers to trigger (n>0, io.EOF) inside specific phases.
type hdrEOFReader struct{ done bool }

func (r *hdrEOFReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		if len(p) > 0 {
			p[0] = 0
		}
		return 1, io.EOF
	}
	return 0, io.EOF
}

type extEOFReader struct{ step int }

func (r *extEOFReader) Read(p []byte) (n int, err error) {
	switch r.step {
	case 0:
		if len(p) > 0 {
			p[0] = 254
		}
		r.step = 1
		return 1, nil
	case 1:
		if len(p) >= 2 {
			p[0], p[1] = 0, 0
		}
		r.step = 2
		return 2, io.EOF
	default:
		return 0, io.EOF
	}
}

type payloadEOFReader struct {
	wire []byte
	step int
}

func (r *payloadEOFReader) Read(p []byte) (int, error) {
	if r.step == 0 {
		// return only the header byte
		if len(p) == 0 {
			return 0, nil
		}
		p[0] = r.wire[0]
		r.step = 1
		return 1, nil
	}
	// payload all at once with EOF
	n := copy(p, r.wire[1:])
	return n, io.EOF
}

func TestStreamRead_HeaderEOFBreaks(t *testing.T) {
	r := fr.NewReader(&hdrEOFReader{}, fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, 1)
	if n, err := r.Read(buf); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestStreamRead_ExtEOFBreaks(t *testing.T) {
	r := fr.NewReader(&extEOFReader{}, fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, 1)
	if n, err := r.Read(buf); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestStreamRead_PayloadEOFBreaks(t *testing.T) {
	// small payload so header is 1 byte
	wire := mkWire(t, binary.BigEndian, []byte("abc"))
	r := fr.NewReader(&payloadEOFReader{wire: wire}, fr.WithProtocol(fr.BinaryStream), fr.WithByteOrder(binary.BigEndian))
	buf := make([]byte, 3)
	if n, err := r.Read(buf); err != nil || n != 3 || !bytes.Equal(buf, []byte("abc")) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
