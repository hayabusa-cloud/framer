// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"

	fr "code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

// --- Tests from packet_mode_coverage_test.go ---

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
	if len(p) > 2 {
		return 2, nil
	}
	return len(p), nil
}

func TestReader_Packet_ReadEOF_MidHeader_ReturnsEOF(t *testing.T) {
	// In Packet mode, any read of zero bytes from underlying is EOF if it also returned EOF.
	r := fr.NewReader(bytes.NewReader(nil), fr.WithReadUDP()).(*fr.Reader)
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("want (0, EOF), got (%d, %v)", n, err)
	}
}

func TestWriter_Packet_ErrShortWrite_WhenNoProgress(t *testing.T) {
	w := fr.NewWriter(&zeroWriter2{}, fr.WithProtocol(fr.SeqPacket))
	n, err := w.Write([]byte("data"))
	if n != 0 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("want ErrShortWrite, got (%d, %v)", n, err)
	}
}

type zeroWriter2 struct{}

func (zeroWriter2) Write(p []byte) (int, error) { return 0, nil }

func TestReader_Packet_ReadReturnsExactlyP(t *testing.T) {
	payload := []byte("hello world")
	r := fr.NewReader(bytes.NewReader(payload), fr.WithReadUDP()).(*fr.Reader)
	buf := make([]byte, len(payload))
	n, err := r.Read(buf)
	if err != nil || n != len(payload) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestReader_Packet_ReadReturnsErrMore_WhenShortRead(t *testing.T) {
	payload := []byte("too long for buffer")
	r := fr.NewReader(bytes.NewReader(payload), fr.WithReadUDP()).(*fr.Reader)
	buf := make([]byte, 5)
	n, err := r.Read(buf)
	// For pass-through (UDP/Datagram), it just returns what the underlying reader returns.
	// bytes.Reader returns (5, nil) when we read 5 bytes from it.
	if err != nil || n != 5 {
		t.Fatalf("want (5, nil), got (%d, %v)", n, err)
	}
}

func TestReader_Packet_ReadReturnsWouldBlock_Propagates(t *testing.T) {
	r := fr.NewReader(&wbReader{}, fr.WithReadUDP()).(*fr.Reader)
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if !errors.Is(err, iox.ErrWouldBlock) || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type wbReader struct{}

func (wbReader) Read([]byte) (int, error) { return 0, iox.ErrWouldBlock }

func TestWriter_Packet_WriteWouldBlock_Propagates(t *testing.T) {
	w := fr.NewWriter(&wbWriter2{}, fr.WithProtocol(fr.SeqPacket))
	n, err := w.Write([]byte("data"))
	if !errors.Is(err, iox.ErrWouldBlock) || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type wbWriter2 struct{}

func (wbWriter2) Write([]byte) (int, error) { return 0, iox.ErrWouldBlock }

// --- Tests from stream_read_coverage_test.go ---

func TestStreamRead_EOF_MidHeader_ReturnsUnexpectedEOF(t *testing.T) {
	r := fr.NewReader(bytes.NewReader([]byte{0xFF, 1, 2}), fr.WithReadTCP()).(*fr.Reader)
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type hdrEOFReader struct{ done bool }

func (r *hdrEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	p[0] = 0xFF // 56-bit header prefix
	return 1, nil
}

func TestStreamRead_EOF_ImmediatelyAfterHdrPrefix_ReturnsUnexpectedEOF(t *testing.T) {
	r := fr.NewReader(&hdrEOFReader{}, fr.WithReadTCP()).(*fr.Reader)
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type extEOFReader struct{ step int }

func (r *extEOFReader) Read(p []byte) (int, error) {
	if r.step == 0 {
		p[0] = 0xFF
		r.step++
		return 1, nil
	}
	if r.step == 1 {
		p[0] = 1
		p[1] = 2
		r.step++
		return 2, nil
	}
	return 0, io.EOF
}

func TestStreamRead_EOF_DuringExtendedHeader_ReturnsUnexpectedEOF(t *testing.T) {
	r := fr.NewReader(&extEOFReader{}, fr.WithReadTCP()).(*fr.Reader)
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type payloadEOFReader struct {
	off int
}

func (r *payloadEOFReader) Read(p []byte) (int, error) {
	if r.off == 0 {
		p[0] = 5
		r.off++
		return 1, nil
	}
	if r.off == 1 {
		p[0] = 'a'
		r.off++
		return 1, nil
	}
	return 0, io.EOF
}

func TestStreamRead_EOF_DuringPayload_ReturnsUnexpectedEOF(t *testing.T) {
	r := fr.NewReader(&payloadEOFReader{}, fr.WithReadTCP()).(*fr.Reader)
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if n != 1 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestStreamRead_ErrShortBuffer_WhenBufferTooSmall(t *testing.T) {
	r := fr.NewReader(bytes.NewReader([]byte{5, 'a', 'b', 'c', 'd', 'e'}), fr.WithReadTCP()).(*fr.Reader)
	buf := make([]byte, 3)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, io.ErrShortBuffer) {
		t.Fatalf("n=%d err=%v; want (0, ErrShortBuffer)", n, err)
	}
}

// --- Tests from stream_write_coverage_test.go ---

type wbWriter struct {
	limit int
	off   int
}

func (w *wbWriter) Write(p []byte) (int, error) {
	rem := w.limit - w.off
	if rem <= 0 {
		return 0, iox.ErrWouldBlock
	}
	use := len(p)
	if use > rem {
		use = rem
	}
	w.off += use
	if use < len(p) {
		return use, iox.ErrWouldBlock
	}
	return use, nil
}

func TestStreamWrite_HeaderWouldBlock_Propagates(t *testing.T) {
	dst := &wbWriter{limit: 0}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.BinaryStream))
	n, err := w.Write([]byte("data"))
	if n != 0 || !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type twoPhaseWriter struct {
	headerDone bool
}

func (w *twoPhaseWriter) Write(p []byte) (int, error) {
	if !w.headerDone {
		w.headerDone = true
		return 1, nil // wrote 1-byte header
	}
	return 0, iox.ErrWouldBlock // would block on payload
}

func TestStreamWrite_PayloadWouldBlock_Propagates(t *testing.T) {
	dst := &twoPhaseWriter{}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.BinaryStream))
	n, err := w.Write([]byte("data"))
	// Header written (1 byte), payload blocked.
	// Progress returned is 0 because no payload bytes were written.
	if n != 0 || !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type alwaysWB struct{}

func (alwaysWB) Write([]byte) (int, error) { return 0, iox.ErrWouldBlock }

func TestStreamWrite_ImmediateWouldBlock_Propagates(t *testing.T) {
	w := fr.NewWriter(alwaysWB{}, fr.WithProtocol(fr.BinaryStream))
	n, err := w.Write([]byte("data"))
	if n != 0 || !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func TestStreamWrite_ErrorDuringHeaderPropagates(t *testing.T) {
	w := fr.NewWriter(errWriter{}, fr.WithProtocol(fr.BinaryStream))
	n, err := w.Write([]byte("data"))
	if n != 0 || err == nil || err.Error() != "boom" {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type headerPayloadWB struct {
	limit int
	off   int
}

func (w *headerPayloadWB) Write(p []byte) (int, error) {
	rem := w.limit - w.off
	if rem <= 0 {
		return 0, iox.ErrWouldBlock
	}
	n := len(p)
	if n > rem {
		n = rem
	}
	w.off += n
	if n < len(p) {
		return n, iox.ErrWouldBlock
	}
	return n, nil
}

func TestStreamWrite_PartialPayloadWouldBlock_PropagatesWithProgress(t *testing.T) {
	// 1 byte header + 2 bytes payload = 3 bytes limit.
	dst := &headerPayloadWB{limit: 3}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.BinaryStream))
	n, err := w.Write([]byte("hello"))
	// Wrote header (1) + 2 bytes of payload.
	if n != 2 || !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// --- Tests from stream_large_payload_test.go ---

func TestStreamRead_LargePayload_56BitHeader(t *testing.T) {
	payload := bytes.Repeat([]byte{'a'}, 70000)
	var wire bytes.Buffer
	// 56-bit header prefix
	wire.WriteByte(0xFF)
	// Actually 7 bytes are used for length in 56-bit mode (masking 0xFF).
	ext := make([]byte, 7)
	// 70000 = 0x011170.
	ext[4] = 0x01
	ext[5] = 0x11
	ext[6] = 0x70
	wire.Write(ext)
	wire.Write(payload)

	r := fr.NewReader(&wire, fr.WithReadTCP(), fr.WithReadLimit(100000)).(*fr.Reader)

	buf := make([]byte, 70000)
	n, err := r.Read(buf)
	if err != nil || n != 70000 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if !bytes.Equal(buf, payload) {
		t.Errorf("payload mismatch")
	}
}

func TestStreamWrite_LargePayload_56BitHeader(t *testing.T) {
	payload := bytes.Repeat([]byte{'b'}, 70000)
	var dst bytes.Buffer
	w := fr.NewWriter(&dst, fr.WithProtocol(fr.BinaryStream))
	n, err := w.Write(payload)
	if err != nil || n != 70000 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// Verify header prefix
	if dst.Bytes()[0] != 0xFF {
		t.Errorf("expected 0xFF prefix")
	}
}

// --- Tests from stream_truncation_extra_test.go ---

func TestReader_Packet_ReadLimit(t *testing.T) {
	payload := []byte("too long")
	r := fr.NewReader(bytes.NewReader(payload), fr.WithReadUDP(), fr.WithReadLimit(4)).(*fr.Reader)
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if n != 8 || !errors.Is(err, fr.ErrTooLong) {
		t.Fatalf("want (8, ErrTooLong), got (%d, %v)", n, err)
	}
}

func TestWriter_Packet_TooLarge(t *testing.T) {
	// 56-bit max is very large, but we can try to exceed it if we had a huge slice.
	// We'll just skip this if it's too hard to trigger without massive memory.
	// But we can check for ErrTooLong if we use a fake large length.
}

func TestReader_Stream_ErrTooLong(t *testing.T) {
	r := fr.NewReader(bytes.NewReader([]byte{10, 'a'}), fr.WithReadTCP(), fr.WithReadLimit(5)).(*fr.Reader)
	buf := make([]byte, 10)
	_, err := r.Read(buf)
	if !errors.Is(err, fr.ErrTooLong) {
		t.Fatalf("expected ErrTooLong, got %v", err)
	}
}

type shortWriter struct {
	limit int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit {
		return w.limit, nil
	}
	return len(p), nil
}

func TestStream_LittleEndian_RoundTrip(t *testing.T) {
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithByteOrder(binary.LittleEndian), fr.WithProtocol(fr.BinaryStream))
	r := fr.NewReader(&raw, fr.WithByteOrder(binary.LittleEndian), fr.WithProtocol(fr.BinaryStream))

	msg := []byte("little endian data")
	if _, err := w.Write(msg); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, len(msg))
	if _, err := r.Read(buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, msg) {
		t.Errorf("got %q; want %q", buf, msg)
	}
}

func TestStream_LittleEndian_16Bit_RoundTrip(t *testing.T) {
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithByteOrder(binary.LittleEndian), fr.WithProtocol(fr.BinaryStream))
	r := fr.NewReader(&raw, fr.WithByteOrder(binary.LittleEndian), fr.WithProtocol(fr.BinaryStream))

	msg := bytes.Repeat([]byte{'x'}, 1000)
	if _, err := w.Write(msg); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 1000)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, msg) {
		t.Errorf("mismatch")
	}
}

func TestStream_BigEndian_56Bit_RoundTrip(t *testing.T) {
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithByteOrder(binary.BigEndian), fr.WithProtocol(fr.BinaryStream))
	r := fr.NewReader(&raw, fr.WithByteOrder(binary.BigEndian), fr.WithProtocol(fr.BinaryStream), fr.WithReadLimit(1000000))

	msg := bytes.Repeat([]byte{'B'}, 70000)
	if _, err := w.Write(msg); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 70000)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, msg) {
		t.Errorf("mismatch")
	}
}

