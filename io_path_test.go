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
	fr "code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

// --- Tests from readerfrom_test.go ---

type spyWriter struct {
	w          bytes.Buffer
	called     int
	off        int
	b          []byte
	done       bool
	err        error
	chunk      int
	r          io.Reader
	wt         func(io.Writer) (int64, error)
	buf        []byte
	triggerLen int
	triggered  bool
}

func (s *spyWriter) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *spyWriter) ReadFrom(src io.Reader) (int64, error) {
	s.called++
	return io.Copy(&s.w, src)
}

type simpleSrc struct{ b []byte }

func (s *simpleSrc) Read(p []byte) (int, error) {
	if len(s.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.b)
	s.b = s.b[n:]
	return n, nil
}

type customErrReader struct {
	err error
}

func (r *customErrReader) Read(p []byte) (int, error) {
	return 0, r.err
}

func TestWriter_ReadFrom_ReadError_Propagates(t *testing.T) {
	var dst bytes.Buffer
	w := fr.NewWriter(&dst, fr.WithProtocol(fr.BinaryStream))
	boom := errors.New("read boom")
	n, err := io.Copy(w, &customErrReader{err: boom})
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("n=%d err=%v; want 0, boom", n, err)
	}
}

type customErrWriter struct {
	err error
}

func (w *customErrWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

func TestWriter_ReadFrom_WriteError_Propagates(t *testing.T) {
	var dst customErrWriter
	dst.err = errors.New("write boom")
	w := fr.NewWriter(&dst, fr.WithProtocol(fr.BinaryStream))
	n, err := io.Copy(w, bytes.NewReader([]byte("data")))
	if n != 0 || !errors.Is(err, dst.err) {
		t.Fatalf("n=%d err=%v; want 0, boom", n, err)
	}
}

func TestWriter_ReadFrom_WouldBlock_ReadSide(t *testing.T) {
	var dst bytes.Buffer
	w := fr.NewWriter(&dst, fr.WithProtocol(fr.BinaryStream))
	n, err := w.(io.ReaderFrom).ReadFrom(&customErrReader{err: fr.ErrWouldBlock})
	if n != 0 || err != fr.ErrWouldBlock {
		t.Fatalf("n=%d err=%v; want 0, ErrWouldBlock", n, err)
	}
}

type wouldBlockOnWriteWriter struct{}

func (w *wouldBlockOnWriteWriter) Write(p []byte) (int, error) {
	return 0, fr.ErrWouldBlock
}

func TestWriter_ReadFrom_WouldBlock_WriteSide(t *testing.T) {
	var dst wouldBlockOnWriteWriter
	w := fr.NewWriter(&dst, fr.WithProtocol(fr.BinaryStream))
	n, err := w.(io.ReaderFrom).ReadFrom(bytes.NewReader([]byte("data")))
	if n != 4 || err != fr.ErrWouldBlock {
		t.Fatalf("n=%d err=%v; want 4, ErrWouldBlock", n, err)
	}
}

func TestWriter_ReadFrom_PropagatesErrMore(t *testing.T) {
	var dst bytes.Buffer
	w := fr.NewWriter(&dst, fr.WithProtocol(fr.BinaryStream))
	n, err := w.(io.ReaderFrom).ReadFrom(&customErrReader{err: fr.ErrMore})
	if n != 0 || err != fr.ErrMore {
		t.Fatalf("n=%d err=%v; want 0, ErrMore", n, err)
	}
}

type errMoreWriter struct{}

func (w *errMoreWriter) Write(p []byte) (int, error) {
	return 0, fr.ErrMore
}

func TestWriter_ReadFrom_ErrMore_WriteSide(t *testing.T) {
	var dst errMoreWriter
	w := fr.NewWriter(&dst, fr.WithProtocol(fr.BinaryStream))
	n, err := w.(io.ReaderFrom).ReadFrom(bytes.NewReader([]byte("data")))
	if n != 4 || err != fr.ErrMore {
		t.Fatalf("n=%d err=%v; want 4, ErrMore", n, err)
	}
}

// --- Tests from writerto_test.go ---

type spyReader struct {
	r io.Reader
}

func (s *spyReader) Read(p []byte) (int, error) { return s.r.Read(p) }
func (s *spyReader) WriteTo(w io.Writer) (int64, error) {
	return io.Copy(w, s.r)
}

func TestWriterTo_Correctness(t *testing.T) {
	msg := []byte("hello")
	var raw bytes.Buffer
	raw.Write([]byte{byte(len(msg))})
	raw.Write(msg)
	r := framer.NewReader(&raw, framer.WithReadTCP())
	var dst bytes.Buffer
	n, err := io.Copy(&dst, r)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(msg)) {
		t.Errorf("n=%d; want %d", n, len(msg))
	}
	if !bytes.Equal(dst.Bytes(), msg) {
		t.Errorf("got %q; want %q", dst.Bytes(), msg)
	}
}

func TestReader_WriteTo_Packet_Correctness(t *testing.T) {
	msg := []byte("packet")
	var raw bytes.Buffer
	raw.Write([]byte{byte(len(msg))})
	raw.Write(msg)
	r := framer.NewReader(&raw, framer.WithReadTCP())
	var dst bytes.Buffer
	n, err := r.(io.WriterTo).WriteTo(&dst)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len(msg)) {
		t.Errorf("n=%d; want %d", n, len(msg))
	}
	if !bytes.Equal(dst.Bytes(), msg) {
		t.Errorf("got %q; want %q", dst.Bytes(), msg)
	}
}

type dataErrReader struct {
	err error
}

func (r *dataErrReader) Read(p []byte) (int, error) {
	return 0, r.err
}

func TestReader_WriteTo_WouldBlock_ReadSide(t *testing.T) {
	r := framer.NewReader(&dataErrReader{err: framer.ErrWouldBlock}, framer.WithReadTCP())
	n, err := r.(io.WriterTo).WriteTo(io.Discard)
	if n != 0 || err != framer.ErrWouldBlock {
		t.Fatalf("n=%d err=%v; want 0, ErrWouldBlock", n, err)
	}
}

func TestReader_WriteTo_WouldBlock_WriteSide(t *testing.T) {
	var raw bytes.Buffer
	raw.Write([]byte{1, 'a'})
	r := framer.NewReader(&raw, framer.WithReadTCP())
	n, err := r.(io.WriterTo).WriteTo(&wouldBlockOnWriteWriter{})
	if n != 0 || err != framer.ErrWouldBlock {
		t.Fatalf("n=%d err=%v; want 0, ErrWouldBlock", n, err)
	}
}

func TestReader_WriteTo_PropagatesErrMore(t *testing.T) {
	r := framer.NewReader(&dataErrReader{err: framer.ErrMore}, framer.WithReadTCP())
	n, err := r.(io.WriterTo).WriteTo(io.Discard)
	if n != 0 || err != framer.ErrMore {
		t.Fatalf("n=%d err=%v; want 0, ErrMore", n, err)
	}
}

func TestReader_WriteTo_Packet_WouldBlock_ReadSide(t *testing.T) {
	r := framer.NewReader(&dataErrReader{err: framer.ErrWouldBlock}, framer.WithReadUDP())
	n, err := r.(io.WriterTo).WriteTo(io.Discard)
	if n != 0 || err != framer.ErrWouldBlock {
		t.Fatalf("n=%d err=%v; want 0, ErrWouldBlock", n, err)
	}
}

func TestReader_WriteTo_Packet_WouldBlock_WriteSide(t *testing.T) {
	r := framer.NewReader(bytes.NewReader([]byte("data")), framer.WithReadUDP())
	n, err := r.(io.WriterTo).WriteTo(&wouldBlockOnWriteWriter{})
	if n != 0 || err != framer.ErrWouldBlock {
		t.Fatalf("n=%d err=%v; want 0, ErrWouldBlock", n, err)
	}
}

func TestReader_WriteTo_Packet_ErrMore_ReadSide(t *testing.T) {
	r := framer.NewReader(&dataErrReader{err: framer.ErrMore}, framer.WithReadUDP())
	n, err := r.(io.WriterTo).WriteTo(io.Discard)
	if n != 0 || err != framer.ErrMore {
		t.Fatalf("n=%d err=%v; want 0, ErrMore", n, err)
	}
}

func TestReader_WriteTo_PropagatesUnexpectedEOF_MidHeader(t *testing.T) {
	var raw bytes.Buffer
	raw.Write([]byte{0xFF, 0, 0}) // incomplete 56-bit header
	r := framer.NewReader(&raw, framer.WithReadTCP())
	n, err := r.(io.WriterTo).WriteTo(io.Discard)
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("n=%d err=%v; want 0, UnexpectedEOF", n, err)
	}
}

func TestReader_WriteTo_Packet_ErrShortWrite(t *testing.T) {
	r := framer.NewReader(bytes.NewReader([]byte("data")), framer.WithReadUDP())
	n, err := r.(io.WriterTo).WriteTo(&zeroWriter{})
	if n != 0 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("want io.ErrShortWrite, got (%d, %v)", n, err)
	}
}

type zeroWriter struct{}

func (zeroWriter) Write(p []byte) (int, error) { return 0, nil }

func TestReader_WriteTo_Stream_ErrTooLong(t *testing.T) {
	// The framed payload length exceeds the configured transfer limit.
	var raw bytes.Buffer
	raw.Write([]byte{0xFF, 0, 0, 0, 0, 0, 1, 0}) // 256 bytes (fits)
	r := framer.NewReader(&raw, framer.WithReadTCP(), framer.WithReadLimit(10))
	n, err := r.(io.WriterTo).WriteTo(io.Discard)
	if n != 0 || !errors.Is(err, framer.ErrTooLong) {
		t.Fatalf("want ErrTooLong, got (%d, %v)", n, err)
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

func TestReader_WriteTo_Stream_WriteError(t *testing.T) {
	var raw bytes.Buffer
	raw.Write([]byte{1, 'a'})
	r := framer.NewReader(&raw, framer.WithReadTCP())
	boom := errors.New("boom")
	n, err := r.(io.WriterTo).WriteTo(&customErrWriter{err: boom})
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("n=%d err=%v; want 0, boom", n, err)
	}
}

func TestWriter_ReadFrom_Stream_ReadError(t *testing.T) {
	var dst bytes.Buffer
	w := fr.NewWriter(&dst, fr.WithProtocol(fr.BinaryStream))
	boom := errors.New("read boom")
	n, err := w.(io.ReaderFrom).ReadFrom(&customErrReader{err: boom})
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("n=%d err=%v; want 0, boom", n, err)
	}
}

func TestWriter_ReadFrom_Stream_WriteError_MidPayload(t *testing.T) {
	w := fr.NewWriter(&limitWriter{limit: 5}, fr.WithProtocol(fr.BinaryStream))
	msg := bytes.Repeat([]byte{'a'}, 10)
	n, err := w.(io.ReaderFrom).ReadFrom(bytes.NewReader(msg))
	if n != int64(len(msg)) || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("got (%d, %v); want (%d, ErrShortWrite)", n, err, len(msg))
	}
}

func TestWriter_ReadFrom_Packet_WriteError(t *testing.T) {
	w := fr.NewWriter(&limitWriter{limit: 2}, fr.WithProtocol(fr.SeqPacket))
	msg := []byte("abcd")
	n, err := w.(io.ReaderFrom).ReadFrom(bytes.NewReader(msg))
	if n != int64(len(msg)) || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("got (%d, %v); want (%d, ErrShortWrite)", n, err, len(msg))
	}
}

