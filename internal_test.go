//go:build amd64 || arm64 || ppc64le || ppc64 || s390x || riscv64 || loong64 || mips64le || mips64
// +build amd64 arm64 ppc64le ppc64 s390x riscv64 loong64 mips64le mips64

// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"

	"code.hybscloud.com/iox"
)

// --- Internal helpers ---

// scriptedReader simulates an underlying transport.
type scriptedReader struct {
	steps []struct {
		b   []byte
		err error
	}
	// current step number
	step int
	// offset into the buffer for current step
	off int
}

func (r *scriptedReader) Read(p []byte) (int, error) {
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

// --- Tests from forward_internal_coverage_test.go ---

func TestForwarder_Packet_ReadLimitLessThanInternalBuffer_AdjustsMax(t *testing.T) {
	f := NewForwarder(io.Discard, bytes.NewReader([]byte("abcd")), WithProtocol(SeqPacket), WithReadLimit(8))
	f.buf = make([]byte, 64)

	if _, err := f.ForwardOnce(); err != nil {
		t.Fatalf("err=%v", err)
	}
}

func TestForwarder_DefensiveInvalidState_ReturnsZeroNil(t *testing.T) {
	f := NewForwarder(io.Discard, bytes.NewReader(nil), WithProtocol(SeqPacket))
	f.state = 3

	n, err := f.ForwardOnce()
	if n != 0 || err != nil {
		t.Fatalf("want (0, nil), got (%d, %v)", n, err)
	}
}

func TestForwarder_Stream_DefensiveEOFInPayloadPhase_ReturnsUnexpectedEOF(t *testing.T) {
	f := NewForwarder(io.Discard, bytes.NewReader(nil), WithProtocol(BinaryStream))
	f.state = 1
	f.need = 8
	f.got = 3
	f.rr.offset = 0

	n, err := f.ForwardOnce()
	if n != f.got || err != io.ErrUnexpectedEOF {
		t.Fatalf("want (%d, io.ErrUnexpectedEOF), got (%d, %v)", f.got, n, err)
	}
}

func TestForwarder_Stream_MsgExceedsBuf_ReturnsShortBuffer(t *testing.T) {
	wire := []byte{20, 'a'} // 20 byte payload
	f := NewForwarder(io.Discard, bytes.NewReader(wire), WithProtocol(BinaryStream))
	f.buf = make([]byte, 10) // manually shrink buffer
	n, err := f.ForwardOnce()
	if !errors.Is(err, io.ErrShortBuffer) || n != 0 {
		t.Fatalf("got (%d, %v); want (0, ErrShortBuffer)", n, err)
	}
}

// --- Tests from netopts_internal_default_test.go ---

func TestDefaultsFor_DefaultBranch(t *testing.T) {
	p, bo := defaultsFor(netKind(255))
	if p != BinaryStream || bo != binary.BigEndian {
		t.Fatalf("unexpected defaults: p=%v bo=%T", p, bo)
	}
}

// --- Tests from readerfrom_defensive_internal_test.go ---

type oneReadSrc struct {
	done bool
}

func (s *oneReadSrc) Read(p []byte) (int, error) {
	if s.done {
		return 0, io.EOF
	}
	s.done = true
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = 'x'
	return 1, nil
}

func TestWriter_ReadFrom_DefensiveShortWriteWhenInternalStateAlreadyComplete(t *testing.T) {
	fr := &framer{wr: io.Discard, wbo: binary.BigEndian, wpr: BinaryStream}
	fr.length = 1
	fr.offset = 2

	w := &Writer{fr: fr}
	_, err := w.ReadFrom(&oneReadSrc{})
	if err != io.ErrShortWrite {
		t.Fatalf("err=%v want io.ErrShortWrite", err)
	}
}

// --- Tests from alloc_fastpath_test.go (converted to package framer) ---

type fixedSink struct {
	b   []byte
	off int
}

func (s *fixedSink) Write(p []byte) (int, error) {
	n := copy(s.b[s.off:], p)
	s.off += n
	if n < len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

func TestAllocs_Reader_WriteTo_Stream(t *testing.T) {
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{4}, err: nil},
		{b: []byte("DATA"), err: io.EOF},
	}}
	r := &Reader{fr: newFramer(sr, nil, WithReadTCP())}

	_, _ = r.WriteTo(io.Discard)

	allocs := testing.AllocsPerRun(1000, func() {
		sr.step, sr.off = 0, 0
		_, _ = r.WriteTo(io.Discard)
	})
	if allocs != 0 {
		t.Fatalf("allocs/op = %v want 0", allocs)
	}
}

func TestAllocs_Reader_WriteTo_StreamZeroLengthFirstUse(t *testing.T) {
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{0}, err: io.EOF},
	}}
	r := &Reader{fr: newFramer(sr, nil, WithReadTCP())}

	var lastN int64
	var lastErr error
	allocs := testing.AllocsPerRun(1000, func() {
		sr.step, sr.off = 0, 0
		r.fr.reset()
		clearWriteToPending(r.fr)
		r.fr.rbuf = nil
		lastN, lastErr = r.WriteTo(io.Discard)
	})
	if allocs != 0 {
		t.Fatalf("allocs/op = %v want 0", allocs)
	}
	if lastN != 0 || lastErr != nil {
		t.Fatalf("WriteTo: got (%d, %v), want (0, nil)", lastN, lastErr)
	}
	if r.fr.rbuf != nil {
		t.Fatalf("rbuf allocated for zero-length stream message")
	}
}