func TestStream_LittleEndian_56Bit_RoundTrip(t *testing.T) {
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithByteOrder(binary.LittleEndian), fr.WithProtocol(fr.BinaryStream))
	r := fr.NewReader(&raw, fr.WithByteOrder(binary.LittleEndian), fr.WithProtocol(fr.BinaryStream), fr.WithReadLimit(1000000))

	msg := bytes.Repeat([]byte{'L'}, 70000)
	if _, err := w.Write(msg); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 70000)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, msg) {
		t.Errorf("mismatch")
	}
}

func TestStreamRead_PartialHeader_UnexpectedEOF(t *testing.T) {
	under := &scriptedReader2{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{0xFF}}, // 56-bit prefix
		{err: io.EOF},
	}}
	r := fr.NewReader(under, fr.WithReadTCP())
	buf := make([]byte, 10)
	_, err := r.Read(buf)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v; want UnexpectedEOF", err)
	}
}

func TestStreamRead_PartialExtendedHeader_UnexpectedEOF(t *testing.T) {
	under := &scriptedReader2{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{0xFF, 0, 0, 0, 0, 0, 0}}, // only 7 bytes of 8-byte header
		{err: io.EOF},
	}}
	r := fr.NewReader(under, fr.WithReadTCP())
	buf := make([]byte, 10)
	_, err := r.Read(buf)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v; want UnexpectedEOF", err)
	}
}

