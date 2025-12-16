package framer_test

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	fr "code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

// stepReader returns ErrWouldBlock once, then serves the provided payload.
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

// stepWriter returns ErrWouldBlock once, then accepts all writes.
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

// no-op

func TestRetryPolicy_Nonblock_NoRetryOnWouldBlock_Read(t *testing.T) {
	msg := []byte("abc")
	r := fr.NewReader(&stepReader{payload: msg}, fr.WithNonblock())
	buf := make([]byte, len(msg))
	n, err := r.Read(buf)
	if !errors.Is(err, iox.ErrWouldBlock) {
		t.Fatalf("err=%v want ErrWouldBlock", err)
	}
	if n != 0 {
		t.Fatalf("n=%d want 0", n)
	}
}

func TestRetryPolicy_YieldAndRetry_Read(t *testing.T) {
	msg := []byte("abcdef")
	wire := encodeOne(t, msg)
	// Use stream protocol via default options.
	r := fr.NewReader(&stepReader{payload: wire}, fr.WithBlock())
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
	// Tiny sleep to cover the time.Sleep branch without slowing tests.
	w := fr.NewWriter(wunder, fr.WithRetryDelay(1*time.Microsecond))
	msg := []byte("hello world")
	n, err := w.Write(msg)
	if err != nil {
		t.Fatalf("write err: %v", err)
	}
	if n != len(msg) {
		t.Fatalf("n=%d want %d", n, len(msg))
	}
	if got := wunder.buf.Bytes(); !bytes.Equal(got, encodeOne(t, msg)) {
		t.Fatalf("wire mismatch")
	}
}

// encodeOne encodes a single message using a stream writer and returns the wire bytes.
func encodeOne(t *testing.T, payload []byte) []byte {
	t.Helper()
	var raw bytes.Buffer
	w := fr.NewWriter(&raw)
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return raw.Bytes()
}
