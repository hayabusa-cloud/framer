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

// simpleSrc is a minimal Reader that does not implement WriterTo.
type simpleSrc struct{ b []byte }

func (s *simpleSrc) Read(p []byte) (int, error) {
	if len(s.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.b)
	s.b = s.b[n:]
	return n, nil
}

type spyWriter struct {
	w      *framer.Writer
	called int
}

func (s *spyWriter) Write(p []byte) (int, error) { return s.w.Write(p) }
func (s *spyWriter) ReadFrom(src io.Reader) (int64, error) {
	s.called++
	return s.w.ReadFrom(src)
}

func TestReaderFrom_FastPath_Selected(t *testing.T) {
	var raw bytes.Buffer
	w := framer.NewWriter(&raw, framer.WithWriteTCP()).(*framer.Writer)
	spy := &spyWriter{w: w}

	src := &simpleSrc{b: []byte("hello")}
	n, err := iox.CopyPolicy(spy, src, &iox.ReturnPolicy{})
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if spy.called == 0 {
		t.Fatalf("ReaderFrom was not used by CopyPolicy")
	}

	// Ensure round-trip decodes via framer.Reader.
	r := framer.NewReader(&raw, framer.WithReadTCP()).(*framer.Reader)
	buf := make([]byte, 5)
	rn, re := r.Read(buf)
	if re != nil || rn != 5 || string(buf) != "hello" {
		t.Fatalf("round-trip rn=%d re=%v buf=%q", rn, re, string(buf))
	}
}

func TestWriter_ReadFrom_WouldBlock_ReadSide(t *testing.T) {
	var raw bytes.Buffer
	w := framer.NewWriter(&raw, framer.WithWriteTCP()).(*framer.Writer)

	// Source emits 3 bytes then ErrWouldBlock.
	src := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte("abc"), err: nil},
		{b: nil, err: iox.ErrWouldBlock},
		{b: []byte("def"), err: io.EOF},
	}}

	n, err := w.ReadFrom(src)
	if !errors.Is(err, iox.ErrWouldBlock) || n != 3 {
		t.Fatalf("want (3, ErrWouldBlock), got (%d, %v)", n, err)
	}

	// Decode and check the first message "abc" is present.
	r := framer.NewReader(&raw, framer.WithReadTCP()).(*framer.Reader)
	buf := make([]byte, 3)
	rn, re := r.Read(buf)
	if re != nil || rn != 3 || string(buf) != "abc" {
		t.Fatalf("rn=%d re=%v buf=%q", rn, re, string(buf))
	}
}

func TestWriter_ReadFrom_PropagatesErrMore(t *testing.T) {
	var raw bytes.Buffer
	w := framer.NewWriter(&raw, framer.WithWriteTCP()).(*framer.Writer)
	src := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: nil, err: iox.ErrMore},
	}}
	n, err := w.ReadFrom(src)
	if !errors.Is(err, iox.ErrMore) || n != 0 {
		t.Fatalf("want (0, ErrMore), got (%d, %v)", n, err)
	}
}

// wouldBlockOnWriteWriter returns WouldBlock after writing limit bytes.
type wouldBlockOnWriteWriter struct {
	buf   bytes.Buffer
	limit int
	wrote int
}

func (w *wouldBlockOnWriteWriter) Write(p []byte) (int, error) {
	if w.wrote >= w.limit {
		return 0, iox.ErrWouldBlock
	}
	n := w.limit - w.wrote
	if n > len(p) {
		n = len(p)
	}
	w.buf.Write(p[:n])
	w.wrote += n
	if n < len(p) {
		return n, iox.ErrWouldBlock
	}
	return n, nil
}

func TestWriter_ReadFrom_WouldBlock_WriteSide(t *testing.T) {
	// Use a writer that returns WouldBlock after writing some bytes.
	dst := &wouldBlockOnWriteWriter{limit: 2}
	w := framer.NewWriter(dst, framer.WithWriteTCP()).(*framer.Writer)

	src := &simpleSrc{b: []byte("hello")}
	n, err := w.ReadFrom(src)
	// The write should fail with WouldBlock during header or payload write.
	if !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("want ErrWouldBlock, got (%d, %v)", n, err)
	}
}

// errMoreWriter returns ErrMore after writing.
type errMoreWriter struct {
	buf bytes.Buffer
}

func (w *errMoreWriter) Write(p []byte) (int, error) {
	n, _ := w.buf.Write(p)
	return n, iox.ErrMore
}

func TestWriter_ReadFrom_ErrMore_WriteSide(t *testing.T) {
	dst := &errMoreWriter{}
	w := framer.NewWriter(dst, framer.WithWriteTCP()).(*framer.Writer)

	src := &simpleSrc{b: []byte("test")}
	n, err := w.ReadFrom(src)
	if !errors.Is(err, iox.ErrMore) {
		t.Fatalf("want ErrMore, got (%d, %v)", n, err)
	}
	// ErrMore is returned during the write, progress depends on when it occurs.
	// The key assertion is that ErrMore is propagated correctly.
}

// customErrWriter returns a custom error.
type customErrWriter struct {
	err error
}

func (w *customErrWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

func TestWriter_ReadFrom_WriteError_Propagates(t *testing.T) {
	customErr := errors.New("custom write error")
	dst := &customErrWriter{err: customErr}
	w := framer.NewWriter(dst, framer.WithWriteTCP()).(*framer.Writer)

	src := &simpleSrc{b: []byte("data")}
	_, err := w.ReadFrom(src)
	if !errors.Is(err, customErr) {
		t.Fatalf("want custom error, got %v", err)
	}
}

// customErrReader returns a custom error after some data.
type customErrReader struct {
	data []byte
	err  error
	done bool
}

func (r *customErrReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, r.err
	}
	r.done = true
	n := copy(p, r.data)
	return n, nil
}

func TestWriter_ReadFrom_ReadError_Propagates(t *testing.T) {
	var raw bytes.Buffer
	w := framer.NewWriter(&raw, framer.WithWriteTCP()).(*framer.Writer)

	customErr := errors.New("custom read error")
	src := &customErrReader{data: []byte("abc"), err: customErr}

	n, err := w.ReadFrom(src)
	// First read succeeds with "abc", second read returns customErr.
	if !errors.Is(err, customErr) {
		t.Fatalf("want custom error, got (%d, %v)", n, err)
	}
	// Should have written the first chunk.
	if n != 3 {
		t.Fatalf("n=%d want=3", n)
	}
}