func TestReaderWriteToStreamZeroLengthFrameDestinationNoSourceScratch(t *testing.T) {
	src := &Reader{fr: newFramer(bytes.NewReader([]byte{0}), nil, WithReadTCP())}
	var raw bytes.Buffer
	dst := &Writer{fr: newFramer(nil, &raw, WithWriteTCP())}

	n, err := src.WriteTo(dst)
	if n != 0 || err != nil {
		t.Fatalf("WriteTo: got (%d, %v), want (0, nil)", n, err)
	}
	if src.fr.rbuf != nil {
		t.Fatalf("source rbuf allocated for zero-length frame emission")
	}
}

func TestAllocs_Reader_WriteTo_PacketControlPendingFirstUse(t *testing.T) {
	sourceErr := errors.New("source frontier")
	r := &Reader{fr: newFramer(bytes.NewReader(nil), nil, WithReadUDP())}

	var lastN int64
	var lastErr error
	allocs := testing.AllocsPerRun(1000, func() {
		r.fr.rbuf = nil
		saveWriteToControl(r.fr, sourceErr)
		lastN, lastErr = r.WriteTo(io.Discard)
	})
	if allocs != 0 {
		t.Fatalf("allocs/op = %v want 0", allocs)
	}
	if lastN != 0 || !errors.Is(lastErr, sourceErr) {
		t.Fatalf("WriteTo: got (%d, %v), want (0, sourceErr)", lastN, lastErr)
	}
	if r.fr.rbuf != nil {
		t.Fatalf("rbuf allocated for control-only pending packet state")
	}
}

func TestAllocs_Reader_WriteTo_WouldBlock(t *testing.T) {
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{4}, err: nil},
		{b: []byte("DA"), err: iox.ErrWouldBlock},
		{b: []byte("TA"), err: io.EOF},
	}}
	r := &Reader{fr: newFramer(sr, nil, WithReadTCP())}
	_, _ = r.WriteTo(io.Discard)

	allocs := testing.AllocsPerRun(1000, func() {
		sr.step, sr.off = 0, 0
		_, _ = r.WriteTo(io.Discard)
	})
	if allocs != 0 {
		t.Fatalf("allocs/op = %v want 0", allocs)
	}
}

func TestAllocs_Writer_ReadFrom_Stream(t *testing.T) {
	sink := &fixedSink{b: make([]byte, 128)}
	w := &Writer{fr: newFramer(nil, sink, WithWriteTCP())}

	payload := make([]byte, 32)
	for i := range payload {
		payload[i] = byte('a' + (i % 26))
	}
	src := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: payload, err: io.EOF},
	}}

	_, _ = w.ReadFrom(&scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: nil, err: io.EOF},
	}})

	allocs := testing.AllocsPerRun(1000, func() {
		sink.off = 0
		src.step, src.off = 0, 0
		_, _ = w.ReadFrom(src)
	})
	if allocs != 0 {
		t.Fatalf("allocs/op = %v want 0", allocs)
	}
}

// --- Tests from internal_retry_test.go (converted to package framer) ---

type stepReader struct {
	payload []byte
	off     int
	called  bool
}

func (r *stepReader) Read(p []byte) (int, error) {
	if !r.called {
		r.called = true
		return 0, iox.ErrWouldBlock
	}
	if r.off >= len(r.payload) {
		return 0, io.EOF
	}
	n := copy(p, r.payload[r.off:])
	r.off += n
	return n, nil
}

type stepWriter struct {
	buf    bytes.Buffer
	called bool
}

func (w *stepWriter) Write(p []byte) (int, error) {
	if !w.called {
		w.called = true
		return 0, iox.ErrWouldBlock
	}
	n, _ := w.buf.Write(p)
	return n, nil
}

func TestRetryPolicy_Nonblock_NoRetryOnWouldBlock_Read(t *testing.T) {
	msg := []byte("abc")
	r := &Reader{fr: newFramer(&stepReader{payload: msg}, nil, WithNonblock())}
	buf := make([]byte, len(msg))
	n, err := r.Read(buf)
	if err != iox.ErrWouldBlock {
		t.Fatalf("err=%v want ErrWouldBlock", err)
	}
	if n != 0 {
		t.Fatalf("n=%d want 0", n)
	}
}

func TestRetryPolicy_YieldAndRetry_Read(t *testing.T) {
	msg := []byte("abcdef")
	wire := encodeOneInternal(t, msg)
	r := &Reader{fr: newFramer(&stepReader{payload: wire}, nil, WithBlock())}
	buf := make([]byte, len(msg))
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read err: %v", err)
	}
	if n != len(msg) {
		t.Fatalf("n=%d want %d", n, len(msg))
	}
}

func TestRetryPolicy_SleepAndRetry_Write(t *testing.T) {
	wunder := &stepWriter{}
	w := &Writer{fr: newFramer(nil, wunder, WithRetryDelay(1*time.Microsecond))}
	msg := []byte("hello world")
	n, err := w.Write(msg)
	if err != nil {
		t.Fatalf("write err: %v", err)
	}
	if n != len(msg) {
		t.Fatalf("n=%d want %d", n, len(msg))
	}
	if got := wunder.buf.Bytes(); !bytes.Equal(got, encodeOneInternal(t, msg)) {
		t.Fatalf("wire mismatch")
	}
}

func encodeOneInternal(t *testing.T, payload []byte) []byte {
	t.Helper()
	var raw bytes.Buffer
	w := &Writer{fr: newFramer(nil, &raw)}
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return raw.Bytes()
}

func TestReadStream_LengthGuardViaState(t *testing.T) {
	fr := newFramer(nil, nil)
	// Emulate "header and ext already consumed, do not parse length from header".
	fr.header[0] = 0               // exLen = 0
	fr.offset = frameHeaderLen + 1 // skip the parse-length block
	fr.length = framePayloadMaxLen56 + 1
	buf := make([]byte, 1)
	if _, err := fr.readStream(buf); !errors.Is(err, ErrTooLong) {
		t.Fatalf("err=%v want ErrTooLong", err)
	}
}