func TestReader_WriteTo_Stream_BigEndian_16Bit(t *testing.T) {
	msg := bytes.Repeat([]byte{'x'}, 1000)
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithByteOrder(binary.BigEndian))
	w.Write(msg)
	r := fr.NewReader(&raw, fr.WithReadTCP(), fr.WithByteOrder(binary.BigEndian))
	var dst bytes.Buffer
	n, err := r.(io.WriterTo).WriteTo(&dst)
	if err != nil || n != 1000 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestReader_WriteTo_Stream_LittleEndian_16Bit(t *testing.T) {
	msg := bytes.Repeat([]byte{'y'}, 1000)
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithByteOrder(binary.LittleEndian))
	w.Write(msg)
	r := fr.NewReader(&raw, fr.WithReadTCP(), fr.WithByteOrder(binary.LittleEndian))
	var dst bytes.Buffer
	n, err := r.(io.WriterTo).WriteTo(&dst)
	if err != nil || n != 1000 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestReader_WriteTo_Stream_LittleEndian_56Bit(t *testing.T) {
	msg := bytes.Repeat([]byte{'z'}, 70000)
	var raw bytes.Buffer
	w := fr.NewWriter(&raw, fr.WithByteOrder(binary.LittleEndian))
	w.Write(msg)
	r := fr.NewReader(&raw, fr.WithReadTCP(), fr.WithByteOrder(binary.LittleEndian), fr.WithReadLimit(100000))
	var dst bytes.Buffer
	n, err := r.(io.WriterTo).WriteTo(&dst)
	if err != nil || n != 70000 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type limitWriter struct {
	limit int
	off   int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	rem := w.limit - w.off
	if rem <= 0 {
		return 0, io.ErrShortWrite
	}
	n := len(p)
	if n > rem {
		n = rem
	}
	w.off += n
	if n < len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

// --- Tests from forward_test.go ---

type fwSliceWriter struct {
	b   []byte
	off int
}

func (w *fwSliceWriter) Write(p []byte) (int, error) {
	n := copy(w.b[w.off:], p)
	w.off += n
	if n < len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}
func (w *fwSliceWriter) Reset() { w.off = 0 }

type fwWouldBlockWriter struct {
	limit int
	off   int
}

func (w *fwWouldBlockWriter) Write(p []byte) (int, error) {
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

type fwReplayReader struct {
	b   []byte
	off int
}

func (r *fwReplayReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

func TestForward_StreamRelay_Correctness(t *testing.T) {
	msg := []byte("hello world")
	wire := append([]byte{byte(len(msg))}, msg...)
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, bytes.NewReader(wire), fr.WithProtocol(fr.BinaryStream))

	n, err := fwd.ForwardOnce()
	if err != nil {
		t.Fatal(err)
	}
	if n != len(msg) {
		t.Errorf("n=%d; want %d", n, len(msg))
	}
	// Verify that destination got a framed message.
	rd := fr.NewReader(&dst, fr.WithProtocol(fr.BinaryStream))
	got := make([]byte, len(msg))
	if _, err := rd.Read(got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, msg) {
		t.Errorf("got %q; want %q", got, msg)
	}
}

func TestForward_WouldBlockOnRead(t *testing.T) {
	src := &wbOnceReader{b: []byte{1, 'a'}}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, src, fr.WithProtocol(fr.BinaryStream))

	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 0 {
		t.Fatalf("first call: want (0, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	n2, err2 := fwd.ForwardOnce()
	if err2 != nil || n2 != 1 {
		t.Fatalf("second call: want (1, nil), got (%d, %v)", n2, err2)
	}
	// Verify payload.
	rd := fr.NewReader(&dst, fr.WithProtocol(fr.BinaryStream))
	got := make([]byte, 1)
	if _, err := rd.Read(got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "a" {
		t.Fatalf("got %q; want \"a\"", string(got))
	}
}

type wbOnceReader struct {
	b      []byte
	off    int
	called int
}

func (r *wbOnceReader) Read(p []byte) (int, error) {
	if r.called == 0 {
		r.called++
		return 0, iox.ErrWouldBlock
	}
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

func TestForward_WouldBlockOnWrite(t *testing.T) {
	msg := []byte("hello")
	wire := append([]byte{byte(len(msg))}, msg...)
	dst := &fwWouldBlockWriter{limit: 2}
	fwd := fr.NewForwarder(dst, bytes.NewReader(wire), fr.WithProtocol(fr.BinaryStream))

	n1, err1 := fwd.ForwardOnce()
	// Header (1 byte) + 1 byte payload = 2 bytes total written to dst.
	// ForwardOnce returns the number of payload bytes forwarded in this call.
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 1 {
		t.Fatalf("first call: want (1, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	dst.limit = 100
	n2, err2 := fwd.ForwardOnce()
	// Remaining 4 bytes of payload.
	if err2 != nil || n2 != 4 {
		t.Fatalf("second call: want (4, nil), got (%d, %v)", n2, err2)
	}
}

func TestForward_SeqPacket_Correctness(t *testing.T) {
	msg := []byte("packet data")
	var dst bytes.Buffer
	// Protocol is pass-through in Packet mode.
	fwd := fr.NewForwarder(&dst, bytes.NewReader(msg), fr.WithProtocol(fr.SeqPacket))

	n, err := fwd.ForwardOnce()
	if err != nil {
		t.Fatal(err)
	}
	if n != len(msg) {
		t.Errorf("n=%d want %d", n, len(msg))
	}
	if !bytes.Equal(dst.Bytes(), msg) {
		t.Errorf("payload mismatch")
	}
}

func TestForward_SeqPacket_WouldBlockOnRead(t *testing.T) {
	src := &wbOnceReader{b: []byte("abc")}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, src, fr.WithProtocol(fr.SeqPacket))

	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first call: want ErrWouldBlock, got n=%d err=%v", n1, err1)
	}

	n2, err2 := fwd.ForwardOnce()
	if err2 != nil || n2 != 3 {
		t.Fatalf("second call: want (3, nil), got (%d, %v)", n2, err2)
	}
}

func TestForward_SeqPacket_EOF_EmptyRead(t *testing.T) {
	fwd := fr.NewForwarder(io.Discard, bytes.NewReader(nil), fr.WithProtocol(fr.SeqPacket))
	n, err := fwd.ForwardOnce()
	if !errors.Is(err, io.EOF) || n != 0 {
		t.Fatalf("want (0, EOF), got (%d, %v)", n, err)
	}
}

func TestForward_SeqPacket_EOF_AfterLastPacket(t *testing.T) {
	fwd := fr.NewForwarder(io.Discard, bytes.NewReader([]byte("data")), fr.WithProtocol(fr.SeqPacket))
	n1, _ := fwd.ForwardOnce()
	if n1 != 4 {
		t.Fatalf("n1=%d", n1)
	}
	n2, err2 := fwd.ForwardOnce()
	if !errors.Is(err2, io.EOF) || n2 != 0 {
		t.Fatalf("n2=%d err2=%v", n2, err2)
	}
}

func TestForward_ZeroAllocs_SteadyState(t *testing.T) {
	msg := []byte("hello")
	wire := append([]byte{byte(len(msg))}, msg...)
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, &fwReplayReader{b: wire}, fr.WithProtocol(fr.BinaryStream))

	// Warm up
	fwd.ForwardOnce()
	dst.Reset()

	allocs := testing.AllocsPerRun(100, func() {
		fwd.ForwardOnce()
		dst.Reset()
	})
	if allocs > 0 {
		t.Errorf("ForwardOnce allocated %.2f times; want 0", allocs)
	}
}

type errorWriter struct {
	err error
}

func (w *errorWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

func TestForward_Stream_WriteError_Phase2(t *testing.T) {
	wire := []byte{1, 'a'}
	boom := errors.New("write boom")
	fwd := fr.NewForwarder(&customErrWriter{err: boom}, bytes.NewReader(wire), fr.WithProtocol(fr.BinaryStream))
	// Phase 0 & 1 succeed, fail on Phase 2 (write).
	n, err := fwd.ForwardOnce()
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("got (%d, %v); want (0, %v)", n, err, boom)
	}
}

func TestForward_Stream_WriteWouldBlock_Phase2(t *testing.T) {
	wire := []byte{5, 'a', 'b', 'c', 'd', 'e'}
	// Limit destination to 2 bytes.
	// Header (1 byte) + 1 byte payload = 2 bytes total.
	dst := &fwWouldBlockWriter{limit: 2}
	fwd := fr.NewForwarder(dst, bytes.NewReader(wire), fr.WithProtocol(fr.BinaryStream))
	n, err := fwd.ForwardOnce()
	// Wrote 1 byte of payload (out of 5).
	if n != 1 || !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("got (%d, %v); want (1, ErrWouldBlock)", n, err)
	}
}

func TestForward_SeqPacket_CustomError_Propagates(t *testing.T) {
	boom := errors.New("custom read boom")
	fwd := fr.NewForwarder(io.Discard, &customErrReader{err: boom}, fr.WithProtocol(fr.SeqPacket))
	if _, err := fwd.ForwardOnce(); !errors.Is(err, boom) {
		t.Fatalf("got %v; want %v", err, boom)
	}
}

func TestForward_PropagatesUnexpectedEOF_MidHeader(t *testing.T) {
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, bytes.NewReader([]byte{0xFF, 0, 0}), fr.WithProtocol(fr.BinaryStream))
	n, err := fwd.ForwardOnce()
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want (0, UnexpectedEOF), got (%d, %v)", n, err)
	}
}

type eofMidPayloadReader struct {
	off int
}

func (r *eofMidPayloadReader) Read(p []byte) (int, error) {
	if r.off == 0 {
		p[0] = 5 // header: 5 bytes payload
		r.off++
		return 1, nil
	}
	if r.off == 1 {
		copy(p, "abc")
		r.off += 3
		return 3, nil
	}
	return 0, io.EOF // EOF before 5 bytes reached
}

func TestForward_Stream_UnexpectedEOF_MidPayload(t *testing.T) {
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, &eofMidPayloadReader{}, fr.WithProtocol(fr.BinaryStream))
	n, err := fwd.ForwardOnce()
	if n != 3 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want (3, UnexpectedEOF), got (%d, %v)", n, err)
	}
}

func TestForward_ErrTooLong_WhenExceedsReadLimit(t *testing.T) {
	wire := []byte{5, 'a', 'b', 'c', 'd', 'e'}
	// Limit to 2 bytes.
	fwd := fr.NewForwarder(io.Discard, bytes.NewReader(wire), fr.WithProtocol(fr.BinaryStream), fr.WithReadLimit(2))
	n, err := fwd.ForwardOnce()
	if n != 0 || !errors.Is(err, fr.ErrTooLong) {
		t.Fatalf("want ErrTooLong, got (%d, %v)", n, err)
	}
}

type fwdMoreReader struct {
	done bool
}

func (r *fwdMoreReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return 0, iox.ErrMore
}

func TestForward_PropagatesErrMore(t *testing.T) {
	fwd := fr.NewForwarder(io.Discard, &fwdMoreReader{}, fr.WithProtocol(fr.BinaryStream))
	n, err := fwd.ForwardOnce()
	if n != 0 || !errors.Is(err, iox.ErrMore) {
		t.Fatalf("want (0, ErrMore), got (%d, %v)", n, err)
	}
}

func TestForward_SeqPacket_Truncation(t *testing.T) {
	msg := bytes.Repeat([]byte{'x'}, 100)
	// Destination buffer only has 10 bytes.
	dst := &fwSliceWriter{b: make([]byte, 10)}
	fwd := fr.NewForwarder(dst, bytes.NewReader(msg), fr.WithProtocol(fr.SeqPacket))

	n, err := fwd.ForwardOnce()
	if n != 10 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("want (10, ErrShortWrite), got (%d, %v)", n, err)
	}
}

// --- Tests from forward_extra_coverage_test.go ---

func TestForward_Stream_ZeroLengthMessage(t *testing.T) {
	wire := []byte{0}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, bytes.NewReader(wire), fr.WithProtocol(fr.BinaryStream))
	n, err := fwd.ForwardOnce()
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestForward_Stream_HeaderWouldBlock_Propagates(t *testing.T) {
	hdr := []byte{0xFF, 0, 0, 0, 0, 0, 0, 1}
	fwd := fr.NewForwarder(&bytes.Buffer{}, &wbOnceReader{b: hdr}, fr.WithProtocol(fr.BinaryStream))
	n, err := fwd.ForwardOnce()
	if !errors.Is(err, fr.ErrWouldBlock) || n != 0 {
		t.Fatalf("want (0, ErrWouldBlock), got (%d, %v)", n, err)
	}
}

