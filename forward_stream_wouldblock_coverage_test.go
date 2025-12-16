package framer_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

type wbOnceReader struct {
	b          []byte
	off        int
	triggered  bool
	triggerLen int
	chunk      int
}

func (r *wbOnceReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	// Inject ErrWouldBlock only once, only on the full-payload read call
	// (len(p)==triggerLen) to avoid interfering with header parsing.
	if !r.triggered && r.triggerLen > 0 && len(p) == r.triggerLen {
		r.triggered = true
		n := r.chunk
		if n <= 0 {
			n = 1
		}
		rem := len(r.b) - r.off
		if n > rem {
			n = rem
		}
		copy(p, r.b[r.off:r.off+n])
		r.off += n
		return n, iox.ErrWouldBlock
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

func TestForward_Stream_ReadWouldBlockWithProgress_ThenCompletesOnRetry(t *testing.T) {
	payload := []byte("0123456789")
	// Encode one BinaryStream message.
	var wire bytes.Buffer
	w := fr.NewWriter(&wire, fr.WithProtocol(fr.BinaryStream))
	if _, err := w.Write(payload); err != nil {
		t.Fatalf("encode err=%v", err)
	}

	src := &wbOnceReader{b: wire.Bytes(), triggerLen: len(payload), chunk: 3}
	var dst bytes.Buffer
	fwd := fr.NewForwarder(&dst, src, fr.WithProtocol(fr.BinaryStream), fr.WithNonblock())

	// First call: would-block during payload read.
	n1, err1 := fwd.ForwardOnce()
	if !errors.Is(err1, iox.ErrWouldBlock) || n1 == 0 {
		t.Fatalf("want (n>0, ErrWouldBlock), got (%d, %v)", n1, err1)
	}

	// Retry: complete message.
	n2, err2 := fwd.ForwardOnce()
	if err2 != nil || n2 != len(payload) {
		t.Fatalf("want (%d, nil), got (%d, %v)", len(payload), n2, err2)
	}
}