// --- Tests from internal_yield_test.go ---

// TestYieldOnce executes the cooperative yield helper.
func TestYieldOnce(t *testing.T) {
	fr := newFramer(nil, nil)
	fr.yieldOnce()
}

// --- Cold path coverage tests ---

// wouldBlockWriter returns ErrWouldBlock on the first write attempt.
type wouldBlockWriter struct {
	calls int
}

func (w *wouldBlockWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		return 0, iox.ErrWouldBlock
	}
	return len(p), nil
}

// errMoreWriter returns ErrMore on the first write attempt.
type errMoreWriter struct {
	calls int
}

func (w *errMoreWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		return len(p), iox.ErrMore
	}
	return len(p), nil
}

// TestWriteTo_Packet_WouldBlockOnWrite covers packet WriteTo write-side suspension.
func TestWriteTo_Packet_WouldBlockOnWrite(t *testing.T) {
	// Packet mode reader with data
	src := bytes.NewReader([]byte("hello"))
	r := &Reader{fr: newFramer(src, nil, WithProtocol(SeqPacket))}

	dst := &wouldBlockWriter{}
	n, err := r.WriteTo(dst)
	if err != iox.ErrWouldBlock {
		t.Fatalf("err=%v want ErrWouldBlock", err)
	}
	if n != 0 {
		t.Fatalf("n=%d want 0", n)
	}
}

// TestWriteTo_Packet_ErrMoreOnWrite covers packet WriteTo write-side continuation.
func TestWriteTo_Packet_ErrMoreOnWrite(t *testing.T) {
	src := bytes.NewReader([]byte("hello"))
	r := &Reader{fr: newFramer(src, nil, WithProtocol(SeqPacket))}

	dst := &errMoreWriter{}
	n, err := r.WriteTo(dst)
	if err != iox.ErrMore {
		t.Fatalf("err=%v want ErrMore", err)
	}
	if n != 5 {
		t.Fatalf("n=%d want 5", n)
	}
}

// TestWriteTo_Stream_WouldBlockOnPayloadRead covers stream payload read suspension.
func TestWriteTo_Stream_WouldBlockOnPayloadRead(t *testing.T) {
	// Create a reader that returns header, then ErrWouldBlock during payload
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{5}, err: nil},         // header: 5-byte payload
		{b: []byte("he"), err: nil},      // partial payload
		{b: nil, err: iox.ErrWouldBlock}, // would-block mid-payload
	}}
	r := &Reader{fr: newFramer(sr, nil, WithProtocol(BinaryStream))}

	n, err := r.WriteTo(io.Discard)
	if err != iox.ErrWouldBlock {
		t.Fatalf("err=%v want ErrWouldBlock", err)
	}
	// Progress should be 0 since we haven't written anything yet (still reading payload)
	if n != 0 {
		t.Fatalf("n=%d want 0", n)
	}
}

// TestWriteTo_Stream_ErrMoreOnPayloadRead covers stream payload read continuation.
func TestWriteTo_Stream_ErrMoreOnPayloadRead(t *testing.T) {
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{5}, err: nil},    // header: 5-byte payload
		{b: []byte("he"), err: nil}, // partial payload
		{b: nil, err: iox.ErrMore},  // ErrMore mid-payload
	}}
	r := &Reader{fr: newFramer(sr, nil, WithProtocol(BinaryStream))}

	n, err := r.WriteTo(io.Discard)
	if err != iox.ErrMore {
		t.Fatalf("err=%v want ErrMore", err)
	}
	if n != 0 {
		t.Fatalf("n=%d want 0", n)
	}
}

// TestWriteTo_Stream_EOFMidPayload covers stream EOF mid-payload handling.
func TestWriteTo_Stream_EOFMidPayload(t *testing.T) {
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{5}, err: nil},    // header: 5-byte payload
		{b: []byte("he"), err: nil}, // partial payload (2 bytes)
		{b: nil, err: io.EOF},       // EOF mid-payload
	}}
	r := &Reader{fr: newFramer(sr, nil, WithProtocol(BinaryStream))}

	n, err := r.WriteTo(io.Discard)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("err=%v want io.ErrUnexpectedEOF", err)
	}
	if n != 0 {
		t.Fatalf("n=%d want 0", n)
	}
}

// TestReadStream_PartialHeaderEOF covers partial-header stream truncation.
func TestReadStream_PartialHeaderEOF(t *testing.T) {
	// Reader that returns first header byte then EOF (simulating truncated stream)
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{0xFE}, err: nil}, // first byte of extended header (indicates 2-byte length follows)
		{b: nil, err: io.EOF},       // EOF before extended length bytes
	}}
	fr := newFramer(sr, nil, WithProtocol(BinaryStream))
	buf := make([]byte, 100)
	_, err := fr.readStream(buf)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("err=%v want io.ErrUnexpectedEOF", err)
	}
}

// TestReadStream_EOFDuringExtendedLength covers EOF while reading extended length bytes.
func TestReadStream_EOFDuringExtendedLength(t *testing.T) {
	// Reader that returns header byte, partial extended length, then EOF with data
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{0xFE, 0x01, 0x00}, err: io.EOF}, // header + 2-byte length (256) + EOF together
	}}
	fr := newFramer(sr, nil, WithProtocol(BinaryStream))
	buf := make([]byte, 300)
	_, err := fr.readStream(buf)
	// Should proceed to payload read and fail with ErrUnexpectedEOF since payload is missing
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("err=%v want io.ErrUnexpectedEOF", err)
	}
}

