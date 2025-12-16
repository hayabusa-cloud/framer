// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

// --- Test fakes (minimal; allocation-free steady state) ---

type fwSliceWriter struct {
	buf []byte
	off int
}

func (w *fwSliceWriter) Reset() { w.off = 0 }
func (w *fwSliceWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := copy(w.buf[w.off:], p)
	w.off += n
	return n, nil
}

type fwReplayReader struct {
	b          []byte
	off        int
	chunkLimit int
	wouldBlock bool
}

func (r *fwReplayReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c := r.chunkLimit
	if c <= 0 || c > len(p) {
		c = len(p)
	}
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	rem := len(r.b) - r.off
	if rem < c {
		c = rem
	}
	n := copy(p, r.b[r.off:r.off+c])
	r.off += n
	if r.wouldBlock && n < len(p) {
		return n, iox.ErrWouldBlock
	}
	return n, nil
}

type fwWouldBlockWriter struct {
	limit int
	buf   bytes.Buffer
}

func (w *fwWouldBlockWriter) Write(p []byte) (int, error) {
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

// frameHelper encodes a single payload into BinaryStream wire.
func frameHelper(payload []byte) []byte {
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithProtocol(fr.BinaryStream))
	if n, err := w.Write(payload); err != nil || n != len(payload) {
		panic("encode failed")
	}
	return raw.Bytes()
}

func TestForward_StreamRelay_Correctness(t *testing.T) {
	msgs := [][]byte{
		{},
		[]byte("hello"),
		bytes.Repeat([]byte{'a'}, 253),
		bytes.Repeat([]byte{'b'}, 254),
		bytes.Repeat([]byte{'c'}, 4096),
	}

	// Prepare source wire (concatenate frames).
	var srcWire bytes.Buffer
	enc := fr.NewWriter(&srcWire, fr.WithByteOrder(binary.BigEndian), fr.WithProtocol(fr.BinaryStream))
	for _, m := range msgs {
		if n, err := enc.Write(m); err != nil || n != len(m) {
			t.Fatalf("encode: n=%d err=%v", n, err)
		}
	}

	dstBuf := &fwSliceWriter{buf: make([]byte, srcWire.Len())}
	fwd := fr.NewForwarder(dstBuf, bytes.NewReader(srcWire.Bytes()), fr.WithByteOrder(binary.BigEndian), fr.WithProtocol(fr.BinaryStream))

	// Forward message-by-message until EOF.
	for {
		_, err := fwd.ForwardOnce()
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, iox.ErrWouldBlock) || errors.Is(err, iox.ErrMore) {
			continue
		}
		t.Fatalf("forward: %v", err)
	}

	// Decode dst wire and verify payloads.
	rd := fr.NewReader(bytes.NewReader(dstBuf.buf[:dstBuf.off]), fr.WithByteOrder(binary.BigEndian), fr.WithProtocol(fr.BinaryStream))
	for i, m := range msgs {
		got := make([]byte, len(m))
		n, err := rd.Read(got)
		if err != nil || n != len(m) {
			t.Fatalf("decode[%d]: n=%d err=%v", i, n, err)
		}
		if !bytes.Equal(got, m) {
			t.Fatalf("payload[%d] mismatch", i)
		}
	}
}

func TestForward_WouldBlockOnRead(t *testing.T) {
	payload := []byte("abcdefghij")
	wire := frameHelper(payload)
	rr := &fwReplayReader{b: wire, chunkLimit: 3, wouldBlock: true}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, rr, fr.WithNonblock(), fr.WithProtocol(fr.BinaryStream))

	var progressed bool
	for {
		n, err := fwd.ForwardOnce()
		if err == nil {
			break
		}
		if errors.Is(err, iox.ErrWouldBlock) || errors.Is(err, iox.ErrMore) {
			if n > 0 {
				progressed = true
			}
			continue
		}
		t.Fatalf("forward: %v", err)
	}
	if !progressed {
		t.Fatalf("expected partial progress with would-block on read")
	}
	// Verify destination decodes correctly.
	rd := fr.NewReader(bytes.NewReader(dst.Bytes()), fr.WithProtocol(fr.BinaryStream))
	got := make([]byte, len(payload))
	n, err := rd.Read(got)
	if err != nil || n != len(payload) || !bytes.Equal(got, payload) {
		t.Fatalf("decode: n=%d err=%v ok=%v", n, err, bytes.Equal(got, payload))
	}
}

