// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"io"
	"testing"

	"code.hybscloud.com/framer"
)

// sliceSrc is a simple io.Reader over a byte slice; it does not allocate.
type sliceSrc struct{ b []byte }

func (s *sliceSrc) Read(p []byte) (int, error) {
	if len(s.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.b)
	s.b = s.b[n:]
	return n, nil
}

func BenchmarkReader_WriteTo_Stream_Small(b *testing.B) {
	// 1-byte frames payload, repeated.
	// Use a scripted reader that can be reset per iteration without allocation.
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{1}, err: nil},
		{b: []byte("x"), err: io.EOF},
	}}
	r := framer.NewReader(sr, framer.WithReadTCP()).(*framer.Reader)
	// Warm up allocation.
	_, _ = r.WriteTo(io.Discard)

	b.SetBytes(1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sr.step, sr.off = 0, 0
		_, _ = r.WriteTo(io.Discard)
	}
}

func BenchmarkReader_WriteTo_Stream_4K(b *testing.B) {
	payload := make([]byte, 4*1024)
	for i := range payload {
		payload[i] = byte(i)
	}
	sr := &scriptedReader{steps: []struct {
		b   []byte
		err error
	}{
		{b: []byte{0xFE, 0x10, 0x00}, err: nil}, // 4096 in big-endian (0x1000)
		{b: payload, err: io.EOF},
	}}
	r := framer.NewReader(sr, framer.WithReadTCP()).(*framer.Reader)
	_, _ = r.WriteTo(io.Discard)

	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sr.step, sr.off = 0, 0
		_, _ = r.WriteTo(io.Discard)
	}
}

func BenchmarkWriter_ReadFrom_Stream_32KChunk(b *testing.B) {
	// Source: 32KiB slice reader, destination: framer.Writer over io.Discard-like sink.
	srcPayload := make([]byte, 32*1024)
	for i := range srcPayload {
		srcPayload[i] = byte(i)
	}

	// Fixed sink to avoid allocations in the writer's destination.
	sink := &fixedSink{b: make([]byte, 40*1024)}
	w := framer.NewWriter(sink, framer.WithWriteTCP()).(*framer.Writer)
	src := &sliceSrc{b: srcPayload}

	// Warm up one-time allocations (e.g., Writer internal buffers) outside the timed region.
	sink.off = 0
	src.b = srcPayload
	_, _ = w.ReadFrom(src)

	b.SetBytes(int64(len(srcPayload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink.off = 0
		src.b = srcPayload
		_, _ = w.ReadFrom(src)
	}
}