// TestReadStream_EOFDuringPayload covers EOF returned with the final payload bytes.
func TestReadStream_EOFDuringPayload(t *testing.T) {
	// Reader that returns header, then payload with EOF together
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{5}, err: nil},          // header: 5-byte payload
		{b: []byte("hello"), err: io.EOF}, // full payload with EOF
	}}
	fr := newFramer(sr, nil, WithProtocol(BinaryStream))
	buf := make([]byte, 10)
	n, err := fr.readStream(buf)
	if err != nil {
		t.Fatalf("err=%v want nil", err)
	}
	if n != 5 {
		t.Fatalf("n=%d want 5", n)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("got %q want %q", string(buf[:n]), "hello")
	}
}

// TestWriteStream_CallerChangedBufferMidFrame covers caller length changes mid-frame.
func TestWriteStream_CallerChangedBufferMidFrame(t *testing.T) {
	// Create a writer that accepts partial writes
	var buf bytes.Buffer
	fr := newFramer(nil, &buf, WithProtocol(BinaryStream))

	// First call: start writing a 5-byte message
	msg1 := []byte("hello")
	fr.length = int64(len(msg1))
	fr.offset = 1 // pretend header already written

	// Second call with different length buffer (simulating caller changed buffer)
	msg2 := []byte("hi") // different length
	_, err := fr.writeStream(msg2)
	if err != io.ErrShortWrite {
		t.Fatalf("err=%v want io.ErrShortWrite", err)
	}
}

// TestForwarder_Stream_WouldBlockDuringPayloadRead covers stream read-phase suspension.
func TestForwarder_Stream_WouldBlockDuringPayloadRead(t *testing.T) {
	// Create a reader that returns header, partial payload, then ErrWouldBlock
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{5}, err: nil},         // header: 5-byte payload
		{b: []byte("he"), err: nil},      // partial payload
		{b: nil, err: iox.ErrWouldBlock}, // would-block mid-payload
	}}
	f := NewForwarder(io.Discard, sr, WithProtocol(BinaryStream))

	n, err := f.ForwardOnce()
	if err != iox.ErrWouldBlock {
		t.Fatalf("err=%v want ErrWouldBlock", err)
	}
	// n reflects progress in current phase (payload bytes read)
	if n != 2 {
		t.Fatalf("n=%d want 2", n)
	}
}

// TestForwarder_Stream_ErrMoreDuringPayloadRead covers stream read-phase continuation.
func TestForwarder_Stream_ErrMoreDuringPayloadRead(t *testing.T) {
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{5}, err: nil},    // header: 5-byte payload
		{b: []byte("he"), err: nil}, // partial payload
		{b: nil, err: iox.ErrMore},  // ErrMore mid-payload
	}}
	f := NewForwarder(io.Discard, sr, WithProtocol(BinaryStream))

	n, err := f.ForwardOnce()
	if err != iox.ErrMore {
		t.Fatalf("err=%v want ErrMore", err)
	}
	if n != 2 {
		t.Fatalf("n=%d want 2", n)
	}
}

// --- Additional cold path coverage tests ---

// eofOnCompleteReader returns (n, io.EOF) when the read completes exactly.
type eofOnCompleteReader struct {
	data []byte
	off  int
}

func (r *eofOnCompleteReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	// Return EOF together with the final bytes
	if r.off >= len(r.data) {
		return n, io.EOF
	}
	return n, nil
}

// TestReadStream_EOFExactlyAtExtendedHeaderCompletion covers EOF returned exactly
// when the extended header read completes.
func TestReadStream_EOFExactlyAtExtendedHeaderCompletion(t *testing.T) {
	// Wire: 0xFE (16-bit length marker) + 2 bytes length (0x0100 = 256 in big-endian)
	// The reader returns EOF exactly when the 3-byte header is complete.
	wire := []byte{0xFE, 0x01, 0x00} // header indicating 256-byte payload
	r := &eofOnCompleteReader{data: wire}
	fr := newFramer(r, nil, WithProtocol(BinaryStream))
	buf := make([]byte, 300)
	_, err := fr.readStream(buf)
	// Should fail with ErrUnexpectedEOF because payload is missing
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("err=%v want io.ErrUnexpectedEOF", err)
	}
}

// TestReadStream_EOFExactlyAtPayloadCompletion covers EOF returned exactly when
// the payload read completes.
func TestReadStream_EOFExactlyAtPayloadCompletion(t *testing.T) {
	// Wire: 1-byte header (length=5) + 5-byte payload
	wire := []byte{5, 'h', 'e', 'l', 'l', 'o'}
	r := &eofOnCompleteReader{data: wire}
	fr := newFramer(r, nil, WithProtocol(BinaryStream))
	buf := make([]byte, 10)
	n, err := fr.readStream(buf)
	// Should succeed - EOF at exact completion is valid
	if err != nil {
		t.Fatalf("err=%v want nil", err)
	}
	if n != 5 {
		t.Fatalf("n=%d want 5", n)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("got %q want %q", string(buf[:n]), "hello")
	}
}

// shortWriteNilErrWriter returns partial write with nil error (violates io.Writer contract).
type shortWriteNilErrWriter struct{}

func (w *shortWriteNilErrWriter) Write(p []byte) (int, error) {
	if len(p) > 1 {
		return 1, nil // partial write, no error
	}
	return len(p), nil
}

// TestWritePacket_ShortWriteWithNilError covers partial packet writes without errors.
func TestWritePacket_ShortWriteWithNilError(t *testing.T) {
	fr := newFramer(nil, &shortWriteNilErrWriter{}, WithProtocol(SeqPacket))
	n, err := fr.writePacket([]byte("hello"))
	if err != io.ErrShortWrite {
		t.Fatalf("err=%v want io.ErrShortWrite", err)
	}
	if n != 1 {
		t.Fatalf("n=%d want 1", n)
	}
}