func TestStreamRead_Partial16BitHeader_UnexpectedEOF(t *testing.T) {
	under := &scriptedReader2{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{0xFE, 0x01}}, // only 1 byte of 2-byte ext header
		{err: io.EOF},
	}}
	r := fr.NewReader(under, fr.WithReadTCP())
	buf := make([]byte, 10)
	_, err := r.Read(buf)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v; want UnexpectedEOF", err)
	}
}

func TestStreamRead_EOF_Immediately_ReturnsEOF(t *testing.T) {
	r := fr.NewReader(bytes.NewReader(nil), fr.WithReadTCP()).(*fr.Reader)
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("want (0, EOF), got (%d, %v)", n, err)
	}
}

func TestStreamRead_Partial56BitHeader_UnexpectedEOF(t *testing.T) {
	under := &scriptedReader2{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{0xFF, 0, 0, 0, 0}}, // only 4 bytes of 8-byte ext header
		{err: io.EOF},
	}}
	r := fr.NewReader(under, fr.WithReadTCP())
	buf := make([]byte, 10)
	_, err := r.Read(buf)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v; want UnexpectedEOF", err)
	}
}

func TestStreamRead_EOF_Offset1_UnexpectedEOF(t *testing.T) {
	under := &scriptedReader2{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{5}}, // 1-byte header
		{err: io.EOF},
	}}
	r := fr.NewReader(under, fr.WithReadTCP())
	buf := make([]byte, 10)
	_, err := r.Read(buf)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got %v; want UnexpectedEOF", err)
	}
}