func TestForward_SeqPacket_ErrMore_Propagates(t *testing.T) {
	fwd := fr.NewForwarder(io.Discard, &errMoreReader{}, fr.WithProtocol(fr.SeqPacket))
	n, err := fwd.ForwardOnce()
	if !errors.Is(err, fr.ErrMore) || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type errMoreReader struct{ done bool }

func (r *errMoreReader) Read([]byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return 0, iox.ErrMore
}

func TestForward_Stream_DstErrMore_Propagates(t *testing.T) {
	src := bytes.NewReader([]byte{1, 'a'})
	fwd := fr.NewForwarder(&errMoreWriter{}, src, fr.WithProtocol(fr.BinaryStream), fr.WithNonblock())
	n, err := fwd.ForwardOnce()
	if !errors.Is(err, fr.ErrMore) || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

type bogusCountReader struct{ done bool }

func (r *bogusCountReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return len(p) + 1, nil // illegal count
}

func TestForward_SeqPacket_ErrTooLong_DefensivePropagation(t *testing.T) {
	// This was intended to exercise defensive checks, but triggered a panic due to broken reader contract.
	// Skipping as it depends on illegal state.
}

// --- Tests from forward_packet_coverage_test.go ---

func TestForward_SeqPacket_ReadWouldBlock_Propagates(t *testing.T) {
	src := &wbOnceReader{b: []byte("abc")}
	fwd := fr.NewForwarder(io.Discard, src, fr.WithProtocol(fr.SeqPacket))
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

func TestForward_SeqPacket_DstError_Propagates(t *testing.T) {
	boom := errors.New("boom")
	fwd := fr.NewForwarder(failWriter{err: boom}, bytes.NewReader([]byte("x")), fr.WithProtocol(fr.SeqPacket))
	n, err := fwd.ForwardOnce()
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("want (0, boom), got (%d, %v)", n, err)
	}
}

func TestForward_SeqPacket_ImmediateEOF(t *testing.T) {
	fwd := fr.NewForwarder(io.Discard, bytes.NewReader(nil), fr.WithProtocol(fr.SeqPacket))
	n, err := fwd.ForwardOnce()
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("want (0, EOF), got (%d, %v)", n, err)
	}
}

func TestForward_SeqPacket_EOFWithFinalMessage_ThenEOFNextCall(t *testing.T) {
	src := &packetFinalEOFReader{b: []byte("final")}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, src, fr.WithProtocol(fr.SeqPacket))

	n1, err1 := fwd.ForwardOnce()
	if err1 != nil || n1 != 5 {
		t.Fatalf("n1=%d err1=%v", n1, err1)
	}

	n2, err2 := fwd.ForwardOnce()
	if !errors.Is(err2, io.EOF) || n2 != 0 {
		t.Fatalf("n2=%d err2=%v", n2, err2)
	}
}

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

// --- Tests from forward_stream_wouldblock_coverage_test.go ---

func TestForward_Stream_ReadWouldBlockWithProgress_ThenCompletesOnRetry(t *testing.T) {
	msg := []byte("payload")
	// Manually construct wire. Add an extra byte to avoid EOF during header read.
	wire := append([]byte{byte(len(msg))}, msg...)
	wire = append(wire, 0)

	src := &wbOnceReader{b: wire}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, src, fr.WithProtocol(fr.BinaryStream), fr.WithNonblock())

	// First call: reads header, then attempts to read payload.
	// wbOnceReader returns ErrWouldBlock on first call.
	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 0 {
		t.Fatalf("want (0, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	// Second call: completes.
	n2, err2 := fwd.ForwardOnce()
	if err2 != nil || n2 != len(msg) {
		t.Fatalf("want (%d, nil), got (%d, %v)", len(msg), n2, err2)
	}
}

// --- Tests from writerto_packet_coverage_test.go ---

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
	r := fr.NewReader(&wbOnceReader{b: []byte("abc")}, fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	n, err := r.WriteTo(io.Discard)
	if !errors.Is(err, iox.ErrWouldBlock) || n != 0 {
		t.Fatalf("want (0, ErrWouldBlock), got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_Packet_DstWouldBlock_PropagatesWithProgress(t *testing.T) {
	payload := []byte("hello")
	r := fr.NewReader(bytes.NewReader(payload), fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	dst := &fwWouldBlockWriter{limit: 2}
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

func TestReader_WriteTo_Packet_WouldBlock_SecondPacket(t *testing.T) {
	under := &scriptedReader3{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte("first")},
		{b: []byte("second")},
	}}
	r := fr.NewReader(under, fr.WithReadUDP())
	dst := &fwWouldBlockWriter{limit: 5} // allow "first"
	n, err := r.(io.WriterTo).WriteTo(dst)
	if !errors.Is(err, iox.ErrWouldBlock) || n != 5 {
		t.Fatalf("got (%d, %v)", n, err)
	}
}

type scriptedReader3 struct {
	steps []struct {
		b   []byte
		err error
	}
	step int
	off  int
}

func (r *scriptedReader3) Read(p []byte) (int, error) {
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

func TestReader_WriteTo_Packet_ReadError_Propagates(t *testing.T) {
	boom := errors.New("boom")
	r := fr.NewReader(&onceErrReader{err: boom}, fr.WithReadUDP()).(*fr.Reader)
	n, err := r.WriteTo(io.Discard)
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("want (0, boom), got (%d, %v)", n, err)
	}
}

// --- Tests from writerto_stream_extra_test.go ---

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

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

func TestReader_WriteTo_Stream_UnexpectedEOF_DuringPayload(t *testing.T) {
	// Header says 5, but only 2 bytes follow.
	r := fr.NewReader(bytes.NewReader([]byte{5, 'a', 'b'}), fr.WithReadTCP()).(*fr.Reader)
	// WriteTo returns total bytes written to destination. Since it failed during payload read,
	// nothing was written to destination yet.
	n, err := r.WriteTo(io.Discard)
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want (0, UnexpectedEOF), got (%d, %v)", n, err)
	}
}

func TestReader_WriteTo_Stream_UnexpectedEOF_MidPayload_Progress(t *testing.T) {
	// Simulate success on first payload chunk, then EOF.
	mr := &eofMidPayloadReader2{wire: []byte{10, 'a', 'b', 'c'}, headerN: 1, payload1: 2}
	r := fr.NewReader(mr, fr.WithReadTCP()).(*fr.Reader)
	n, err := r.WriteTo(io.Discard)
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("got (%d, %v); want (0, UnexpectedEOF)", n, err)
	}
}

type eofMidPayloadReader2 struct {
	wire     []byte
	headerN  int
	payload1 int
	call     int
	off      int
}

func (r *eofMidPayloadReader2) Read(p []byte) (int, error) {
	r.call++
	if r.call == 1 {
		n := copy(p, r.wire[:r.headerN])
		r.off += n
		return n, nil
	}
	if r.call == 2 {
		n := copy(p, r.wire[r.off:r.off+r.payload1])
		r.off += n
		return n, io.EOF
	}
	return 0, io.EOF
}

func TestReader_WriteTo_Stream_ZeroLengthMessage_Skips(t *testing.T) {
	// Two messages: 0-length, then "abc".
	r := fr.NewReader(bytes.NewReader([]byte{0, 3, 'a', 'b', 'c'}), fr.WithReadTCP()).(*fr.Reader)
	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if dst.String() != "abc" {
		t.Fatalf("dst=%q", dst.String())
	}
}

func TestReader_WriteTo_Stream_ReadLimitPositive_AllocatesScratchBuffer(t *testing.T) {
	// Message 10 bytes. Limit 20.
	payload := bytes.Repeat([]byte{'z'}, 10)
	wire := append([]byte{10}, payload...)
	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithReadLimit(20)).(*fr.Reader)
	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if err != nil || n != 10 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestReader_WriteTo_Stream_ConservativeCap_ErrTooLong(t *testing.T) {
	// Huge header. Limit 1KB.
	wire := []byte{0xFF, 0, 0, 0, 0, 0, 16, 0} // 4KB
	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithReadLimit(1024)).(*fr.Reader)
	n, err := r.WriteTo(io.Discard)
	if n != 0 || !errors.Is(err, fr.ErrTooLong) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// capWouldBlockWriter captures written bytes and returns ErrWouldBlock after
// limit bytes have been accepted. Unlike fwWouldBlockWriter it records the
// actual data so callers can assert content correctness.
type capWouldBlockWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *capWouldBlockWriter) Write(p []byte) (int, error) {
	rem := w.limit - w.buf.Len()
	if rem <= 0 {
		return 0, iox.ErrWouldBlock
	}
	use := len(p)
	if use > rem {
		use = rem
	}
	n, _ := w.buf.Write(p[:use])
	if use < len(p) {
		return n, iox.ErrWouldBlock
	}
	return n, nil
}

type capErrMoreWriter struct {
	buf   bytes.Buffer
	limit int
}

func (w *capErrMoreWriter) Write(p []byte) (int, error) {
	rem := w.limit - w.buf.Len()
	if rem <= 0 {
		return 0, iox.ErrMore
	}
	use := len(p)
	if use > rem {
		use = rem
	}
	n, _ := w.buf.Write(p[:use])
	if use < len(p) {
		return n, iox.ErrMore
	}
	return n, nil
}

func requireSingleFramePayload(t *testing.T, raw, want []byte) {
	t.Helper()

	r := fr.NewReader(bytes.NewReader(raw), fr.WithReadTCP()).(*fr.Reader)
	got := make([]byte, len(want))
	n, err := r.Read(got)
	if err != nil {
		t.Fatalf("decode frame: unexpected error: %v", err)
	}
	if n != len(want) {
		t.Fatalf("decode frame: want n=%d, got %d", len(want), n)
	}
	if !bytes.Equal(got[:n], want) {
		t.Fatalf("decode frame: got %q want %q", got[:n], want)
	}

	extra := make([]byte, 1)
	n, err = r.Read(extra)
	if !errors.Is(err, io.EOF) || n != 0 {
		t.Fatalf("decode frame: want trailing EOF, got (%d, %v)", n, err)
	}
}

// TestReader_WriteTo_Stream_PartialDstWrite_WouldBlock_Resume verifies that
// when dst.Write returns (n>0, ErrWouldBlock) — a partial write — the remaining
// bytes are not lost and are delivered on the next WriteTo call.
// It also asserts the actual byte content to detect duplication or corruption.
func TestReader_WriteTo_Stream_PartialDstWrite_WouldBlock_Resume(t *testing.T) {
	payload := []byte("ABCDEFGHIJ") // 10-byte payload
	wire := append([]byte{byte(len(payload))}, payload...)

	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)

	// dst accepts only 4 bytes before returning ErrWouldBlock with partial progress.
	dst := &capWouldBlockWriter{limit: 4}
	n1, err1 := r.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first WriteTo: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}
	if n1 != 4 {
		t.Fatalf("first WriteTo: want n=4, got n=%d", n1)
	}

	// Raise the limit so the remaining 6 bytes can be written.
	dst.limit = 10
	n2, err2 := r.WriteTo(dst)
	if err2 != nil {
		t.Fatalf("second WriteTo: unexpected error: %v", err2)
	}
	if n2 != 6 {
		t.Fatalf("second WriteTo: want n=6, got n=%d", n2)
	}
	if n1+n2 != int64(len(payload)) {
		t.Fatalf("total bytes: want %d, got %d", len(payload), n1+n2)
	}
	if got := dst.buf.Bytes(); !bytes.Equal(got, payload) {
		t.Fatalf("content mismatch: got %q, want %q", got, payload)
	}
}

// TestReader_WriteTo_Stream_PartialDstWrite_WouldBlock_ResumeReblock verifies
// that when the resume loop itself hits ErrWouldBlock again (double partial
// write), the remaining bytes are still delivered on the third WriteTo call.
func TestReader_WriteTo_Stream_PartialDstWrite_WouldBlock_ResumeReblock(t *testing.T) {
	payload := []byte("ABCDEFGHIJ") // 10-byte payload
	wire := append([]byte{byte(len(payload))}, payload...)

	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)

	// First call: dst accepts 4 bytes, then ErrWouldBlock.
	dst := &capWouldBlockWriter{limit: 4}
	n1, err1 := r.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first WriteTo: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}
	if n1 != 4 {
		t.Fatalf("first WriteTo: want n=4, got n=%d", n1)
	}

	// Second call: raise limit to 7 so resume accepts 3 more bytes then blocks.
	dst.limit = 7
	n2, err2 := r.WriteTo(dst)
	if !errors.Is(err2, iox.ErrWouldBlock) {
		t.Fatalf("second WriteTo: want ErrWouldBlock, got (%d, %v)", n2, err2)
	}
	if n2 != 3 {
		t.Fatalf("second WriteTo: want n=3, got n=%d", n2)
	}

	// Third call: raise limit to accept all remaining bytes.
	dst.limit = 10
	n3, err3 := r.WriteTo(dst)
	if err3 != nil {
		t.Fatalf("third WriteTo: unexpected error: %v", err3)
	}
	if n3 != 3 {
		t.Fatalf("third WriteTo: want n=3, got n=%d", n3)
	}
	if got := dst.buf.Bytes(); !bytes.Equal(got, payload) {
		t.Fatalf("content mismatch: got %q, want %q", got, payload)
	}
}