// TestWriteTo_MessageExceedsRbufCapacity covers stream messages beyond the scratch buffer.
func TestWriteTo_MessageExceedsRbufCapacity(t *testing.T) {
	// Create a wire with a message that has payload larger than the rbuf capacity.
	// Use 16-bit header: 0xFE + 2-byte length
	payloadLen := 100
	wire := make([]byte, 3+payloadLen)
	wire[0] = 0xFE                                            // 16-bit length marker
	binary.BigEndian.PutUint16(wire[1:3], uint16(payloadLen)) // length = 100
	copy(wire[3:], bytes.Repeat([]byte{'x'}, payloadLen))     // payload

	r := &Reader{fr: newFramer(bytes.NewReader(wire), nil, WithReadTCP())}
	// Manually set a small rbuf to trigger the capacity check
	r.fr.rbuf = make([]byte, 10) // capacity < payloadLen

	_, err := r.WriteTo(io.Discard)
	if err != ErrTooLong {
		t.Fatalf("err=%v want ErrTooLong", err)
	}
}

// TestWriteTo_EOFMidPayload covers EOF while WriteTo is reading a stream payload.
func TestWriteTo_EOFMidPayload(t *testing.T) {
	// Create a scripted reader that returns header, partial payload, then EOF
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{10}, err: nil},      // header: 10-byte payload
		{b: []byte("hello"), err: nil}, // 5 bytes of payload
		{b: nil, err: io.EOF},          // EOF mid-payload
	}}
	r := &Reader{fr: newFramer(sr, nil, WithReadTCP())}

	_, err := r.WriteTo(io.Discard)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("err=%v want io.ErrUnexpectedEOF", err)
	}
}

// TestReadStream_EOFExactlyAtMinimalHeaderCompletion covers EOF returned exactly
// when the 1-byte minimal header is complete.
func TestReadStream_EOFExactlyAtMinimalHeaderCompletion(t *testing.T) {
	// Reader that returns (1, io.EOF) - header byte with EOF together
	// Header byte 5 means 5-byte payload
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{5}, err: io.EOF}, // 1-byte header with EOF
	}}
	fr := newFramer(sr, nil, WithProtocol(BinaryStream))
	buf := make([]byte, 10)
	_, err := fr.readStream(buf)
	// Should fail with ErrUnexpectedEOF because payload is missing
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("err=%v want io.ErrUnexpectedEOF", err)
	}
}

// TestReadStream_NonEOFErrorDuringExtendedHeader covers non-EOF extended-header errors.
func TestReadStream_NonEOFErrorDuringExtendedHeader(t *testing.T) {
	customErr := errors.New("custom read error")
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{0xFE}, err: nil}, // 16-bit length marker (requires 2 more bytes)
		{b: nil, err: customErr},    // error during extended header read
	}}
	fr := newFramer(sr, nil, WithProtocol(BinaryStream))
	buf := make([]byte, 300)
	_, err := fr.readStream(buf)
	if err != customErr {
		t.Fatalf("err=%v want %v", err, customErr)
	}
}

// partialWouldBlockReader returns (n>0, ErrWouldBlock) on the first call,
// then delivers the remaining data normally.
type partialWouldBlockReader struct {
	data    []byte
	off     int
	partial int // bytes to return with ErrWouldBlock on first call
	called  int
}

func (r *partialWouldBlockReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	r.called++
	if r.called == 1 && r.partial > 0 {
		n := copy(p, r.data[r.off:r.off+r.partial])
		r.off += n
		return n, iox.ErrWouldBlock
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

// TestReadOnce_ProgressFirst_NoOverwrite verifies that readOnce returns
// immediately when the underlying reader returns (n>0, ErrWouldBlock),
// preventing data corruption from retrying with the same buffer slice.
func TestReadOnce_ProgressFirst_NoOverwrite(t *testing.T) {
	payload := []byte("ABCDEFGHIJ")
	rd := &partialWouldBlockReader{data: payload, partial: 4}
	fr := newFramer(rd, nil)
	fr.retryDelay = 0 // blocking mode

	buf := make([]byte, 10)
	n, err := fr.readOnce(buf)
	if n != 4 {
		t.Fatalf("readOnce: want n=4, got n=%d", n)
	}
	if err != iox.ErrWouldBlock {
		t.Fatalf("readOnce: want ErrWouldBlock, got %v", err)
	}
	if string(buf[:n]) != "ABCD" {
		t.Fatalf("readOnce: got %q, want %q", string(buf[:n]), "ABCD")
	}
}

// partialWouldBlockWriter returns (n>0, ErrWouldBlock) on the first call,
// then accepts all remaining data normally.
type partialWouldBlockWriter struct {
	buf     bytes.Buffer
	partial int
	called  int
}

func (w *partialWouldBlockWriter) Write(p []byte) (int, error) {
	w.called++
	if w.called == 1 && w.partial > 0 {
		use := w.partial
		if use > len(p) {
			use = len(p)
		}
		n, _ := w.buf.Write(p[:use])
		return n, iox.ErrWouldBlock
	}
	return w.buf.Write(p)
}

// TestWriteOnce_ProgressFirst_NoDuplication verifies that writeOnce returns
// immediately when the underlying writer returns (n>0, ErrWouldBlock),
// preventing data duplication from retrying with the same buffer slice.
func TestWriteOnce_ProgressFirst_NoDuplication(t *testing.T) {
	dst := &partialWouldBlockWriter{partial: 4}
	fr := newFramer(nil, dst)
	fr.retryDelay = 0 // blocking mode

	payload := []byte("ABCDEFGHIJ")
	n, err := fr.writeOnce(payload)
	if n != 4 {
		t.Fatalf("writeOnce: want n=4, got n=%d", n)
	}
	if err != iox.ErrWouldBlock {
		t.Fatalf("writeOnce: want ErrWouldBlock, got %v", err)
	}
	if got := dst.buf.String(); got != "ABCD" {
		t.Fatalf("writeOnce: got %q, want %q", got, "ABCD")
	}
}

type invalidCountWriter struct{}

func (invalidCountWriter) Write(p []byte) (int, error) { return len(p) + 1, nil }

type zeroNilWriter struct{}

func (zeroNilWriter) Write([]byte) (int, error) { return 0, nil }

type zeroErrWriter struct {
	err error
}

func (w zeroErrWriter) Write([]byte) (int, error) { return 0, w.err }

type fullErrWriter struct {
	buf bytes.Buffer
	err error
}

func (w *fullErrWriter) Write(p []byte) (int, error) {
	_, _ = w.buf.Write(p)
	return len(p), w.err
}

type packetErrReader struct {
	err error
}

func (r packetErrReader) Read([]byte) (int, error) { return 0, r.err }

type headerEOFReader struct {
	done bool
}

func (r *headerEOFReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	p[0] = 0
	return 1, io.EOF
}

func TestCoverageWritePacketDefensiveBranches(t *testing.T) {
	t.Run("invalid count", func(t *testing.T) {
		fr := newFramer(nil, invalidCountWriter{}, WithProtocol(SeqPacket))
		n, err := fr.writePacket([]byte("x"))
		if n != 0 || !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("want (0, ErrShortWrite), got (%d, %v)", n, err)
		}
	})

	t.Run("zero nil write", func(t *testing.T) {
		fr := newFramer(nil, zeroNilWriter{}, WithProtocol(SeqPacket))
		n, err := fr.writePacket([]byte("x"))
		if n != 0 || !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("want (0, ErrShortWrite), got (%d, %v)", n, err)
		}
	})
}

