// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

// scriptedReader is defined in framer_test.go; reuse it here.

type spyReader struct {
	r      io.Reader
	wt     func(io.Writer) (int64, error)
	called int
}

func (s *spyReader) Read(p []byte) (int, error) { return s.r.Read(p) }
func (s *spyReader) WriteTo(w io.Writer) (int64, error) {
	s.called++
	return s.wt(w)
}

func TestWriterTo_FastPath_Selected(t *testing.T) {
	// Source: framer.Reader with stream semantics.
	var raw bytes.Buffer
	raw.Write([]byte{5, 'h', 'e', 'l', 'l', 'o'}) // a single framed message (implicit big endian, small payload)
	r := framer.NewReader(&raw, framer.WithReadTCP()).(*framer.Reader)

	spy := &spyReader{r: r, wt: r.WriteTo}

	var dst bytes.Buffer
	// Use iox.CopyPolicy default which prefers fast-path when available.
	n, err := iox.CopyPolicy(&dst, spy, &iox.ReturnPolicy{})
	if err != nil || n != 5 || dst.String() != "hello" {
		t.Fatalf("n=%d err=%v dst=%q", n, err, dst.String())
	}
	if spy.called == 0 {
		t.Fatalf("WriterTo was not used by CopyPolicy")
	}
}

// wouldBlockWriter is defined in framer_test.go; reuse it here.

func TestReader_WriteTo_WouldBlock_ReadSide(t *testing.T) {
	// Build a scripted reader: header (len=5), then 2 payload bytes, then would-block.
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{5}, err: nil},
		{b: nil, err: iox.ErrWouldBlock},
		{b: []byte("hello"), err: io.EOF},
	}}
	r := framer.NewReader(sr, framer.WithReadTCP()).(*framer.Reader)

	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if !errors.Is(err, iox.ErrWouldBlock) || n != 0 {
		t.Fatalf("want (0, ErrWouldBlock), got (%d, %v)", n, err)
	}

	// Resume: now complete the remaining data using the same fast path.
	n2, err2 := r.WriteTo(&dst)
	if err2 != nil || n2 != 5 || dst.String() != "hello" {
		t.Fatalf("resume n=%d err=%v dst=%q", n2, err2, dst.String())
	}
}

func TestReader_WriteTo_WouldBlock_WriteSide(t *testing.T) {
	// Prepare one message in raw buffer.
	var raw bytes.Buffer
	raw.Write([]byte{3, 'b', 'y', 't'})
	r := framer.NewReader(&raw, framer.WithReadTCP()).(*framer.Reader)

	dst := &wouldBlockWriter{limit: 2}
	n, err := r.WriteTo(dst)
	if !errors.Is(err, iox.ErrWouldBlock) || n != 2 {
		t.Fatalf("want (2, ErrWouldBlock), got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_PropagatesErrMore(t *testing.T) {
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: nil, err: iox.ErrMore}, // semantic signal without progress
	}}
	r := framer.NewReader(sr, framer.WithReadTCP()).(*framer.Reader)
	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if !errors.Is(err, iox.ErrMore) || n != 0 {
		t.Fatalf("want (0, ErrMore), got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_PropagatesUnexpectedEOF_MidHeader(t *testing.T) {
	// Simulate stream ending mid-header: partial header byte then EOF.
	// For extended length (0xFE), we need 3 bytes total but only get 1.
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{0xFE}, err: nil}, // header byte indicating 2-byte extended length
		{b: nil, err: io.EOF},       // EOF before extended length bytes
	}}
	r := framer.NewReader(sr, framer.WithReadTCP()).(*framer.Reader)
	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	// Must propagate io.ErrUnexpectedEOF, not convert to nil.
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want io.ErrUnexpectedEOF, got (%d, %v)", n, err)
	}
}

// --- Packet protocol WriteTo tests ---

func TestReader_WriteTo_Packet_Correctness(t *testing.T) {
	// SeqPacket/Datagram: pass-through copy.
	msgs := [][]byte{
		[]byte("hello"),
		[]byte("world"),
		bytes.Repeat([]byte{'p'}, 1024),
	}

	for _, proto := range []framer.Protocol{framer.SeqPacket, framer.Datagram} {
		// Concatenate all messages as raw packets (no framing).
		var src bytes.Buffer
		for _, m := range msgs {
			src.Write(m)
		}

		// Use a scripted reader that returns one packet per read.
		sr := &scriptedReader{steps: make([]struct {
			b   []byte
			err error
		}, len(msgs)+1)}
		for i, m := range msgs {
			sr.steps[i] = struct {
				b   []byte
				err error
			}{b: m, err: nil}
		}
		sr.steps[len(msgs)] = struct {
			b   []byte
			err error
		}{b: nil, err: io.EOF}

		r := framer.NewReader(sr, framer.WithProtocol(proto)).(*framer.Reader)
		var dst bytes.Buffer
		n, err := r.WriteTo(&dst)
		if err != nil {
			t.Fatalf("proto=%d: err=%v", proto, err)
		}

		// Total bytes written should equal sum of all message lengths.
		var totalLen int64
		for _, m := range msgs {
			totalLen += int64(len(m))
		}
		if n != totalLen {
			t.Fatalf("proto=%d: n=%d want=%d", proto, n, totalLen)
		}
	}
}