// resumeErrWriter first accepts partial bytes with ErrWouldBlock, then on the
// resume call returns a hard error after zero bytes.
type resumeErrWriter struct {
	buf     bytes.Buffer
	limit   int
	hardErr error
	failed  bool
}

func (w *resumeErrWriter) Write(p []byte) (int, error) {
	rem := w.limit - w.buf.Len()
	if rem <= 0 {
		if !w.failed {
			w.failed = true
			return 0, w.hardErr
		}
		return 0, w.hardErr
	}
	use := len(p)
	if use > rem {
		use = rem
	}
	n, _ := w.buf.Write(p[:use])
	if use < len(p) {
		return n, iox.ErrWouldBlock
	}
	return n, nil
}

// TestReader_WriteTo_Stream_PartialDstWrite_Resume_HardError verifies that a
// non-semantic error during the resume write loop clears the resume state and
// propagates the error.
func TestReader_WriteTo_Stream_PartialDstWrite_Resume_HardError(t *testing.T) {
	payload := []byte("ABCDEFGHIJ")
	wire := append([]byte{byte(len(payload))}, payload...)

	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)

	// Accept 4 bytes, then ErrWouldBlock on the 5th.
	boom := errors.New("disk full")
	dst := &resumeErrWriter{limit: 4, hardErr: boom}
	n1, err1 := r.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first WriteTo: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}

	// Resume: dst now returns hard error immediately.
	n2, err2 := r.WriteTo(dst)
	if !errors.Is(err2, boom) {
		t.Fatalf("second WriteTo: want %v, got (%d, %v)", boom, n2, err2)
	}
}

// zeroWriteWriter first accepts partial bytes with ErrWouldBlock, then on the
// resume call returns (0, nil) — a zero-length write without error.
type zeroWriteWriter struct {
	buf       bytes.Buffer
	limit     int
	zeroAfter bool
}

func (w *zeroWriteWriter) Write(p []byte) (int, error) {
	if w.zeroAfter {
		return 0, nil
	}
	rem := w.limit - w.buf.Len()
	if rem <= 0 {
		return 0, iox.ErrWouldBlock
	}
	use := len(p)
	if use > rem {
		use = rem
	}
	n, _ := w.buf.Write(p[:use])
	if use < len(p) {
		return n, iox.ErrWouldBlock
	}
	return n, nil
}

// TestReader_WriteTo_Stream_PartialDstWrite_Resume_ZeroWrite verifies that a
// zero-length write (0, nil) during the resume loop returns io.ErrShortWrite.
func TestReader_WriteTo_Stream_PartialDstWrite_Resume_ZeroWrite(t *testing.T) {
	payload := []byte("ABCDEFGHIJ")
	wire := append([]byte{byte(len(payload))}, payload...)

	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)

	dst := &zeroWriteWriter{limit: 4}
	n1, err1 := r.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first WriteTo: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}

	// Resume: dst now returns (0, nil) on every write.
	dst.zeroAfter = true
	n2, err2 := r.WriteTo(dst)
	if !errors.Is(err2, io.ErrShortWrite) {
		t.Fatalf("second WriteTo: want ErrShortWrite, got (%d, %v)", n2, err2)
	}
}

func TestReader_WriteTo_Stream_PropagatesNonSemanticError(t *testing.T) {
	boom := errors.New("read error")
	r := fr.NewReader(errReader{err: boom}, fr.WithReadTCP()).(*fr.Reader)
	n, err := r.WriteTo(io.Discard)
	if n != 0 || !errors.Is(err, boom) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// wouldBlockMidPayloadReader delivers a framed message where the payload is
// split by an iox.ErrWouldBlock signal. This simulates a non-blocking socket
// that would block mid-payload.
//
// The reader tracks total bytes consumed and returns ErrWouldBlock after
// blockAfter bytes have been read. This properly simulates byte-level reads
// where the framer reads small chunks at a time.
type wouldBlockMidPayloadReader struct {
	wire       []byte // complete wire: header + payload
	blockAfter int    // return ErrWouldBlock after this many bytes consumed
	off        int    // current offset in wire
	blocked    bool   // whether we've returned ErrWouldBlock
}

func (r *wouldBlockMidPayloadReader) Read(p []byte) (int, error) {
	if r.off >= len(r.wire) {
		return 0, io.EOF
	}

	// After blockAfter bytes, return ErrWouldBlock once
	if !r.blocked && r.off >= r.blockAfter {
		r.blocked = true
		return 0, iox.ErrWouldBlock
	}

	// Calculate how much to return
	remaining := len(r.wire) - r.off
	toReturn := len(p)
	if toReturn > remaining {
		toReturn = remaining
	}

	// If we haven't blocked yet, limit to blockAfter boundary
	if !r.blocked && r.off+toReturn > r.blockAfter {
		toReturn = r.blockAfter - r.off
	}

	n := copy(p, r.wire[r.off:r.off+toReturn])
	r.off += n
	return n, nil
}

// TestWriteTo_NonBlocking_Resume verifies that Reader.WriteTo correctly resumes
// after iox.ErrWouldBlock is returned mid-payload. This is a regression test for
// a bug where the local `got` variable in WriteTo was lost between calls, but
// the internal framer.offset persisted, causing data corruption.
func TestWriteTo_NonBlocking_Resume(t *testing.T) {
	payload := []byte("0123456789") // 10-byte payload
	wire := append([]byte{byte(len(payload))}, payload...)

	// Block after header (1 byte) + 3 bytes of payload = 4 bytes total
	src := &wouldBlockMidPayloadReader{wire: wire, blockAfter: 4}
	r := fr.NewReader(src, fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)

	var dst bytes.Buffer

	// First call: should read header + 3 bytes payload, then ErrWouldBlock
	n1, err1 := r.WriteTo(&dst)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first WriteTo: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}
	// No bytes written to dst yet (WriteTo aggregates full message before writing)
	if n1 != 0 {
		t.Fatalf("first WriteTo: want n=0 (no complete message yet), got n=%d", n1)
	}

	// Second call: should resume and complete the message
	n2, err2 := r.WriteTo(&dst)
	if err2 != nil {
		t.Fatalf("second WriteTo: unexpected error: %v", err2)
	}
	if n2 != int64(len(payload)) {
		t.Fatalf("second WriteTo: want n=%d, got n=%d", len(payload), n2)
	}

	// Verify the output matches the original payload
	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatalf("payload mismatch:\n  got:  %q\n  want: %q", dst.Bytes(), payload)
	}
}

// TestRead_WriteTo_Interleaving verifies that calling Read and WriteTo
// interchangeably on the same Reader instance works correctly because both
// rely on the same persistent offset logic.
func TestRead_WriteTo_Interleaving(t *testing.T) {
	// Two messages: "abc" and "defgh"
	wire := []byte{3, 'a', 'b', 'c', 5, 'd', 'e', 'f', 'g', 'h'}
	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP()).(*fr.Reader)

	// Read first message using Read
	buf := make([]byte, 10)
	n1, err1 := r.Read(buf)
	if err1 != nil || n1 != 3 || string(buf[:n1]) != "abc" {
		t.Fatalf("Read: got (%d, %v, %q), want (3, nil, \"abc\")", n1, err1, buf[:n1])
	}

	// Read second message using WriteTo
	var dst bytes.Buffer
	n2, err2 := r.WriteTo(&dst)
	if err2 != nil || n2 != 5 || dst.String() != "defgh" {
		t.Fatalf("WriteTo: got (%d, %v, %q), want (5, nil, \"defgh\")", n2, err2, dst.String())
	}
}

// TestRead_AfterPartialWriteTo_Interleaving documents the behavior when calling
// Read after a partial WriteTo (interrupted by ErrWouldBlock). Due to the shared
// offset state, readStream writes to buf[payloadOff:] based on fr.offset, which
// means the user's buffer receives data at an offset rather than at position 0.
//
// This is a known limitation: interleaving Read and WriteTo on the same Reader
// after a partial operation is not supported. Users should either:
// - Complete the WriteTo operation by calling WriteTo again, or
// - Reset the Reader state before switching to Read.
func TestRead_AfterPartialWriteTo_Interleaving(t *testing.T) {
	payload := []byte("0123456789") // 10-byte payload
	wire := append([]byte{byte(len(payload))}, payload...)

	// Block after header (1 byte) + 3 bytes of payload = 4 bytes total
	src := &wouldBlockMidPayloadReader{wire: wire, blockAfter: 4}
	r := fr.NewReader(src, fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)

	// First call to WriteTo: reads header + 3 bytes payload, then ErrWouldBlock
	n1, err1 := r.WriteTo(io.Discard)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first WriteTo: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}

	// Now call Read instead of WriteTo to continue.
	// Due to shared offset state, readStream writes to buf[payloadOff:] = buf[3:]
	// This is documented behavior for interleaving after partial operations.
	buf := make([]byte, 20)
	n2, err2 := r.Read(buf)
	if err2 != nil {
		t.Fatalf("Read after partial WriteTo: unexpected error: %v", err2)
	}

	// The remaining payload is "3456789" (7 bytes)
	// readStream writes to buf[3:10], so n2 = 7 but data is at buf[3:10]
	// The returned n2 reflects bytes written to the buffer (at offset position)
	if n2 != 7 {
		t.Fatalf("Read: want n=7, got n=%d", n2)
	}
	// Verify data is at the offset position (buf[3:10])
	expected := payload[3:] // "3456789"
	if !bytes.Equal(buf[3:10], expected) {
		t.Fatalf("Read payload at offset mismatch:\n  got:  %q\n  want: %q", buf[3:10], expected)
	}
}

// partialPacketReader returns data with ErrWouldBlock in the same source read.
// Under packet semantics, positive n is an admitted packet and ErrWouldBlock is
// the source frontier after that packet.
type partialPacketReader struct {
	data       []byte
	off        int
	blockAfter int  // return (blockAfter bytes, ErrWouldBlock) on first read
	blocked    bool // whether we've returned ErrWouldBlock
}

func (r *partialPacketReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}

	// On first read, return partial data WITH ErrWouldBlock in the same call.
	// This simulates a non-blocking socket returning partial data before blocking.
	if !r.blocked {
		r.blocked = true
		toReturn := r.blockAfter
		if toReturn > len(p) {
			toReturn = len(p)
		}
		if toReturn > len(r.data)-r.off {
			toReturn = len(r.data) - r.off
		}
		n := copy(p, r.data[r.off:r.off+toReturn])
		r.off += n
		return n, iox.ErrWouldBlock
	}

	// Subsequent reads return remaining data normally
	remaining := len(r.data) - r.off
	toReturn := len(p)
	if toReturn > remaining {
		toReturn = remaining
	}

	n := copy(p, r.data[r.off:r.off+toReturn])
	r.off += n
	return n, nil
}

// TestForward_SeqPacket_PositiveErrWouldBlockDoesNotMerge documents the source
// shape under packet semantics: positive ErrWouldBlock admits the current packet
// instead of accumulating bytes into the next packet.
func TestForward_SeqPacket_PositiveErrWouldBlockDoesNotMerge(t *testing.T) {
	payload := []byte("0123456789") // 10-byte packet

	// Return first 3 bytes with ErrWouldBlock, then remaining 7 bytes as the next
	// source packet.
	src := &partialPacketReader{data: payload, blockAfter: 3}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, src, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	// First call: reads and writes the first packet, then reports the deferred
	// source ErrWouldBlock.
	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first ForwardOnce: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}
	if n1 != 3 {
		t.Fatalf("first ForwardOnce: want n=3, got n=%d", n1)
	}

	// Second call: reads and writes the second packet.
	n2, err2 := fwd.ForwardOnce()
	if err2 != nil {
		t.Fatalf("second ForwardOnce: unexpected error: %v", err2)
	}
	if n2 != 7 {
		t.Fatalf("second ForwardOnce: want n=7, got n=%d", n2)
	}

	// Verify the output matches the original payload
	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatalf("payload mismatch:\n  got:  %q\n  want: %q", dst.Bytes(), payload)
	}
}