func TestCoveragePacketTransferCapDefensiveBranches(t *testing.T) {
	fr := newFramer(nil, nil, WithReadUDP(), WithReadLimit(maxInt()))

	if _, err := packetTransferCap(fr); !errors.Is(err, ErrTooLong) {
		t.Fatalf("packetTransferCap: want ErrTooLong, got %v", err)
	}
	if _, _, err := fr.ensurePacketReadBuffer(); !errors.Is(err, ErrTooLong) {
		t.Fatalf("ensurePacketReadBuffer: want ErrTooLong, got %v", err)
	}
	if _, _, err := fr.ensureForwardBufferCap(); !errors.Is(err, ErrTooLong) {
		t.Fatalf("ensureForwardBufferCap: want ErrTooLong, got %v", err)
	}
}

func TestCoverageDrainByteCursorDefensiveCount(t *testing.T) {
	cur := byteCursor{end: 1}
	n, err := drainByteCursor(&cur, []byte("x"), invalidCountWriter{})
	if n != 0 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("want (0, ErrShortWrite), got (%d, %v)", n, err)
	}
}

func TestCoverageReadStreamHeaderEOFWithProgress(t *testing.T) {
	fr := newFramer(&headerEOFReader{}, nil, WithReadTCP())
	n, err := fr.read(make([]byte, 1))
	if n != 0 || err != nil {
		t.Fatalf("want (0, nil), got (%d, %v)", n, err)
	}
}

func TestCoverageForwarderPacketDefensiveBranches(t *testing.T) {
	t.Run("constructor cap fallback", func(t *testing.T) {
		f := NewForwarder(io.Discard, bytes.NewReader(nil), WithProtocol(SeqPacket), WithReadLimit(maxInt()))
		if cap(f.buf) != defaultPacketTransferMax {
			t.Fatalf("cap=%d want %d", cap(f.buf), defaultPacketTransferMax)
		}
	})

	t.Run("forward cap error", func(t *testing.T) {
		f := NewForwarder(io.Discard, bytes.NewReader(nil), WithProtocol(SeqPacket))
		f.state = 1
		f.rr.readLimit = int64(maxInt())
		n, err := f.ForwardOnce()
		if n != 0 || !errors.Is(err, ErrTooLong) {
			t.Fatalf("want (0, ErrTooLong), got (%d, %v)", n, err)
		}
	})

	t.Run("forward buffer too small", func(t *testing.T) {
		f := NewForwarder(io.Discard, bytes.NewReader([]byte("packet")), WithProtocol(SeqPacket), WithReadLimit(4))
		f.state = 1
		f.buf = make([]byte, 1)
		n, err := f.ForwardOnce()
		if n != 0 || !errors.Is(err, io.ErrShortBuffer) {
			t.Fatalf("want (0, ErrShortBuffer), got (%d, %v)", n, err)
		}
	})

	t.Run("forward read ErrTooLong", func(t *testing.T) {
		f := NewForwarder(io.Discard, packetErrReader{err: ErrTooLong}, WithProtocol(SeqPacket))
		n, err := f.ForwardOnce()
		if n != 0 || !errors.Is(err, ErrTooLong) {
			t.Fatalf("want (0, ErrTooLong), got (%d, %v)", n, err)
		}
	})
}

