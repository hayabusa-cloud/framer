// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"io"
	"testing"

	"code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

// fixedSink is a no-alloc writer into a preallocated buffer.
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
	// One 4-byte message: header(1) + payload("DATA").
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{4}, err: nil},
		{b: []byte("DATA"), err: io.EOF},
	}}
	r := framer.NewReader(sr, framer.WithReadTCP()).(*framer.Reader)

	// Warm-up to allocate scratch buffer once (outside measurement).
	_, _ = r.WriteTo(io.Discard)

	allocs := testing.AllocsPerRun(1000, func() {
		// Reset scripted reader state.
		sr.step, sr.off = 0, 0
		_, _ = r.WriteTo(io.Discard)
	})
	if allocs != 0 {
		t.Fatalf("allocs/op = %v want 0", allocs)
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
	r := framer.NewReader(sr, framer.WithReadTCP()).(*framer.Reader)
	_, _ = r.WriteTo(io.Discard) // warm-up allocate

	allocs := testing.AllocsPerRun(1000, func() {
		sr.step, sr.off = 0, 0
		_, _ = r.WriteTo(io.Discard)
	})
	if allocs != 0 {
		t.Fatalf("allocs/op = %v want 0", allocs)
	}
}

func TestAllocs_Writer_ReadFrom_Stream(t *testing.T) {
	// Prepare writer with fixed sink to avoid allocations in destination.
	sink := &fixedSink{b: make([]byte, 128)}
	w := framer.NewWriter(sink, framer.WithWriteTCP()).(*framer.Writer)

	// Source scripted reader: emits 32 bytes then EOF.
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

	// Warm-up to ensure any one-time allocations occur before measuring.
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
