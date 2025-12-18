// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
)

func BenchmarkStreamWrite_4KB(b *testing.B) {
	w := fr.NewWriter(io.Discard, fr.WithProtocol(fr.BinaryStream))
	msg := make([]byte, 4096)
	b.SetBytes(int64(len(msg)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = w.Write(msg)
	}
}

func BenchmarkPacketWrite_4KB(b *testing.B) {
	w := fr.NewWriter(io.Discard, fr.WithProtocol(fr.SeqPacket))
	msg := make([]byte, 4096)
	b.SetBytes(int64(len(msg)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = w.Write(msg)
	}
}

func BenchmarkStreamRead_4KB(b *testing.B) {
	msg := make([]byte, 4096)
	// We'll use a pipe to get the wire format.
	pr, pw := io.Pipe()
	go func() {
		ww := fr.NewWriter(pw)
		for {
			_, err := ww.Write(msg)
			if err != nil {
				return
			}
		}
	}()

	r := fr.NewReader(pr)
	buf := make([]byte, 4096)
	b.SetBytes(int64(len(msg)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.Read(buf)
	}
}

// --- Benchmarks from bench_fastpath_test.go ---

type fastPathSink struct {
	b   []byte
	off int
}

func (s *fastPathSink) Write(p []byte) (int, error) {
	n := copy(s.b[s.off:], p)
	s.off += n
	return n, nil
}

type fastPathSrc struct {
	b []byte
}

func (s *fastPathSrc) Read(p []byte) (int, error) {
	if len(s.b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.b)
	s.b = s.b[n:]
	return n, nil
}

func BenchmarkWriter_ReadFrom_FastPath(b *testing.B) {
	sink := &fastPathSink{b: make([]byte, 64*1024)}
	w := fr.NewWriter(sink, fr.WithWriteTCP()).(io.ReaderFrom)
	srcPayload := make([]byte, 32*1024)
	src := &fastPathSrc{b: srcPayload}

	// Warm up
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