func TestReader_WriteTo_Packet_WouldBlock_ReadSide(t *testing.T) {
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: nil, err: iox.ErrWouldBlock},
		{b: []byte("packet"), err: nil},
		{b: nil, err: io.EOF},
	}}
	r := framer.NewReader(sr, framer.WithProtocol(framer.SeqPacket)).(*framer.Reader)

	var dst bytes.Buffer
	n1, err1 := r.WriteTo(&dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 0 {
		t.Fatalf("first: want (0, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	// Resume.
	n2, err2 := r.WriteTo(&dst)
	if err2 != nil || n2 != 6 || dst.String() != "packet" {
		t.Fatalf("resume: n=%d err=%v dst=%q", n2, err2, dst.String())
	}
}

func TestReader_WriteTo_Packet_WouldBlock_WriteSide(t *testing.T) {
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte("packet-data"), err: nil},
		{b: nil, err: io.EOF},
	}}
	r := framer.NewReader(sr, framer.WithProtocol(framer.SeqPacket)).(*framer.Reader)

	dst := &wouldBlockWriter{limit: 3}
	n, err := r.WriteTo(dst)
	if !errors.Is(err, iox.ErrWouldBlock) || n != 3 {
		t.Fatalf("want (3, ErrWouldBlock), got (%d, %v)", n, err)
	}
}

// dataErrReader returns data and error together in a single Read call.
type dataErrReader struct {
	data []byte
	err  error
	done bool
}

func (r *dataErrReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	n := copy(p, r.data)
	return n, r.err
}

func TestReader_WriteTo_Packet_ErrMore_ReadSide(t *testing.T) {
	// Use a reader that returns data AND ErrMore together.
	r := framer.NewReader(&dataErrReader{
		data: []byte("part1"),
		err:  iox.ErrMore,
	}, framer.WithProtocol(framer.SeqPacket)).(*framer.Reader)

	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if !errors.Is(err, iox.ErrMore) {
		t.Fatalf("want ErrMore, got (%d, %v)", n, err)
	}
	// Progress should be reported.
	if n != 5 || dst.String() != "part1" {
		t.Fatalf("n=%d dst=%q", n, dst.String())
	}
}

// zeroWriter always returns (0, nil) - a pathological writer.
type zeroWriter struct{}

func (zeroWriter) Write(p []byte) (int, error) { return 0, nil }

func TestReader_WriteTo_Packet_ErrShortWrite(t *testing.T) {
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte("data"), err: nil},
	}}
	r := framer.NewReader(sr, framer.WithProtocol(framer.SeqPacket)).(*framer.Reader)

	n, err := r.WriteTo(zeroWriter{})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("want io.ErrShortWrite, got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_Stream_ErrTooLong(t *testing.T) {
	// Build a framed message larger than the default 64KiB cap.
	// Header: 0xFF + 7 bytes for length (128KiB = 131072).
	payload := bytes.Repeat([]byte{'x'}, 128*1024)
	var raw bytes.Buffer
	w := framer.NewWriter(&raw, framer.WithWriteTCP())
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Reader with no ReadLimit uses default 64KiB cap for WriteTo.
	r := framer.NewReader(&raw, framer.WithReadTCP()).(*framer.Reader)
	var dst bytes.Buffer
	_, err := r.WriteTo(&dst)
	if !errors.Is(err, framer.ErrTooLong) {
		t.Fatalf("want ErrTooLong, got %v", err)
	}
}

func TestReader_WriteTo_Stream_ErrShortWrite(t *testing.T) {
	var raw bytes.Buffer
	raw.Write([]byte{4, 'd', 'a', 't', 'a'})
	r := framer.NewReader(&raw, framer.WithReadTCP()).(*framer.Reader)

	n, err := r.WriteTo(zeroWriter{})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("want io.ErrShortWrite, got (%d, %v)", n, err)
	}
}