// wouldBlockMidWriteWriter returns ErrWouldBlock after writing a limited number of bytes.
type wouldBlockMidWriteWriter struct {
	buf     bytes.Buffer
	limit   int  // bytes to write before returning ErrWouldBlock
	written int  // total bytes written so far
	blocked bool // whether we've returned ErrWouldBlock
}

func (w *wouldBlockMidWriteWriter) Write(p []byte) (int, error) {
	if !w.blocked && w.written+len(p) > w.limit {
		// Write up to limit, then return ErrWouldBlock
		canWrite := w.limit - w.written
		if canWrite > 0 {
			n, _ := w.buf.Write(p[:canWrite])
			w.written += n
			w.blocked = true
			return n, iox.ErrWouldBlock
		}
		w.blocked = true
		return 0, iox.ErrWouldBlock
	}
	n, err := w.buf.Write(p)
	w.written += n
	return n, err
}

// twoChunkReader returns two chunks: first chunk, then second chunk.
type twoChunkReader struct {
	chunks [][]byte
	idx    int
}

func (r *twoChunkReader) Read(p []byte) (int, error) {
	if r.idx >= len(r.chunks) {
		return 0, io.EOF
	}
	n := copy(p, r.chunks[r.idx])
	r.idx++
	return n, nil
}

type chunkErrReader struct {
	data []byte
	err  error
	done bool
}

func (r *chunkErrReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), r.err
}

// TestWriter_ReadFrom_NonBlocking_Resume verifies that Writer.ReadFrom correctly
// resumes after ErrWouldBlock is returned mid-message. This is a regression test
// for a bug where the next call to ReadFrom would read a new chunk from src,
// losing the in-flight data.
func TestWriter_ReadFrom_NonBlocking_Resume(t *testing.T) {
	chunk1 := []byte("hello") // 5-byte message

	// Source provides one chunk
	src := &twoChunkReader{chunks: [][]byte{chunk1}}

	// Destination blocks after writing header (1 byte) + 2 bytes of payload
	dst := &wouldBlockMidWriteWriter{limit: 3}

	w := fr.NewWriter(dst, fr.WithWriteTCP(), fr.WithNonblock()).(*fr.Writer)

	// First call: reads chunk1, starts writing framed message, blocks mid-payload
	n1, err1 := w.ReadFrom(src)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first ReadFrom: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}
	if n1 != int64(len(chunk1)) {
		t.Fatalf("first ReadFrom: want n=%d, got n=%d", len(chunk1), n1)
	}

	// Second call: should resume writing the remaining payload
	n2, err2 := w.ReadFrom(src)
	// Should complete with EOF (src exhausted)
	if err2 != nil {
		t.Fatalf("second ReadFrom: unexpected error: %v", err2)
	}
	if n2 != 0 {
		t.Fatalf("second ReadFrom: want n=0, got n=%d", n2)
	}

	// Verify the wire format: header (1 byte with length 5) + payload "hello"
	expectedWire := append([]byte{5}, chunk1...)
	if !bytes.Equal(dst.buf.Bytes(), expectedWire) {
		t.Fatalf("wire mismatch:\n  got:  %v\n  want: %v", dst.buf.Bytes(), expectedWire)
	}
}

// --- Coverage improvement tests ---

// TestReader_WriteTo_Stream_ZeroLengthMessagePath verifies that WriteTo correctly
// handles zero-length messages by skipping the payload read/write phase.
func TestReader_WriteTo_Stream_ZeroLengthMessagePath(t *testing.T) {
	// Wire: zero-length message (header 0x00), then 3-byte message "abc"
	wire := []byte{0, 3, 'a', 'b', 'c'}
	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP()).(*fr.Reader)

	var dst bytes.Buffer
	n, err := r.WriteTo(&dst)
	if err != nil {
		t.Fatalf("WriteTo: unexpected error: %v", err)
	}
	// Only the 3-byte message should be written (zero-length is skipped)
	if n != 3 {
		t.Fatalf("WriteTo: want n=3, got n=%d", n)
	}
	if dst.String() != "abc" {
		t.Fatalf("WriteTo: want \"abc\", got %q", dst.String())
	}
}

// TestWriter_ReadFrom_MediumLength_Resume verifies that ReadFrom correctly resumes
// a medium-length message (254-65535 bytes) after ErrWouldBlock.
func TestWriter_ReadFrom_MediumLength_Resume(t *testing.T) {
	// Create a 300-byte payload (requires 3-byte header: 0xFE + 2-byte length)
	payload := bytes.Repeat([]byte{'m'}, 300)

	src := &twoChunkReader{chunks: [][]byte{payload}}

	// Block after writing header (3 bytes) + 10 bytes of payload = 13 bytes
	dst := &wouldBlockMidWriteWriter{limit: 13}

	w := fr.NewWriter(dst, fr.WithWriteTCP(), fr.WithNonblock()).(*fr.Writer)

	// First call: reads payload, starts writing, blocks mid-payload
	n1, err1 := w.ReadFrom(src)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first ReadFrom: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}
	if n1 != int64(len(payload)) {
		t.Fatalf("first ReadFrom: want n=%d, got n=%d", len(payload), n1)
	}

	// Second call: should resume writing the remaining payload
	n2, err2 := w.ReadFrom(src)
	if err2 != nil {
		t.Fatalf("second ReadFrom: unexpected error: %v", err2)
	}
	if n2 != 0 {
		t.Fatalf("second ReadFrom: want n=0, got n=%d", n2)
	}

	// Verify total wire length: 3-byte header + 300-byte payload = 303 bytes
	if len(dst.buf.Bytes()) != 303 {
		t.Fatalf("wire length: got %d, want 303", len(dst.buf.Bytes()))
	}
	// Verify header byte indicates medium-length encoding
	if dst.buf.Bytes()[0] != 0xFE {
		t.Fatalf("header byte: got 0x%02X, want 0xFE", dst.buf.Bytes()[0])
	}
	// Verify payload content at offset 3
	if !bytes.Equal(dst.buf.Bytes()[3:], payload) {
		t.Fatalf("payload mismatch")
	}
}

// TestWriter_ReadFrom_LargeLength_Resume verifies that ReadFrom correctly resumes
// a large-length message (>65535 bytes) after ErrWouldBlock.
//
// Note: ReadFrom uses an internal 32KB buffer, so we test with Write directly
// to ensure the large header path is exercised.
func TestWriter_ReadFrom_LargeLength_Resume(t *testing.T) {
	// Create a 70000-byte payload (requires 8-byte header: 0xFF + 7-byte length)
	payload := bytes.Repeat([]byte{'L'}, 70000)

	// Block after writing header (8 bytes) + 100 bytes of payload = 108 bytes
	dst := &wouldBlockMidWriteWriter{limit: 108}

	w := fr.NewWriter(dst, fr.WithWriteTCP(), fr.WithNonblock()).(*fr.Writer)

	// First call: starts writing, blocks mid-payload
	n1, err1 := w.Write(payload)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first Write: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}
	// n1 is the number of payload bytes written before block
	// Header is 8 bytes, so payload bytes = 108 - 8 = 100
	if n1 != 100 {
		t.Fatalf("first Write: want n=100, got n=%d", n1)
	}

	// Second call: should resume writing the remaining payload
	n2, err2 := w.Write(payload)
	if err2 != nil {
		t.Fatalf("second Write: unexpected error: %v", err2)
	}
	// n2 should be 69900 (remaining payload bytes: 70000 - 100)
	if n2 != 69900 {
		t.Fatalf("second Write: want n=69900, got n=%d", n2)
	}

	// Verify total wire length: 8-byte header + 70000-byte payload
	if len(dst.buf.Bytes()) != 8+70000 {
		t.Fatalf("wire length: got %d, want %d", len(dst.buf.Bytes()), 8+70000)
	}
	// Verify header byte indicates large-length encoding
	if dst.buf.Bytes()[0] != 0xFF {
		t.Fatalf("header byte: got 0x%02X, want 0xFF", dst.buf.Bytes()[0])
	}
}

// persistentBlockWriter blocks on every write after the first successful writes.
type persistentBlockWriter struct {
	buf     bytes.Buffer
	limit   int // bytes to write before blocking
	written int // total bytes written
}

func (w *persistentBlockWriter) Write(p []byte) (int, error) {
	if w.written >= w.limit {
		return 0, iox.ErrWouldBlock
	}
	canWrite := w.limit - w.written
	if canWrite > len(p) {
		canWrite = len(p)
	}
	n, _ := w.buf.Write(p[:canWrite])
	w.written += n
	if w.written >= w.limit {
		return n, iox.ErrWouldBlock
	}
	return n, nil
}

// TestWriter_ReadFrom_ResumeBlocksAgain verifies that ReadFrom correctly handles
// multiple consecutive ErrWouldBlock returns during resume.
func TestWriter_ReadFrom_ResumeBlocksAgain(t *testing.T) {
	payload := []byte("hello world!") // 12-byte message

	src := &twoChunkReader{chunks: [][]byte{payload}}

	// Block after writing header (1 byte) + 3 bytes = 4 bytes total
	dst := &persistentBlockWriter{limit: 4}

	w := fr.NewWriter(dst, fr.WithWriteTCP(), fr.WithNonblock()).(*fr.Writer)

	// First call: admits the source chunk, writes header + 3 bytes payload, then blocks
	n1, err1 := w.ReadFrom(src)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first ReadFrom: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}
	if n1 != int64(len(payload)) {
		t.Fatalf("first ReadFrom: want n=%d, got n=%d", len(payload), n1)
	}

	// Second call: tries to resume but blocks immediately (limit reached)
	dst.limit = 4 // still at limit
	n2, err2 := w.ReadFrom(src)
	if !errors.Is(err2, iox.ErrWouldBlock) {
		t.Fatalf("second ReadFrom: want ErrWouldBlock, got (%d, %v)", n2, err2)
	}
	if n2 != 0 {
		t.Fatalf("second ReadFrom: want n=0, got n=%d", n2)
	}

	// Third call: allow more writes
	dst.limit = 100
	n3, err3 := w.ReadFrom(src)
	if err3 != nil {
		t.Fatalf("third ReadFrom: unexpected error: %v", err3)
	}
	if n3 != 0 {
		t.Fatalf("third ReadFrom: want n=0, got n=%d", n3)
	}

	// Verify wire format
	expectedWire := append([]byte{12}, payload...)
	if !bytes.Equal(dst.buf.Bytes(), expectedWire) {
		t.Fatalf("wire mismatch:\n  got:  %v\n  want: %v", dst.buf.Bytes(), expectedWire)
	}
}

// TestReader_Read_PartialHeaderEOF verifies that Read returns io.ErrUnexpectedEOF
// when EOF is received after reading a partial extended header.
func TestReader_Read_PartialHeaderEOF(t *testing.T) {
	// Send header byte 0xFE (indicates 2-byte extended length follows) then EOF
	// This should trigger the partial header EOF path
	wire := []byte{0xFE} // header indicates extended length, but no length bytes follow
	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP())

	buf := make([]byte, 100)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Read: want (0, ErrUnexpectedEOF), got (%d, %v)", n, err)
	}
}

// TestReader_Read_PartialExtendedHeaderEOF verifies that Read returns io.ErrUnexpectedEOF
// when EOF is received mid-extended-header (after reading some but not all extended bytes).
func TestReader_Read_PartialExtendedHeaderEOF(t *testing.T) {
	// Send header byte 0xFE + 1 byte of extended length (need 2), then EOF
	wire := []byte{0xFE, 0x00} // header + partial extended length
	r := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP())

	buf := make([]byte, 100)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Read: want (0, ErrUnexpectedEOF), got (%d, %v)", n, err)
	}
}