func TestCoverageWriteToStreamZeroLengthKnownPacketDestinationControl(t *testing.T) {
	src := &Reader{fr: newFramer(bytes.NewReader([]byte{0}), nil, WithReadTCP())}
	rawDst := &fullErrWriter{err: ErrMore}
	dst := &Writer{fr: newFramer(nil, rawDst, WithProtocol(SeqPacket))}

	n, err := src.WriteTo(dst)
	if n != 0 || !errors.Is(err, ErrMore) {
		t.Fatalf("want (0, ErrMore), got (%d, %v)", n, err)
	}
	if rawDst.buf.Len() != 0 {
		t.Fatalf("dst len=%d want 0", rawDst.buf.Len())
	}
}

func TestCoverageWriteToStreamUnexpectedEOFPayload(t *testing.T) {
	src := &Reader{fr: newFramer(bytes.NewReader([]byte{5, 'a'}), nil, WithReadTCP())}

	n, err := src.WriteTo(io.Discard)
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want (0, UnexpectedEOF), got (%d, %v)", n, err)
	}
}

func TestCoverageWriteToPacketReadCapError(t *testing.T) {
	src := &Reader{fr: newFramer(bytes.NewReader(nil), nil, WithReadUDP(), WithReadLimit(maxInt()))}

	n, err := src.WriteTo(io.Discard)
	if n != 0 || !errors.Is(err, ErrTooLong) {
		t.Fatalf("want (0, ErrTooLong), got (%d, %v)", n, err)
	}
}

func TestCoverageResumeWriteToPendingBranches(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		fr := newFramer(nil, nil)
		n, err, done := resumeWriteToPending(fr, writeToSink{mode: writeToByte, dst: io.Discard})
		if n != 0 || err != nil || done {
			t.Fatalf("want (0, nil, false), got (%d, %v, %v)", n, err, done)
		}
	})

	t.Run("mode mismatch", func(t *testing.T) {
		fr := newFramer(nil, nil)
		saveWriteToPacket(fr, 1, nil)
		n, err, done := resumeWriteToPending(fr, writeToSink{mode: writeToByte, dst: io.Discard})
		if n != 0 || !errors.Is(err, io.ErrShortWrite) || !done {
			t.Fatalf("want (0, ErrShortWrite, true), got (%d, %v, %v)", n, err, done)
		}
	})

	t.Run("byte full control keeps source", func(t *testing.T) {
		sourceErr := errors.New("source frontier")
		fr := newFramer(nil, nil)
		fr.rbuf = []byte("packet")
		saveWriteToByteCursor(fr, 2, len(fr.rbuf), sourceErr)
		dst := &fullErrWriter{err: ErrMore}

		n, err, done := resumeWriteToPending(fr, writeToSink{mode: writeToByte, dst: dst})
		if n != len("cket") || !errors.Is(err, ErrMore) || !done {
			t.Fatalf("want (%d, ErrMore, true), got (%d, %v, %v)", len("cket"), n, err, done)
		}
		n, err, done = resumeWriteToPending(fr, writeToSink{mode: writeToByte, dst: io.Discard})
		if n != 0 || !errors.Is(err, sourceErr) || !done {
			t.Fatalf("want (0, sourceErr, true), got (%d, %v, %v)", n, err, done)
		}
	})

	t.Run("packet resume full control keeps source", func(t *testing.T) {
		sourceErr := errors.New("source frontier")
		fr := newFramer(nil, nil)
		fr.rbuf = []byte("packet")
		saveWriteToPacket(fr, len(fr.rbuf), sourceErr)
		rawDst := &fullErrWriter{err: ErrMore}
		dst := newFramer(nil, rawDst, WithProtocol(SeqPacket))

		n, err, done := resumeWriteToPending(fr, writeToSink{mode: writeToPacket, fr: dst})
		if n != len(fr.rbuf) || !errors.Is(err, ErrMore) || !done {
			t.Fatalf("want (%d, ErrMore, true), got (%d, %v, %v)", len(fr.rbuf), n, err, done)
		}
		n, err, done = resumeWriteToPending(fr, writeToSink{mode: writeToPacket, fr: dst})
		if n != 0 || !errors.Is(err, sourceErr) || !done {
			t.Fatalf("want (0, sourceErr, true), got (%d, %v, %v)", n, err, done)
		}
	})

	t.Run("packet resume partial semantic clears", func(t *testing.T) {
		fr := newFramer(nil, nil)
		fr.rbuf = []byte("packet")
		saveWriteToPacket(fr, len(fr.rbuf), errors.New("source frontier"))
		rawDst := &partialWouldBlockWriter{partial: 2}
		dst := newFramer(nil, rawDst, WithProtocol(SeqPacket))

		n, err, done := resumeWriteToPending(fr, writeToSink{mode: writeToPacket, fr: dst})
		if n != 2 || !errors.Is(err, io.ErrShortWrite) || !done || fr.wtMode != writeToNone {
			t.Fatalf("want (2, ErrShortWrite, true) with cleared state, got (%d, %v, %v) wtMode=%d", n, err, done, fr.wtMode)
		}
	})

	t.Run("invalid mode", func(t *testing.T) {
		fr := newFramer(nil, nil)
		fr.wtMode = writeToMode(99)
		n, err, done := resumeWriteToPending(fr, writeToSink{mode: writeToMode(99)})
		if n != 0 || !errors.Is(err, ErrInvalidArgument) || !done {
			t.Fatalf("want (0, ErrInvalidArgument, true), got (%d, %v, %v)", n, err, done)
		}
	})
}