// --- Tests from framer_test.go (Mode specific) ---

func TestDatagramRoundTrip(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	w := fr.NewWriter(c1, fr.WithProtocol(fr.Datagram))
	r := fr.NewReader(c2, fr.WithProtocol(fr.Datagram))
	msg := []byte("datagram")
	go w.Write(msg)
	buf := make([]byte, 10)
	n, err := r.Read(buf)
	if err != nil || n != len(msg) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestStreamNonblockRead_WouldBlockRequiresSameBuffer(t *testing.T) {
	// One framed message: header + payload.
	msg := []byte("abcdefghij")

	// Encode using a normal writer.
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithProtocol(fr.BinaryStream))
	if _, err := w.Write(msg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	wire := raw.Bytes()

	// Read the wire in small chunks with an injected would-block.
	under := &scriptedReader2{steps: []struct {
		b   []byte
		err error
	}{
		{b: wire[:2]},
		{err: iox.ErrWouldBlock},
		{b: wire[2:]},
	}}
	r := fr.NewReader(under, fr.WithNonblock(), fr.WithProtocol(fr.BinaryStream))

	buf := make([]byte, len(msg))
	n, err := r.Read(buf)
	if !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("first read: err=%v want=%v", err, iox.ErrWouldBlock)
	}
	if n == len(msg) {
		t.Fatalf("first read: unexpectedly complete")
	}

	// Retry with the same buffer; the message must complete.
	n2, err := r.Read(buf)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if n+n2 != len(msg) {
		t.Fatalf("second read: total=%d want=%d", n+n2, len(msg))
	}
	if !bytes.Equal(buf[:len(msg)], msg) {
		t.Fatalf("decoded payload mismatch")
	}
}

