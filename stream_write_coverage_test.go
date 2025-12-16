package framer_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"

	fr "code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

var boom = errors.New("boom")

type wbWriter struct {
	called bool
	buf    bytes.Buffer
}

func (w *wbWriter) Write(p []byte) (int, error) {
	if !w.called {
		w.called = true
		return 0, iox.ErrWouldBlock
	}
	n, _ := w.buf.Write(p)
	return n, nil
}

type twoPhaseWriter struct {
	phase int
	buf   bytes.Buffer
}

func (w *twoPhaseWriter) Write(p []byte) (int, error) {
	switch w.phase {
	case 0:
		w.phase = 1
		n, _ := w.buf.Write(p)
		return n, nil
	case 1:
		w.phase = 2
		return 0, iox.ErrWouldBlock
	default:
		n, _ := w.buf.Write(p)
		return n, nil
	}
}

type alwaysWB struct{}

func (alwaysWB) Write([]byte) (int, error) { return 0, iox.ErrWouldBlock }

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, boom }

func TestStreamWrite_SizeClasses_BE_and_LE(t *testing.T) {
	cases := []struct {
		bo binary.ByteOrder
		sz int
	}{
		{binary.BigEndian, 5},
		{binary.BigEndian, 260},
		{binary.BigEndian, 65536},
		{binary.LittleEndian, 65536},
	}
	for _, tc := range cases {
		var dst bytes.Buffer
		w := fr.NewWriter(&dst, fr.WithByteOrder(tc.bo), fr.WithProtocol(fr.BinaryStream))
		src := bytes.Repeat([]byte{0xEE}, tc.sz)
		n, err := w.Write(src)
		if err != nil || n != tc.sz {
			t.Fatalf("write err=%v n=%d", err, n)
		}
		// decode to verify
		r := fr.NewReader(bytes.NewReader(dst.Bytes()), fr.WithByteOrder(tc.bo), fr.WithProtocol(fr.BinaryStream))
		buf := make([]byte, tc.sz)
		rn, re := r.Read(buf)
		if re != nil || rn != tc.sz || !bytes.Equal(buf, src) {
			t.Fatalf("roundtrip failed for %d/%T", tc.sz, tc.bo)
		}
	}
}

// header would-block once (n=0), then continue; WithBlock covers the yield path.
func TestStreamWrite_WouldBlockDuringHeader_RetryAndSucceed(t *testing.T) {
	wunder := &wbWriter{}
	w := fr.NewWriter(wunder, fr.WithBlock(), fr.WithProtocol(fr.BinaryStream))
	msg := []byte("payload-1")
	if n, err := w.Write(msg); err != nil || n != len(msg) {
		t.Fatalf("write: %v n=%d", err, n)
	}
	// verify decodes
	r := fr.NewReader(bytes.NewReader(wunder.buf.Bytes()), fr.WithProtocol(fr.BinaryStream))
	got := make([]byte, len(msg))
	if n, err := r.Read(got); err != nil || n != len(msg) || !bytes.Equal(got, msg) {
		t.Fatalf("decode failed")
	}
}

// would-block after the first successful call (likely during payload), then succeed after sleep retry
func TestStreamWrite_WouldBlockDuringPayload_RetryAndSucceed(t *testing.T) {
	wunder := &twoPhaseWriter{}
	w := fr.NewWriter(wunder, fr.WithRetryDelay(1*time.Microsecond), fr.WithProtocol(fr.BinaryStream))
	msg := bytes.Repeat([]byte{'x'}, 128)
	if n, err := w.Write(msg); err != nil || n != len(msg) {
		t.Fatalf("write: %v n=%d", err, n)
	}
	// verify decodes
	r := fr.NewReader(bytes.NewReader(wunder.buf.Bytes()), fr.WithProtocol(fr.BinaryStream))
	got := make([]byte, len(msg))
	if n, err := r.Read(got); err != nil || n != len(msg) || !bytes.Equal(got, msg) {
		t.Fatalf("decode failed")
	}
}

type headerPayloadWB struct {
	phase int
	limit int
}

func (w *headerPayloadWB) Write(p []byte) (int, error) {
	if w.phase == 0 { // header: accept fully
		w.phase = 1
		return len(p), nil
	}
	n := w.limit
	if n > len(p) {
		n = len(p)
	}
	if n <= 0 {
		return 0, iox.ErrWouldBlock
	}
	return n, iox.ErrWouldBlock
}

func TestStreamWrite_BufferMutationAfterWouldBlock_YieldsShortWrite(t *testing.T) {
	// Underlying writes header fully, then makes partial progress on payload and returns would-block.
	wunder := &headerPayloadWB{limit: 3}
	w := fr.NewWriter(wunder, fr.WithNonblock(), fr.WithProtocol(fr.BinaryStream))
	a := []byte("abcdef")
	n, err := w.Write(a)
	if !errors.Is(err, iox.ErrWouldBlock) || n == 0 {
		t.Fatalf("expected partial would-block, got n=%d err=%v", n, err)
	}
	// Change message size on retry should be rejected with io.ErrShortWrite since offset>0.
	b := []byte("abcdefg")
	if _, err := w.Write(b); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("err=%v want io.ErrShortWrite", err)
	}
}

func TestStreamWrite_ErrorDuringHeaderPropagates(t *testing.T) {
	w := fr.NewWriter(errWriter{}, fr.WithProtocol(fr.BinaryStream))
	if _, err := w.Write([]byte("abc")); !errors.Is(err, boom) {
		t.Fatalf("err=%v want boom", err)
	}
}