func TestForward_WouldBlockOnWrite(t *testing.T) {
	payload := []byte("hello world")
	wire := frameHelper(payload)
	rr := &fwReplayReader{b: wire, chunkLimit: len(wire)}
	dst := &fwWouldBlockWriter{limit: 4}
	fwd := fr.NewForwarder(dst, rr, fr.WithNonblock(), fr.WithProtocol(fr.BinaryStream))

	var progressed bool
	for {
		n, err := fwd.ForwardOnce()
		if err == nil {
			break
		}
		if errors.Is(err, iox.ErrWouldBlock) || errors.Is(err, iox.ErrMore) {
			if n > 0 {
				progressed = true
			}
			continue
		}
		t.Fatalf("forward: %v", err)
	}
	if !progressed {
		t.Fatalf("expected partial progress with would-block on write")
	}
	// Verify destination decodes correctly.
	rd := fr.NewReader(bytes.NewReader(dst.buf.Bytes()), fr.WithProtocol(fr.BinaryStream))
	got := make([]byte, len(payload))
	n, err := rd.Read(got)
	if err != nil || n != len(payload) || !bytes.Equal(got, payload) {
		t.Fatalf("decode: n=%d err=%v ok=%v", n, err, bytes.Equal(got, payload))
	}
}

// moreReader simulates a multi-shot read (ErrMore) during payload.
type fwdMoreReader struct {
	wire     []byte
	headerN  int
	payload1 int
	off      int
	call     int
}

func (r *fwdMoreReader) Read(p []byte) (int, error) {
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
		if r.off >= len(r.wire) {
			return n, nil
		}
		return n, nil
	}
}

func TestForward_PropagatesErrMore(t *testing.T) {
	msg := []byte("multi-shot")
	wire := frameHelper(msg)
	mr := &fwdMoreReader{wire: wire, headerN: 1, payload1: 3}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, mr, fr.WithProtocol(fr.BinaryStream))

	n1, err := fwd.ForwardOnce()
	if !errors.Is(err, iox.ErrMore) || n1 <= 0 {
		t.Fatalf("first: n=%d err=%v", n1, err)
	}
	// Complete
	for {
		_, e := fwd.ForwardOnce()
		if e == nil {
			break
		}
		if !errors.Is(e, iox.ErrMore) && !errors.Is(e, iox.ErrWouldBlock) {
			t.Fatalf("forward: %v", e)
		}
	}
	// Verify
	rd := fr.NewReader(bytes.NewReader(dst.Bytes()), fr.WithProtocol(fr.BinaryStream))
	got := make([]byte, len(msg))
	n, e := rd.Read(got)
	if e != nil || n != len(msg) || !bytes.Equal(got, msg) {
		t.Fatalf("decode: n=%d err=%v ok=%v", n, e, bytes.Equal(got, msg))
	}
}

func TestForward_ErrShortBuffer_WhenMessageExceedsInternalBuf(t *testing.T) {
	// Build a 128KiB message; Forwarder default buffer is 64KiB (when no ReadLimit).
	payload := bytes.Repeat([]byte{'x'}, 128<<10)
	wire := frameHelper(payload)
	rr := bytes.NewReader(wire)
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, rr, fr.WithProtocol(fr.BinaryStream))

	_, err := fwd.ForwardOnce()
	if !errors.Is(err, io.ErrShortBuffer) {
		t.Fatalf("want io.ErrShortBuffer, got %v", err)
	}
}

func TestForward_ErrTooLong_WhenExceedsReadLimit(t *testing.T) {
	payload := bytes.Repeat([]byte{'y'}, 2<<10)
	wire := frameHelper(payload)
	rr := bytes.NewReader(wire)
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, rr, fr.WithReadLimit(1024), fr.WithProtocol(fr.BinaryStream))
	_, err := fwd.ForwardOnce()
	if !errors.Is(err, fr.ErrTooLong) {
		t.Fatalf("want ErrTooLong, got %v", err)
	}
}