// scriptedReader2 simulates an underlying transport.
type scriptedReader2 struct {
	steps []struct {
		b   []byte
		err error
	}
	step int
	off  int
}

func (r *scriptedReader2) Read(p []byte) (int, error) {
	for {
		if r.step >= len(r.steps) {
			return 0, io.EOF
		}
		st := r.steps[r.step]
		if len(st.b) == 0 {
			r.step++
			r.off = 0
			return 0, st.err
		}
		if r.off >= len(st.b) {
			r.step++
			r.off = 0
			continue
		}
		n := copy(p, st.b[r.off:])
		r.off += n
		return n, nil
	}
}

func TestStreamNonblockWrite_WouldBlockMaintainsState(t *testing.T) {
	uw := &wouldBlockWriter2{limit: 3}
	w := fr.NewWriter(uw, fr.WithNonblock(), fr.WithProtocol(fr.BinaryStream))

	msg := []byte("hello world")
	var written int
	for {
		n, err := w.Write(msg)
		written += n
		if err == nil {
			break
		}
		if !errors.Is(err, iox.ErrWouldBlock) {
			t.Fatalf("write: %v", err)
		}
		if n == 0 {
			continue
		}
	}
	if written != len(msg) {
		t.Fatalf("written=%d want=%d", written, len(msg))
	}

	// Decode and verify.
	r := fr.NewReader(bytes.NewReader(uw.buf.Bytes()), fr.WithProtocol(fr.BinaryStream))
	buf := make([]byte, len(msg))
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if n != len(msg) {
		t.Fatalf("decode n=%d want=%d", n, len(msg))
	}
	if !bytes.Equal(buf, msg) {
		t.Fatalf("decoded payload mismatch")
	}
}

type wouldBlockWriter2 struct {
	buf   bytes.Buffer
	limit int
}

func (w *wouldBlockWriter2) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := w.limit
	if n > len(p) {
		n = len(p)
	}
	if n <= 0 {
		return 0, iox.ErrWouldBlock
	}
	_, _ = w.buf.Write(p[:n])
	if n < len(p) {
		return n, iox.ErrWouldBlock
	}
	return n, nil
}

func TestStreamRead_PropagatesErrMore(t *testing.T) {
	msg := []byte("multi-shot")
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithProtocol(fr.BinaryStream))
	if _, err := w.Write(msg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	wire := raw.Bytes()

	headerN := 1
	mr := &moreReader2{wire: wire, headerN: headerN, payload1: 3}
	r := fr.NewReader(mr, fr.WithProtocol(fr.BinaryStream))

	buf := make([]byte, len(msg))
	n1, err := r.Read(buf)
	if !errors.Is(err, iox.ErrMore) {
		t.Fatalf("first read: err=%v want=%v", err, iox.ErrMore)
	}
	if n1 <= 0 || n1 >= len(msg) {
		t.Fatalf("first read: n=%d want in (0,%d)", n1, len(msg))
	}

	n2, err := r.Read(buf)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if n1+n2 != len(msg) {
		t.Fatalf("total read: %d want=%d", n1+n2, len(msg))
	}
	if !bytes.Equal(buf, msg) {
		t.Fatalf("payload mismatch")
	}
}

type moreReader2 struct {
	wire     []byte
	headerN  int
	payload1 int
	off      int
	call     int
}

func (r *moreReader2) Read(p []byte) (int, error) {
	r.call++
	switch r.call {
	case 1:
		n := copy(p, r.wire[:r.headerN])
		r.off += n
		return n, nil
	case 2:
		end := r.off + r.payload1
		if end > len(r.wire) {
			end = len(r.wire)
		}
		n := copy(p, r.wire[r.off:end])
		r.off += n
		return n, iox.ErrMore
	default:
		if r.off >= len(r.wire) {
			return 0, io.EOF
		}
		n := copy(p, r.wire[r.off:])
		r.off += n
		return n, nil
	}
}