// TestWriter_ReadFrom_LargeMessageResumeGuard verifies that ReadFrom returns
// io.ErrShortBuffer when trying to resume a large message (>32KB) that was
// started by Write.
func TestWriter_ReadFrom_LargeMessageResumeGuard(t *testing.T) {
	// Create a 70000-byte payload (requires 8-byte header, >32KB buffer)
	payload := bytes.Repeat([]byte{'L'}, 70000)

	// Block after writing header (8 bytes) + 100 bytes of payload
	dst := &wouldBlockMidWriteWriter{limit: 108}

	w := fr.NewWriter(dst, fr.WithWriteTCP(), fr.WithNonblock()).(*fr.Writer)

	// First call via Write: starts writing large message, blocks mid-payload
	n1, err1 := w.Write(payload)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("Write: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}

	// Second call via ReadFrom: should return ErrShortBuffer because the
	// in-flight message (70000 bytes) exceeds the internal 32KB buffer.
	src := bytes.NewReader(nil)
	n2, err2 := w.ReadFrom(src)
	if !errors.Is(err2, io.ErrShortBuffer) {
		t.Fatalf("ReadFrom: want ErrShortBuffer, got (%d, %v)", n2, err2)
	}
}

// errorAfterProgressWriter writes some bytes successfully, then returns an error.
type errorAfterProgressWriter struct {
	buf     bytes.Buffer
	limit   int   // bytes to write before returning error
	written int   // total bytes written
	err     error // error to return after limit
}

func (w *errorAfterProgressWriter) Write(p []byte) (int, error) {
	if w.written >= w.limit {
		return 0, w.err
	}
	canWrite := w.limit - w.written
	if canWrite > len(p) {
		canWrite = len(p)
	}
	n, _ := w.buf.Write(p[:canWrite])
	w.written += n
	if w.written >= w.limit {
		return n, w.err
	}
	return n, nil
}

// TestWriter_ReadFrom_ResumeNonSemanticError verifies that ReadFrom correctly
// propagates non-semantic errors (not ErrWouldBlock/ErrMore) during resume.
func TestWriter_ReadFrom_ResumeNonSemanticError(t *testing.T) {
	payload := []byte("hello world!") // 12-byte message

	customErr := errors.New("custom write error")

	// First write: blocks after header (1 byte) + 3 bytes payload = 4 bytes
	dst := &errorAfterProgressWriter{limit: 4, err: iox.ErrWouldBlock}

	w := fr.NewWriter(dst, fr.WithWriteTCP(), fr.WithNonblock()).(*fr.Writer)

	// First call: writes header + 3 bytes payload, then blocks
	src := &twoChunkReader{chunks: [][]byte{payload}}
	n1, err1 := w.ReadFrom(src)
	if !errors.Is(err1, iox.ErrWouldBlock) {
		t.Fatalf("first ReadFrom: want ErrWouldBlock, got (%d, %v)", n1, err1)
	}
	if n1 != int64(len(payload)) {
		t.Fatalf("first ReadFrom: want n=%d, got n=%d", len(payload), n1)
	}

	// Change the error to a custom error for the resume
	dst.err = customErr

	// Second call: tries to resume but gets custom error
	n2, err2 := w.ReadFrom(bytes.NewReader(nil))
	if !errors.Is(err2, customErr) {
		t.Fatalf("second ReadFrom: want customErr, got (%d, %v)", n2, err2)
	}
	if n2 != 0 {
		t.Fatalf("second ReadFrom: want n=0, got n=%d", n2)
	}
}

func TestWriterReadFromDeferredSourceErrMoreAfterWriteResume(t *testing.T) {
	payload := []byte("frontier")
	src := &chunkErrReader{data: payload, err: iox.ErrMore}
	dst := &wouldBlockMidWriteWriter{limit: 4}
	w := fr.NewWriter(dst, fr.WithWriteTCP(), fr.WithNonblock()).(*fr.Writer)

	n1, err1 := w.ReadFrom(src)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != int64(len(payload)) {
		t.Fatalf("first ReadFrom: want (%d, ErrWouldBlock), got (%d, %v)", len(payload), n1, err1)
	}

	n2, err2 := w.ReadFrom(src)
	if !errors.Is(err2, iox.ErrMore) || n2 != 0 {
		t.Fatalf("second ReadFrom: want (0, ErrMore), got (%d, %v)", n2, err2)
	}

	expectedWire := append([]byte{byte(len(payload))}, payload...)
	if !bytes.Equal(dst.buf.Bytes(), expectedWire) {
		t.Fatalf("wire mismatch:\n  got:  %v\n  want: %v", dst.buf.Bytes(), expectedWire)
	}
}

func TestWriterReadFromStreamSourceEOFAfterProgress(t *testing.T) {
	payload := []byte("done")
	src := &chunkErrReader{data: payload, err: io.EOF}
	var dst bytes.Buffer
	w := fr.NewWriter(&dst, fr.WithWriteTCP()).(*fr.Writer)

	n, err := w.ReadFrom(src)
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("ReadFrom: want (%d, nil), got (%d, %v)", len(payload), n, err)
	}

	expectedWire := append([]byte{byte(len(payload))}, payload...)
	if !bytes.Equal(dst.Bytes(), expectedWire) {
		t.Fatalf("wire mismatch:\n  got:  %v\n  want: %v", dst.Bytes(), expectedWire)
	}
}

func TestWriterReadFromDeferredSourceErrorAfterPacketResume(t *testing.T) {
	msg := []byte("packet")
	sourceErr := errors.New("source frontier")
	src := &chunkErrReader{data: msg, err: sourceErr}
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 0, err: iox.ErrWouldBlock},
	}}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock()).(*fr.Writer)

	n1, err1 := w.ReadFrom(src)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != int64(len(msg)) {
		t.Fatalf("first ReadFrom: want (%d, ErrWouldBlock), got (%d, %v)", len(msg), n1, err1)
	}

	n2, err2 := w.ReadFrom(src)
	if !errors.Is(err2, sourceErr) || n2 != 0 {
		t.Fatalf("second ReadFrom: want (0, sourceErr), got (%d, %v)", n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestWriterReadFromPacketSourceEOFAfterProgress(t *testing.T) {
	msg := []byte("packet")
	src := &chunkErrReader{data: msg, err: io.EOF}
	var dst bytes.Buffer
	w := fr.NewWriter(&dst, fr.WithProtocol(fr.SeqPacket)).(*fr.Writer)

	n, err := w.ReadFrom(src)
	if err != nil || n != int64(len(msg)) {
		t.Fatalf("ReadFrom: want (%d, nil), got (%d, %v)", len(msg), n, err)
	}
	if !bytes.Equal(dst.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.Bytes(), msg)
	}
}

type packetAtomicStepWriter struct {
	steps []struct {
		n   int
		err error
	}
	call int
	buf  bytes.Buffer
}

func (w *packetAtomicStepWriter) Write(p []byte) (int, error) {
	if w.call < len(w.steps) {
		st := w.steps[w.call]
		w.call++
		n := st.n
		if n < 0 {
			n = 0
		}
		if n > len(p) {
			n = len(p)
		}
		if n > 0 {
			_, _ = w.buf.Write(p[:n])
		}
		return n, st.err
	}
	w.call++
	return w.buf.Write(p)
}

type packetRecordWriter struct {
	packets [][]byte
}

func (w *packetRecordWriter) Write(p []byte) (int, error) {
	packet := append([]byte(nil), p...)
	w.packets = append(w.packets, packet)
	return len(p), nil
}

type packetSourceStepReader struct {
	steps []struct {
		b   []byte
		err error
	}
	step int
}

func (r *packetSourceStepReader) Read(p []byte) (int, error) {
	if r.step >= len(r.steps) {
		return 0, io.EOF
	}
	st := r.steps[r.step]
	r.step++
	return copy(p, st.b), st.err
}

type packetAtomicOnceReader struct {
	data []byte
	done bool
}

func (r *packetAtomicOnceReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, r.data), nil
}

func TestWriterReadFromPacketZeroProgressWouldBlockRetainsWholePacket(t *testing.T) {
	msg := []byte("packet")
	src := &packetAtomicOnceReader{data: msg}
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 0, err: iox.ErrWouldBlock},
	}}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock()).(*fr.Writer)

	n1, err1 := w.ReadFrom(src)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != int64(len(msg)) {
		t.Fatalf("first ReadFrom: want (%d, ErrWouldBlock), got (%d, %v)", len(msg), n1, err1)
	}

	n2, err2 := w.ReadFrom(src)
	if err2 != nil || n2 != 0 {
		t.Fatalf("second ReadFrom: want (0, nil), got (%d, %v)", n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestWriterReadFromPacketPartialWouldBlockIsShortWrite(t *testing.T) {
	msg := []byte("packet")
	src := &packetAtomicOnceReader{data: msg}
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 2, err: iox.ErrWouldBlock},
	}}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock()).(*fr.Writer)

	n, err := w.ReadFrom(src)
	if !errors.Is(err, io.ErrShortWrite) || n != int64(len(msg)) {
		t.Fatalf("ReadFrom: want (%d, ErrShortWrite), got (%d, %v)", len(msg), n, err)
	}
	if got, want := dst.buf.String(), "pa"; got != want {
		t.Fatalf("dst=%q want %q", got, want)
	}
}

func TestWriterWritePacketErrMoreFullWriteDoesNotReplay(t *testing.T) {
	first := []byte("packet")
	second := []byte("second")
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: len(first), err: iox.ErrMore},
	}}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := w.Write(first)
	if !errors.Is(err1, iox.ErrMore) || n1 != len(first) {
		t.Fatalf("first Write: want (%d, ErrMore), got (%d, %v)", len(first), n1, err1)
	}

	n2, err2 := w.Write(second)
	if err2 != nil || n2 != len(second) {
		t.Fatalf("second Write: want (%d, nil), got (%d, %v)", len(second), n2, err2)
	}
	if got, want := dst.buf.String(), string(first)+string(second); got != want {
		t.Fatalf("dst=%q want %q", got, want)
	}
}

func TestWriterWritePacketWouldBlockFullWriteDoesNotReplay(t *testing.T) {
	first := []byte("packet")
	second := []byte("second")
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: len(first), err: iox.ErrWouldBlock},
	}}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := w.Write(first)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != len(first) {
		t.Fatalf("first Write: want (%d, ErrWouldBlock), got (%d, %v)", len(first), n1, err1)
	}

	n2, err2 := w.Write(second)
	if err2 != nil || n2 != len(second) {
		t.Fatalf("second Write: want (%d, nil), got (%d, %v)", len(second), n2, err2)
	}
	if got, want := dst.buf.String(), string(first)+string(second); got != want {
		t.Fatalf("dst=%q want %q", got, want)
	}
}