func TestForward_PropagatesUnexpectedEOF_MidHeader(t *testing.T) {
	// Simulate stream ending mid-header: partial header byte then EOF.
	// For extended length (0xFE), we need 3 bytes total but only get 1.
	sr := &scriptedPacketReader{steps: []packetStep{
		{data: []byte{0xFE}, err: nil}, // header byte indicating 2-byte extended length
		{data: nil, err: io.EOF},       // EOF before extended length bytes
	}}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, sr, fr.WithProtocol(fr.BinaryStream))

	_, err := fwd.ForwardOnce()
	// Must propagate io.ErrUnexpectedEOF, not convert to io.EOF.
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestForward_ZeroAllocs_SteadyState(t *testing.T) {
	payload := bytes.Repeat([]byte{'z'}, 4<<10)
	wire := frameHelper(payload)
	// Prepare readers/writers that do not allocate.
	rr := &fwReplayReader{b: wire, chunkLimit: len(wire)}
	sink := &fwSliceWriter{buf: make([]byte, len(wire))}
	fwd := fr.NewForwarder(sink, rr, fr.WithProtocol(fr.BinaryStream))

	allocs := testing.AllocsPerRun(1000, func() {
		// Reset state for deterministic loop.
		rr.off = 0
		sink.off = 0
		for {
			_, err := fwd.ForwardOnce()
			if err == nil {
				continue
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, iox.ErrWouldBlock) || errors.Is(err, iox.ErrMore) {
				continue
			}
			t.Fatalf("forward: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("allocs/op = %v; want 0", allocs)
	}
}

// --- Packet-preserving protocol tests (SeqPacket / Datagram) ---

func TestForward_SeqPacket_Correctness(t *testing.T) {
	// SeqPacket preserves boundaries; no framing header is added.
	msgs := [][]byte{
		[]byte("hello"),
		[]byte("world"),
		bytes.Repeat([]byte{'p'}, 1024),
	}

	protos := []struct {
		name  string
		proto fr.Protocol
	}{
		{"SeqPacket", fr.SeqPacket},
		{"Datagram", fr.Datagram},
	}

	for _, tc := range protos {
		t.Run(tc.name, func(t *testing.T) {
			for i, msg := range msgs {
				// Source: raw packet bytes (no framing).
				rr := &fwReplayReader{b: msg, chunkLimit: len(msg)}
				sink := &fwSliceWriter{buf: make([]byte, len(msg)+16)}
				fwd := fr.NewForwarder(sink, rr, fr.WithProtocol(tc.proto))

				n, err := fwd.ForwardOnce()
				if err != nil {
					t.Fatalf("msg[%d]: forward err=%v", i, err)
				}
				if n != len(msg) {
					t.Fatalf("msg[%d]: n=%d want=%d", i, n, len(msg))
				}
				if !bytes.Equal(sink.buf[:sink.off], msg) {
					t.Fatalf("msg[%d]: payload mismatch", i)
				}
			}
		})
	}
}

func TestForward_SeqPacket_WouldBlockOnRead(t *testing.T) {
	// For packet protocols, WouldBlock means no packet available yet.
	// Use a reader that returns WouldBlock first, then delivers the packet.
	msg := []byte("packet-data")
	sr := &scriptedPacketReader{steps: []packetStep{
		{n: 0, err: iox.ErrWouldBlock},
		{data: msg, err: nil},
	}}
	sink := &fwSliceWriter{buf: make([]byte, len(msg)+16)}
	fwd := fr.NewForwarder(sink, sr, fr.WithNonblock(), fr.WithProtocol(fr.SeqPacket))

	// First call should return WouldBlock.
	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first call: want ErrWouldBlock, got n=%d err=%v", n1, err1)
	}

	// Second call should complete.
	n2, err2 := fwd.ForwardOnce()
	if err2 != nil {
		t.Fatalf("second call: n=%d err=%v", n2, err2)
	}
	if !bytes.Equal(sink.buf[:sink.off], msg) {
		t.Fatalf("payload mismatch: got=%q want=%q", sink.buf[:sink.off], msg)
	}
}

// scriptedPacketReader returns predefined responses for each Read call.
type scriptedPacketReader struct {
	steps []packetStep
	idx   int
}

type packetStep struct {
	data []byte
	n    int
	err  error
}

func (r *scriptedPacketReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.steps) {
		return 0, io.EOF
	}
	s := r.steps[r.idx]
	r.idx++
	if s.data != nil {
		n := copy(p, s.data)
		return n, s.err
	}
	return s.n, s.err
}

func TestForward_SeqPacket_Truncation(t *testing.T) {
	// For packet protocols, when ReadLimit is set, packets are truncated to the limit.
	// This tests that truncation works correctly.
	msg := bytes.Repeat([]byte{'x'}, 2048)
	sr := &scriptedPacketReader{steps: []packetStep{
		{data: msg, err: nil},
	}}
	sink := &fwSliceWriter{buf: make([]byte, len(msg)+16)}
	fwd := fr.NewForwarder(sink, sr, fr.WithReadLimit(1024), fr.WithProtocol(fr.SeqPacket))

	n, err := fwd.ForwardOnce()
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	// Packet is truncated to ReadLimit.
	if n != 1024 {
		t.Fatalf("n=%d want=1024 (truncated)", n)
	}
}

func TestForward_SeqPacket_EOF_EmptyRead(t *testing.T) {
	// EOF on first read with no data.
	rr := &fwReplayReader{b: nil}
	sink := &fwSliceWriter{buf: make([]byte, 64)}
	fwd := fr.NewForwarder(sink, rr, fr.WithProtocol(fr.SeqPacket))

	_, err := fwd.ForwardOnce()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestForward_SeqPacket_EOF_AfterLastPacket(t *testing.T) {
	// Some io.Readers return (n>0, io.EOF) on the final read. ForwardOnce must
	// forward that final packet and then report io.EOF on the next call.
	msg := []byte("last-packet")
	sr := &scriptedPacketReader{steps: []packetStep{
		{data: msg, err: io.EOF},
	}}
	sink := &fwSliceWriter{buf: make([]byte, 64)}
	fwd := fr.NewForwarder(sink, sr, fr.WithProtocol(fr.SeqPacket))

	n, err := fwd.ForwardOnce()
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if n != len(msg) {
		t.Fatalf("n=%d want=%d", n, len(msg))
	}
	if !bytes.Equal(sink.buf[:sink.off], msg) {
		t.Fatalf("payload mismatch: got=%q want=%q", sink.buf[:sink.off], msg)
	}

	_, err = fwd.ForwardOnce()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
}

// --- Stream EOF during payload read ---

type eofMidPayloadReader struct {
	wire []byte
	off  int
	call int
}

func (r *eofMidPayloadReader) Read(p []byte) (int, error) {
	r.call++
	if r.off >= len(r.wire) {
		return 0, io.EOF
	}
	// First call: return header + partial payload.
	// Second call: return EOF (simulating truncated stream).
	if r.call == 1 {
		n := copy(p, r.wire[:len(r.wire)-2]) // leave 2 bytes unread
		r.off += n
		return n, nil
	}
	return 0, io.EOF
}

func TestForward_Stream_UnexpectedEOF_MidPayload(t *testing.T) {
	payload := []byte("hello-world")
	wire := frameHelper(payload)
	rr := &eofMidPayloadReader{wire: wire}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, rr, fr.WithProtocol(fr.BinaryStream))

	_, err := fwd.ForwardOnce()
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want io.ErrUnexpectedEOF, got %v", err)
	}
}

// --- Non-semantic error propagation in write phase ---

type errorWriter struct {
	err error
}

func (w *errorWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

func TestForward_Stream_WriteError_Propagates(t *testing.T) {
	payload := []byte("test")
	wire := frameHelper(payload)
	rr := bytes.NewReader(wire)
	customErr := errors.New("custom write error")
	dst := &errorWriter{err: customErr}
	fwd := fr.NewForwarder(dst, rr, fr.WithProtocol(fr.BinaryStream))

	_, err := fwd.ForwardOnce()
	if !errors.Is(err, customErr) {
		t.Fatalf("want custom error, got %v", err)
	}
}
