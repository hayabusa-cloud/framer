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

	"code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

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

// Read implements io.Reader.
func (r *scriptedReader) Read(p []byte) (int, error) {
	// Main loop handles empty buffers and EOF.
	for {
		// Done with all steps.
		if r.step >= len(r.steps) {
			return 0, io.EOF
		}
		// Get current step.
		st := r.steps[r.step]
		if len(st.b) == 0 {
			// Empty buffer => return the step error.
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

type wouldBlockWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *wouldBlockWriter) Write(p []byte) (int, error) {
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
	return n, nil // short write without error -> should trigger io.ErrShortWrite for packet mode
}

type noProgressReader struct{}

func (*noProgressReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return 0, nil
}

type noProgressWriter struct{}

func (*noProgressWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	return 0, nil
}

func TestStreamRoundTrip_BigEndian(t *testing.T) {
	var raw bytes.Buffer
	w := framer.NewWriter(&raw, framer.WithByteOrder(binary.BigEndian), framer.WithProtocol(framer.BinaryStream))
	r := framer.NewReader(&raw, framer.WithByteOrder(binary.BigEndian), framer.WithProtocol(framer.BinaryStream))

	msgs := [][]byte{
		{},
		[]byte("hello"),
		bytes.Repeat([]byte{'a'}, 253),
		bytes.Repeat([]byte{'b'}, 254),
		bytes.Repeat([]byte{'c'}, 4096),
	}

	for i, m := range msgs {
		n, err := w.Write(m)
		if err != nil {
			t.Fatalf("write[%d]: %v", i, err)
		}
		if n != len(m) {
			t.Fatalf("write[%d]: n=%d want=%d", i, n, len(m))
		}
	}

	for i, m := range msgs {
		buf := make([]byte, len(m))
		n, err := r.Read(buf)
		if err != nil {
			t.Fatalf("read[%d]: %v", i, err)
		}
		if n != len(m) {
			t.Fatalf("read[%d]: n=%d want=%d", i, n, len(m))
		}
		if !bytes.Equal(buf, m) {
			t.Fatalf("read[%d]: payload mismatch", i)
		}
	}
}

func TestStreamNonblockRead_WouldBlockRequiresSameBuffer(t *testing.T) {
	// One framed message: header + payload.
	msg := []byte("abcdefghij")

	// Encode using a normal writer.
	var raw bytes.Buffer
	w := framer.NewWriter(&raw, framer.WithProtocol(framer.BinaryStream))
	if _, err := w.Write(msg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	wire := raw.Bytes()

	// Read the wire in small chunks with an injected would-block.
	under := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: wire[:2]},
		{err: iox.ErrWouldBlock},
		{b: wire[2:]},
	}}
	r := framer.NewReader(under, framer.WithNonblock(), framer.WithProtocol(framer.BinaryStream))

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

func TestStreamNonblockWrite_WouldBlockMaintainsState(t *testing.T) {
	uw := &wouldBlockWriter{limit: 3}
	w := framer.NewWriter(uw, framer.WithNonblock(), framer.WithProtocol(framer.BinaryStream))

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
			// header might still be in progress.
			continue
		}
	}
	if written != len(msg) {
		t.Fatalf("written=%d want=%d", written, len(msg))
	}

	// Decode and verify.
	r := framer.NewReader(bytes.NewReader(uw.buf.Bytes()), framer.WithProtocol(framer.BinaryStream))
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

func TestStreamRead_NoProgressGuard(t *testing.T) {
	r := framer.NewReader(&noProgressReader{}, framer.WithProtocol(framer.BinaryStream))
	buf := make([]byte, 8)
	_, err := r.Read(buf)
	if !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("want io.ErrNoProgress, got %v", err)
	}
}

func TestStreamWrite_NoProgressGuard(t *testing.T) {
	w := framer.NewWriter(&noProgressWriter{}, framer.WithProtocol(framer.BinaryStream))
	_, err := w.Write([]byte("x"))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("want io.ErrShortWrite, got %v", err)
	}
}

// moreReader simulates an underlying transport that returns data together with
// a semantic multi-shot signal (iox.ErrMore). It returns:
//   - first call: the frame header only (no error)
//   - second call: a slice of the payload with iox.ErrMore
//   - third call: the rest of the payload (no error)
type moreReader struct {
	wire     []byte
	headerN  int
	payload1 int
	off      int
	call     int
}

func (r *moreReader) Read(p []byte) (int, error) {
	r.call++
	switch r.call {
	case 1:
		// Return header only.
		n := copy(p, r.wire[:r.headerN])
		r.off += n
		return n, nil
	case 2:
		// Return first payload chunk with ErrMore.
		end := r.off + r.payload1
		if end > len(r.wire) {
			end = len(r.wire)
		}
		n := copy(p, r.wire[r.off:end])
		r.off += n
		return n, iox.ErrMore
	default:
		// Return the rest.
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

func TestStreamRead_PropagatesErrMore(t *testing.T) {
	// Prepare a framed message on a binary stream.
	msg := []byte("multi-shot")
	var raw bytes.Buffer
	w := framer.NewWriter(&raw, framer.WithProtocol(framer.BinaryStream))
	if _, err := w.Write(msg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	wire := raw.Bytes()

	// Stream header for small payloads is 1 byte.
	headerN := 1
	// Simulate underlying returning part of the payload along with ErrMore.
	mr := &moreReader{wire: wire, headerN: headerN, payload1: 3}
	r := framer.NewReader(mr, framer.WithProtocol(framer.BinaryStream))

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