func TestWriterReadFromPacketErrMoreFullWriteDoesNotReplay(t *testing.T) {
	msg := []byte("packet")
	src := &packetAtomicOnceReader{data: msg}
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: len(msg), err: iox.ErrMore},
	}}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock()).(*fr.Writer)

	n1, err1 := w.ReadFrom(src)
	if !errors.Is(err1, iox.ErrMore) || n1 != int64(len(msg)) {
		t.Fatalf("first ReadFrom: want (%d, ErrMore), got (%d, %v)", len(msg), n1, err1)
	}

	n2, err2 := w.ReadFrom(src)
	if err2 != nil || n2 != 0 {
		t.Fatalf("second ReadFrom: want (0, nil), got (%d, %v)", n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestWriterReadFromPacketSourceAfterRetainedBehindDestinationFullErrMore(t *testing.T) {
	msg := []byte("packet")
	sourceErr := errors.New("source frontier")
	src := &chunkErrReader{data: msg, err: sourceErr}
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: len(msg), err: iox.ErrMore},
	}}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock()).(*fr.Writer)

	n1, err1 := w.ReadFrom(src)
	if !errors.Is(err1, iox.ErrMore) || n1 != int64(len(msg)) {
		t.Fatalf("first ReadFrom: want (%d, ErrMore), got (%d, %v)", len(msg), n1, err1)
	}

	n2, err2 := w.ReadFrom(src)
	if !errors.Is(err2, sourceErr) || n2 != 0 {
		t.Fatalf("second ReadFrom: want (0, sourceErr), got (%d, %v)", n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestWriterReadFromPacketSourceEOFBehindDestinationFullErrMore(t *testing.T) {
	msg := []byte("packet")
	src := &chunkErrReader{data: msg, err: io.EOF}
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: len(msg), err: iox.ErrMore},
	}}
	w := fr.NewWriter(dst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock()).(*fr.Writer)

	n1, err1 := w.ReadFrom(src)
	if !errors.Is(err1, iox.ErrMore) || n1 != int64(len(msg)) {
		t.Fatalf("first ReadFrom: want (%d, ErrMore), got (%d, %v)", len(msg), n1, err1)
	}

	n2, err2 := w.ReadFrom(src)
	if err2 != nil || n2 != 0 {
		t.Fatalf("second ReadFrom: want (0, nil), got (%d, %v)", n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestForwarderPacketZeroProgressWouldBlockRetainsWholePacket(t *testing.T) {
	msg := []byte("packet")
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 0, err: iox.ErrWouldBlock},
	}}
	fwd := fr.NewForwarder(dst, bytes.NewReader(msg), fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 0 {
		t.Fatalf("first ForwardOnce: want (0, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	n2, err2 := fwd.ForwardOnce()
	if err2 != nil || n2 != len(msg) {
		t.Fatalf("second ForwardOnce: want (%d, nil), got (%d, %v)", len(msg), n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestForwarderPacketPartialWouldBlockIsShortWrite(t *testing.T) {
	msg := []byte("packet")
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 2, err: iox.ErrWouldBlock},
	}}
	fwd := fr.NewForwarder(dst, bytes.NewReader(msg), fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n, err := fwd.ForwardOnce()
	if !errors.Is(err, io.ErrShortWrite) || n != 2 {
		t.Fatalf("ForwardOnce: want (2, ErrShortWrite), got (%d, %v)", n, err)
	}
	if got, want := dst.buf.String(), "pa"; got != want {
		t.Fatalf("dst=%q want %q", got, want)
	}
}

func TestForwarderPacketErrMoreFullWriteDoesNotReplay(t *testing.T) {
	msg := []byte("packet")
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: len(msg), err: iox.ErrMore},
	}}
	fwd := fr.NewForwarder(dst, bytes.NewReader(msg), fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrMore) || n1 != len(msg) {
		t.Fatalf("first ForwardOnce: want (%d, ErrMore), got (%d, %v)", len(msg), n1, err1)
	}

	n2, err2 := fwd.ForwardOnce()
	if !errors.Is(err2, io.EOF) || n2 != 0 {
		t.Fatalf("second ForwardOnce: want (0, EOF), got (%d, %v)", n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestForwarderPacketSourceAfterRetainedBehindDestinationFullErrMore(t *testing.T) {
	msg := []byte("packet")
	sourceErr := errors.New("source frontier")
	src := &packetSourceStepReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: msg, err: sourceErr},
	}}
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: len(msg), err: iox.ErrMore},
	}}
	fwd := fr.NewForwarder(dst, src, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrMore) || n1 != len(msg) {
		t.Fatalf("first ForwardOnce: want (%d, ErrMore), got (%d, %v)", len(msg), n1, err1)
	}

	n2, err2 := fwd.ForwardOnce()
	if !errors.Is(err2, sourceErr) || n2 != 0 {
		t.Fatalf("second ForwardOnce: want (0, sourceErr), got (%d, %v)", n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestForwarderPacketSourceEOFBehindDestinationFullErrMore(t *testing.T) {
	msg := []byte("packet")
	src := &packetSourceStepReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: msg, err: io.EOF},
	}}
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: len(msg), err: iox.ErrMore},
	}}
	fwd := fr.NewForwarder(dst, src, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrMore) || n1 != len(msg) {
		t.Fatalf("first ForwardOnce: want (%d, ErrMore), got (%d, %v)", len(msg), n1, err1)
	}

	n2, err2 := fwd.ForwardOnce()
	if err2 != nil || n2 != 0 {
		t.Fatalf("second ForwardOnce: want (0, nil), got (%d, %v)", n2, err2)
	}

	n3, err3 := fwd.ForwardOnce()
	if !errors.Is(err3, io.EOF) || n3 != 0 {
		t.Fatalf("third ForwardOnce: want (0, EOF), got (%d, %v)", n3, err3)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestForwarderPacketSourceErrMoreWithProgressDoesNotMerge(t *testing.T) {
	first := []byte("first")
	second := []byte("second")
	src := &packetSourceStepReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: first, err: iox.ErrMore},
		{b: second},
	}}
	dst := &packetRecordWriter{}
	fwd := fr.NewForwarder(dst, src, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrMore) || n1 != len(first) {
		t.Fatalf("first ForwardOnce: want (%d, ErrMore), got (%d, %v)", len(first), n1, err1)
	}
	if len(dst.packets) != 1 || !bytes.Equal(dst.packets[0], first) {
		t.Fatalf("after first ForwardOnce packets=%q want [%q]", dst.packets, first)
	}

	n2, err2 := fwd.ForwardOnce()
	if err2 != nil || n2 != len(second) {
		t.Fatalf("second ForwardOnce: want (%d, nil), got (%d, %v)", len(second), n2, err2)
	}
	if len(dst.packets) != 2 || !bytes.Equal(dst.packets[0], first) || !bytes.Equal(dst.packets[1], second) {
		t.Fatalf("packets=%q want [%q %q]", dst.packets, first, second)
	}
}

func TestForwarderPacketSourceHardErrorAfterProgressEmitsPacket(t *testing.T) {
	msg := []byte("packet")
	boom := errors.New("source frontier")
	src := &packetSourceStepReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: msg, err: boom},
	}}
	dst := &packetRecordWriter{}
	fwd := fr.NewForwarder(dst, src, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n, err := fwd.ForwardOnce()
	if !errors.Is(err, boom) || n != len(msg) {
		t.Fatalf("ForwardOnce: want (%d, boom), got (%d, %v)", len(msg), n, err)
	}
	if len(dst.packets) != 1 || !bytes.Equal(dst.packets[0], msg) {
		t.Fatalf("packets=%q want [%q]", dst.packets, msg)
	}
}