func TestCoverageEmitWriteToMessageBranches(t *testing.T) {
	t.Run("byte full control keeps source", func(t *testing.T) {
		sourceErr := errors.New("source frontier")
		fr := newFramer(nil, nil)
		dst := &fullErrWriter{err: ErrMore}

		n, err, done := emitWriteToMessage(fr, writeToSink{mode: writeToByte, dst: dst}, []byte("packet"), sourceErr)
		if n != len("packet") || !errors.Is(err, ErrMore) || !done {
			t.Fatalf("want (%d, ErrMore, true), got (%d, %v, %v)", len("packet"), n, err, done)
		}
		n, err, done = resumeWriteToPending(fr, writeToSink{mode: writeToByte, dst: io.Discard})
		if n != 0 || !errors.Is(err, sourceErr) || !done {
			t.Fatalf("want (0, sourceErr, true), got (%d, %v, %v)", n, err, done)
		}
	})

	t.Run("packet success reports after", func(t *testing.T) {
		sourceErr := errors.New("source frontier")
		fr := newFramer(nil, nil)
		rawDst := &bytes.Buffer{}
		dst := newFramer(nil, rawDst, WithProtocol(SeqPacket))

		n, err, done := emitWriteToMessage(fr, writeToSink{mode: writeToPacket, fr: dst}, []byte("packet"), sourceErr)
		if n != len("packet") || !errors.Is(err, sourceErr) || !done {
			t.Fatalf("want (%d, sourceErr, true), got (%d, %v, %v)", len("packet"), n, err, done)
		}
	})

	t.Run("frame hard error", func(t *testing.T) {
		boom := errors.New("write failed")
		fr := newFramer(nil, nil)
		dst := newFramer(nil, &fullErrWriter{err: boom}, WithWriteTCP())

		n, err, done := emitWriteToMessage(fr, writeToSink{mode: writeToFrame, fr: dst}, []byte("packet"), nil)
		if n != 0 || !errors.Is(err, boom) || !done {
			t.Fatalf("want (0, boom, true), got (%d, %v, %v)", n, err, done)
		}
	})

	t.Run("invalid mode", func(t *testing.T) {
		fr := newFramer(nil, nil)
		n, err, done := emitWriteToMessage(fr, writeToSink{mode: writeToMode(99)}, []byte("packet"), nil)
		if n != 0 || !errors.Is(err, ErrInvalidArgument) || !done {
			t.Fatalf("want (0, ErrInvalidArgument, true), got (%d, %v, %v)", n, err, done)
		}
	})
}

func TestCoverageReadFromDefensiveBranches(t *testing.T) {
	t.Run("stream pending larger than buffer", func(t *testing.T) {
		fr := newFramer(nil, io.Discard, WithWriteTCP())
		fr.wbuf = make([]byte, 1)
		saveReadFromPending(fr, 2, nil)
		w := &Writer{fr: fr}

		n, err := w.ReadFrom(bytes.NewReader(nil))
		if n != 0 || !errors.Is(err, io.ErrShortBuffer) {
			t.Fatalf("want (0, ErrShortBuffer), got (%d, %v)", n, err)
		}
	})

	t.Run("packet source semantic without progress", func(t *testing.T) {
		w := &Writer{fr: newFramer(nil, io.Discard, WithProtocol(SeqPacket))}
		n, err := w.ReadFrom(packetErrReader{err: ErrMore})
		if n != 0 || !errors.Is(err, ErrMore) {
			t.Fatalf("want (0, ErrMore), got (%d, %v)", n, err)
		}
	})
}

func TestCoverageReadFromPacketResumeBranches(t *testing.T) {
	t.Run("zero progress semantic keeps packet", func(t *testing.T) {
		rawDst := zeroErrWriter{err: ErrWouldBlock}
		fr := newFramer(nil, rawDst, WithProtocol(SeqPacket))
		fr.wbuf = []byte("packet")
		saveReadFromPending(fr, len(fr.wbuf), nil)
		w := &Writer{fr: fr}

		n, err := w.readFromPacket(bytes.NewReader(nil))
		if n != 0 || !errors.Is(err, ErrWouldBlock) || fr.rfLen != len(fr.wbuf) {
			t.Fatalf("want (0, ErrWouldBlock) with pending packet, got (%d, %v) rfLen=%d", n, err, fr.rfLen)
		}
	})

	t.Run("full semantic keeps source frontier", func(t *testing.T) {
		sourceErr := errors.New("source frontier")
		rawDst := &fullErrWriter{err: ErrMore}
		fr := newFramer(nil, rawDst, WithProtocol(SeqPacket))
		fr.wbuf = []byte("packet")
		saveReadFromPending(fr, len(fr.wbuf), sourceErr)
		w := &Writer{fr: fr}

		n, err := w.readFromPacket(bytes.NewReader(nil))
		if n != 0 || !errors.Is(err, ErrMore) || !hasReadFromControl(fr) {
			t.Fatalf("want (0, ErrMore) with control pending, got (%d, %v) rfLen=%d rfAfter=%v", n, err, fr.rfLen, fr.rfAfter)
		}
		n, err = w.ReadFrom(bytes.NewReader(nil))
		if n != 0 || !errors.Is(err, sourceErr) {
			t.Fatalf("want (0, sourceErr), got (%d, %v)", n, err)
		}
	})

	t.Run("partial semantic clears packet", func(t *testing.T) {
		rawDst := &partialWouldBlockWriter{partial: 2}
		fr := newFramer(nil, rawDst, WithProtocol(SeqPacket))
		fr.wbuf = []byte("packet")
		saveReadFromPending(fr, len(fr.wbuf), errors.New("source frontier"))
		w := &Writer{fr: fr}

		n, err := w.readFromPacket(bytes.NewReader(nil))
		if n != 0 || !errors.Is(err, io.ErrShortWrite) || fr.rfLen != 0 || fr.rfAfter != nil {
			t.Fatalf("want cleared ErrShortWrite, got (%d, %v) rfLen=%d rfAfter=%v", n, err, fr.rfLen, fr.rfAfter)
		}
	})
}