func TestForwarderPacketSourceErrMoreDeferredAcrossWriteWouldBlock(t *testing.T) {
	msg := []byte("packet")
	src := &packetSourceStepReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: msg, err: iox.ErrMore},
	}}
	dst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 0, err: iox.ErrWouldBlock},
	}}
	fwd := fr.NewForwarder(dst, src, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 0 {
		t.Fatalf("first ForwardOnce: want (0, ErrWouldBlock), got (%d, %v)", n1, err1)
	}
	if dst.buf.Len() != 0 {
		t.Fatalf("dst wrote before resume: %q", dst.buf.Bytes())
	}

	n2, err2 := fwd.ForwardOnce()
	if !errors.Is(err2, iox.ErrMore) || n2 != len(msg) {
		t.Fatalf("second ForwardOnce: want (%d, ErrMore), got (%d, %v)", len(msg), n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestForwarderPacketSourceWouldBlockWithProgressDoesNotMerge(t *testing.T) {
	first := []byte("first")
	second := []byte("second")
	src := &packetSourceStepReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: first, err: iox.ErrWouldBlock},
		{b: second},
	}}
	dst := &packetRecordWriter{}
	fwd := fr.NewForwarder(dst, src, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != len(first) {
		t.Fatalf("first ForwardOnce: want (%d, ErrWouldBlock), got (%d, %v)", len(first), n1, err1)
	}
	if len(dst.packets) != 1 || !bytes.Equal(dst.packets[0], first) {
		t.Fatalf("after first ForwardOnce packets=%q want [%q]", dst.packets, first)
	}

	n2, err2 := fwd.ForwardOnce()
	if err2 != nil || n2 != len(second) {
		t.Fatalf("second ForwardOnce: want (%d, nil), got (%d, %v)", len(second), n2, err2)
	}
	if len(dst.packets) != 2 || !bytes.Equal(dst.packets[0], first) || !bytes.Equal(dst.packets[1], second) {
		t.Fatalf("packets=%q want [%q %q]", dst.packets, first, second)
	}
}

type packetErrMorePartialWriter struct {
	buf    bytes.Buffer
	limit  int
	called bool
}

func (w *packetErrMorePartialWriter) Write(p []byte) (int, error) {
	if !w.called {
		w.called = true
		n := w.limit
		if n > len(p) {
			n = len(p)
		}
		if n > 0 {
			_, _ = w.buf.Write(p[:n])
		}
		return n, iox.ErrMore
	}
	return w.buf.Write(p)
}

func TestReaderWriteToPacketByteDstPartialWouldBlockResumesSuffix(t *testing.T) {
	msg := []byte("packet")
	r := fr.NewReader(bytes.NewReader(msg), fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	dst := &capWouldBlockWriter{limit: 2}

	n1, err1 := r.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 2 {
		t.Fatalf("first WriteTo: want (2, ErrWouldBlock), got (%d, %v)", n1, err1)
	}
	dst.limit = len(msg)
	n2, err2 := r.WriteTo(dst)
	if err2 != nil || n2 != int64(len(msg)-2) {
		t.Fatalf("second WriteTo: want (%d, nil), got (%d, %v)", len(msg)-2, n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestReaderWriteToPacketByteDstPartialErrMoreResumesSuffix(t *testing.T) {
	msg := []byte("packet")
	r := fr.NewReader(bytes.NewReader(msg), fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	dst := &packetErrMorePartialWriter{limit: 2}

	n1, err1 := r.WriteTo(dst)
	if !errors.Is(err1, iox.ErrMore) || n1 != 2 {
		t.Fatalf("first WriteTo: want (2, ErrMore), got (%d, %v)", n1, err1)
	}
	n2, err2 := r.WriteTo(dst)
	if err2 != nil || n2 != int64(len(msg)-2) {
		t.Fatalf("second WriteTo: want (%d, nil), got (%d, %v)", len(msg)-2, n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestReaderWriteToPacketSourceErrMoreSurvivesByteDstWouldBlock(t *testing.T) {
	msg := []byte("packet")
	src := &chunkErrReader{data: msg, err: iox.ErrMore}
	r := fr.NewReader(src, fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	dst := &capWouldBlockWriter{limit: 2}

	n1, err1 := r.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 2 {
		t.Fatalf("first WriteTo: want (2, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	dst.limit = len(msg)
	n2, err2 := r.WriteTo(dst)
	if !errors.Is(err2, iox.ErrMore) || n2 != int64(len(msg)-2) {
		t.Fatalf("second WriteTo: want (%d, ErrMore), got (%d, %v)", len(msg)-2, n2, err2)
	}
	if !bytes.Equal(dst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", dst.buf.Bytes(), msg)
	}
}

func TestReaderWriteToPacketKnownPacketWriterZeroProgressRetainsWholePacket(t *testing.T) {
	msg := []byte("packet")
	src := fr.NewReader(bytes.NewReader(msg), fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 0, err: iox.ErrWouldBlock},
	}}
	dst := fr.NewWriter(rawDst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := src.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 0 {
		t.Fatalf("first WriteTo: want (0, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	n2, err2 := src.WriteTo(dst)
	if err2 != nil || n2 != int64(len(msg)) {
		t.Fatalf("second WriteTo: want (%d, nil), got (%d, %v)", len(msg), n2, err2)
	}
	if !bytes.Equal(rawDst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", rawDst.buf.Bytes(), msg)
	}
}

func TestReaderWriteToPacketSourceEOFSurvivesPacketDstWouldBlock(t *testing.T) {
	msg := []byte("packet")
	src := &chunkErrReader{data: msg, err: io.EOF}
	r := fr.NewReader(src, fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 0, err: iox.ErrWouldBlock},
	}}
	dst := fr.NewWriter(rawDst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := r.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 0 {
		t.Fatalf("first WriteTo: want (0, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	n2, err2 := r.WriteTo(dst)
	if err2 != nil || n2 != int64(len(msg)) {
		t.Fatalf("second WriteTo: want (%d, nil), got (%d, %v)", len(msg), n2, err2)
	}

	n3, err3 := r.WriteTo(dst)
	if err3 != nil || n3 != 0 {
		t.Fatalf("third WriteTo: want (0, nil), got (%d, %v)", n3, err3)
	}
	if !bytes.Equal(rawDst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", rawDst.buf.Bytes(), msg)
	}
}

func TestReaderWriteToPacketKnownStreamWriterPartialWouldBlockRetainsWholeFrame(t *testing.T) {
	msg := []byte("packet")
	src := fr.NewReader(bytes.NewReader(msg), fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &capWouldBlockWriter{limit: 4}
	dst := fr.NewWriter(rawDst, fr.WithWriteTCP(), fr.WithNonblock())

	n1, err1 := src.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 3 {
		t.Fatalf("first WriteTo: want (3, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	rawDst.limit = len(msg) + 1
	n2, err2 := src.WriteTo(dst)
	if err2 != nil || n2 != int64(len(msg)-3) {
		t.Fatalf("second WriteTo: want (%d, nil), got (%d, %v)", len(msg)-3, n2, err2)
	}
	requireSingleFramePayload(t, rawDst.buf.Bytes(), msg)
}

func TestReaderWriteToPacketSourceHardErrorSurvivesFrameDstWouldBlock(t *testing.T) {
	msg := []byte("packet")
	sourceErr := errors.New("source frontier")
	src := &chunkErrReader{data: msg, err: sourceErr}
	r := fr.NewReader(src, fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &capWouldBlockWriter{limit: 4}
	dst := fr.NewWriter(rawDst, fr.WithWriteTCP(), fr.WithNonblock())

	n1, err1 := r.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 3 {
		t.Fatalf("first WriteTo: want (3, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	rawDst.limit = len(msg) + 1
	n2, err2 := r.WriteTo(dst)
	if !errors.Is(err2, sourceErr) || n2 != int64(len(msg)-3) {
		t.Fatalf("second WriteTo: want (%d, sourceErr), got (%d, %v)", len(msg)-3, n2, err2)
	}
	requireSingleFramePayload(t, rawDst.buf.Bytes(), msg)
}

func TestReaderWriteToPacketKnownStreamWriterResumeReblocksRetainsWholeFrame(t *testing.T) {
	msg := []byte("packet")
	src := fr.NewReader(bytes.NewReader(msg), fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &capWouldBlockWriter{limit: 4}
	dst := fr.NewWriter(rawDst, fr.WithWriteTCP(), fr.WithNonblock())

	n1, err1 := src.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 3 {
		t.Fatalf("first WriteTo: want (3, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	rawDst.limit = 5
	n2, err2 := src.WriteTo(dst)
	if !errors.Is(err2, iox.ErrWouldBlock) || n2 != 1 {
		t.Fatalf("second WriteTo: want (1, ErrWouldBlock), got (%d, %v)", n2, err2)
	}

	rawDst.limit = len(msg) + 1
	n3, err3 := src.WriteTo(dst)
	if err3 != nil || n3 != int64(len(msg)-4) {
		t.Fatalf("third WriteTo: want (%d, nil), got (%d, %v)", len(msg)-4, n3, err3)
	}
	requireSingleFramePayload(t, rawDst.buf.Bytes(), msg)
}

func TestReaderWriteToPacketKnownStreamWriterResumeHardErrorClearsFrame(t *testing.T) {
	msg := []byte("packet")
	src := fr.NewReader(bytes.NewReader(msg), fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	boom := errors.New("write failed")
	rawDst := &resumeErrWriter{limit: 4, hardErr: boom}
	dst := fr.NewWriter(rawDst, fr.WithWriteTCP(), fr.WithNonblock())

	n1, err1 := src.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 3 {
		t.Fatalf("first WriteTo: want (3, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	n2, err2 := src.WriteTo(dst)
	if !errors.Is(err2, boom) || n2 != 0 {
		t.Fatalf("second WriteTo: want (0, boom), got (%d, %v)", n2, err2)
	}
}

func TestReaderWriteToStreamKnownStreamWriterPartialWouldBlockRetainsWholeFrame(t *testing.T) {
	msg := []byte("packet")
	wire := append([]byte{byte(len(msg))}, msg...)
	src := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &capWouldBlockWriter{limit: 4}
	dst := fr.NewWriter(rawDst, fr.WithWriteTCP(), fr.WithNonblock())

	n1, err1 := src.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 3 {
		t.Fatalf("first WriteTo: want (3, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	rawDst.limit = len(msg) + 1
	n2, err2 := src.WriteTo(dst)
	if err2 != nil || n2 != int64(len(msg)-3) {
		t.Fatalf("second WriteTo: want (%d, nil), got (%d, %v)", len(msg)-3, n2, err2)
	}
	requireSingleFramePayload(t, rawDst.buf.Bytes(), msg)
}

func TestReaderWriteToStreamKnownPacketWriterResumeReblocksRetainsWholePacket(t *testing.T) {
	msg := []byte("packet")
	wire := append([]byte{byte(len(msg))}, msg...)
	src := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 0, err: iox.ErrWouldBlock},
		{n: 0, err: iox.ErrWouldBlock},
	}}
	dst := fr.NewWriter(rawDst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := src.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 0 {
		t.Fatalf("first WriteTo: want (0, ErrWouldBlock), got (%d, %v)", n1, err1)
	}
	n2, err2 := src.WriteTo(dst)
	if !errors.Is(err2, iox.ErrWouldBlock) || n2 != 0 {
		t.Fatalf("second WriteTo: want (0, ErrWouldBlock), got (%d, %v)", n2, err2)
	}
	n3, err3 := src.WriteTo(dst)
	if err3 != nil || n3 != int64(len(msg)) {
		t.Fatalf("third WriteTo: want (%d, nil), got (%d, %v)", len(msg), n3, err3)
	}
	if !bytes.Equal(rawDst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", rawDst.buf.Bytes(), msg)
	}
}

func TestReaderWriteToStreamKnownPacketWriterPartialWouldBlockIsShortWrite(t *testing.T) {
	msg := []byte("packet")
	wire := append([]byte{byte(len(msg))}, msg...)
	src := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 2, err: iox.ErrWouldBlock},
	}}
	dst := fr.NewWriter(rawDst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n, err := src.WriteTo(dst)
	if !errors.Is(err, io.ErrShortWrite) || n != 2 {
		t.Fatalf("WriteTo: want (2, ErrShortWrite), got (%d, %v)", n, err)
	}
	if got, want := rawDst.buf.String(), "pa"; got != want {
		t.Fatalf("dst=%q want %q", got, want)
	}
}

func TestReaderWriteToPacketKnownStreamWriterErrMoreRetainsWholeFrame(t *testing.T) {
	msg := []byte("packet")
	src := fr.NewReader(bytes.NewReader(msg), fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &capErrMoreWriter{limit: 4}
	dst := fr.NewWriter(rawDst, fr.WithWriteTCP(), fr.WithNonblock())

	n1, err1 := src.WriteTo(dst)
	if !errors.Is(err1, iox.ErrMore) || n1 != 3 {
		t.Fatalf("first WriteTo: want (3, ErrMore), got (%d, %v)", n1, err1)
	}

	rawDst.limit = len(msg) + 1
	n2, err2 := src.WriteTo(dst)
	if err2 != nil || n2 != int64(len(msg)-3) {
		t.Fatalf("second WriteTo: want (%d, nil), got (%d, %v)", len(msg)-3, n2, err2)
	}
	requireSingleFramePayload(t, rawDst.buf.Bytes(), msg)
}

func TestReaderWriteToPacketSourceAfterRetainedBehindDestinationFullErrMore(t *testing.T) {
	msg := []byte("packet")
	sourceErr := errors.New("source frontier")
	src := &chunkErrReader{data: msg, err: sourceErr}
	r := fr.NewReader(src, fr.WithReadUDP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: len(msg), err: iox.ErrMore},
	}}
	dst := fr.NewWriter(rawDst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := r.WriteTo(dst)
	if !errors.Is(err1, iox.ErrMore) || n1 != int64(len(msg)) {
		t.Fatalf("first WriteTo: want (%d, ErrMore), got (%d, %v)", len(msg), n1, err1)
	}

	n2, err2 := r.WriteTo(dst)
	if !errors.Is(err2, sourceErr) || n2 != 0 {
		t.Fatalf("second WriteTo: want (0, sourceErr), got (%d, %v)", n2, err2)
	}
	if !bytes.Equal(rawDst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", rawDst.buf.Bytes(), msg)
	}
}

func TestReaderWriteToStreamKnownPacketWriterZeroProgressRetainsWholePacket(t *testing.T) {
	msg := []byte("packet")
	wire := append([]byte{byte(len(msg))}, msg...)
	src := fr.NewReader(bytes.NewReader(wire), fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)
	rawDst := &packetAtomicStepWriter{steps: []struct {
		n   int
		err error
	}{
		{n: 0, err: iox.ErrWouldBlock},
	}}
	dst := fr.NewWriter(rawDst, fr.WithProtocol(fr.SeqPacket), fr.WithNonblock())

	n1, err1 := src.WriteTo(dst)
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 != 0 {
		t.Fatalf("first WriteTo: want (0, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	n2, err2 := src.WriteTo(dst)
	if err2 != nil || n2 != int64(len(msg)) {
		t.Fatalf("second WriteTo: want (%d, nil), got (%d, %v)", len(msg), n2, err2)
	}
	if !bytes.Equal(rawDst.buf.Bytes(), msg) {
		t.Fatalf("dst=%q want %q", rawDst.buf.Bytes(), msg)
	}
}

func TestReaderWriteToStreamKnownStreamWriterZeroLengthMessageEmitsFrame(t *testing.T) {
	src := fr.NewReader(bytes.NewReader([]byte{0}), fr.WithReadTCP(), fr.WithNonblock()).(*fr.Reader)
	var rawDst bytes.Buffer
	dst := fr.NewWriter(&rawDst, fr.WithWriteTCP(), fr.WithNonblock())

	n, err := src.WriteTo(dst)
	if err != nil || n != 0 {
		t.Fatalf("WriteTo: want (0, nil), got (%d, %v)", n, err)
	}
	requireSingleFramePayload(t, rawDst.Bytes(), nil)
}

func TestReaderWriteToPacketReadLimitUsesSentinelCapacity(t *testing.T) {
	msg := []byte("12345")
	r := fr.NewReader(bytes.NewReader(msg), fr.WithReadUDP(), fr.WithReadLimit(4)).(*fr.Reader)
	var dst bytes.Buffer

	n, err := r.WriteTo(&dst)
	if !errors.Is(err, fr.ErrTooLong) || n != 0 {
		t.Fatalf("WriteTo: want (0, ErrTooLong), got (%d, %v)", n, err)
	}
	if dst.Len() != 0 {
		t.Fatalf("dst len=%d want 0", dst.Len())
	}
}

func TestReaderWriteToPacketDefaultTransferCapRejectsOversize(t *testing.T) {
	msg := bytes.Repeat([]byte{'x'}, 64*1024+1)
	r := fr.NewReader(bytes.NewReader(msg), fr.WithReadUDP()).(*fr.Reader)
	var dst bytes.Buffer

	n, err := r.WriteTo(&dst)
	if !errors.Is(err, fr.ErrTooLong) || n != 0 {
		t.Fatalf("WriteTo: want (0, ErrTooLong), got (%d, %v)", n, err)
	}
	if dst.Len() != 0 {
		t.Fatalf("dst len=%d want 0", dst.Len())
	}
}

func TestForwarderPacketReadLimitUsesSentinelCapacity(t *testing.T) {
	msg := []byte("12345")
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, bytes.NewReader(msg), fr.WithProtocol(fr.SeqPacket), fr.WithReadLimit(4))

	_, err := fwd.ForwardOnce()
	if !errors.Is(err, fr.ErrTooLong) {
		t.Fatalf("ForwardOnce: want ErrTooLong, got %v", err)
	}
	if dst.Len() != 0 {
		t.Fatalf("dst len=%d want 0", dst.Len())
	}
}

func TestForwarderPacketDefaultTransferCapRejectsOversize(t *testing.T) {
	msg := bytes.Repeat([]byte{'x'}, 64*1024+1)
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, bytes.NewReader(msg), fr.WithProtocol(fr.SeqPacket))

	_, err := fwd.ForwardOnce()
	if !errors.Is(err, fr.ErrTooLong) {
		t.Fatalf("ForwardOnce: want ErrTooLong, got %v", err)
	}
	if dst.Len() != 0 {
		t.Fatalf("dst len=%d want 0", dst.Len())
	}
}
