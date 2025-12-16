// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
	"code.hybscloud.com/iox"
)

// --- Benchmark fakes (allocation-free) ---

// sliceWriter writes into a preallocated byte slice without allocating.
type sliceWriter struct {
	buf []byte
	off int
}

func (w *sliceWriter) Reset() { w.off = 0 }

func (w *sliceWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Always accept the full write; benchmarks pre-size the sink.
	n := copy(w.buf[w.off:], p)
	w.off += n
	return n, nil
}

// replayReader replays a fixed wire buffer in a loop. It can limit the
// per-call chunk and optionally return ErrWouldBlock when it cannot fill the
// requested Read entirely in a single call.
type replayReader struct {
	b          []byte
	off        int
	chunkLimit int  // <=0 means no limit (use len(p))
	wouldBlock bool // if true, return ErrWouldBlock when short
}

func (r *replayReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// Determine this call's chunk size.
	c := r.chunkLimit
	if c <= 0 || c > len(p) {
		c = len(p)
	}
	if r.off >= len(r.b) {
		r.off = 0 // loop
	}
	rem := len(r.b) - r.off
	if rem < c {
		c = rem
	}
	n := copy(p, r.b[r.off:r.off+c])
	r.off += n
	if r.wouldBlock && n < len(p) {
		return n, iox.ErrWouldBlock
	}
	return n, nil
}

// benchWBWriter simulates a non-blocking writer that writes at most `limit`
// bytes per call and returns ErrWouldBlock when not all data can be consumed.
type benchWBWriter struct{ limit int }

func (w benchWBWriter) Write(p []byte) (int, error) {
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
	if n < len(p) {
		return n, iox.ErrWouldBlock
	}
	return n, nil
}

// headerSize returns the wire header size for a payload length.
func headerSize(n int) int {
	switch {
	case n <= 253:
		return 1
	case n <= 65535:
		return 3
	default:
		return 8
	}
}

// frameOnce encodes a single payload into a preallocated wire buffer.
func frameOnce(payload []byte) []byte {
	wire := make([]byte, headerSize(len(payload))+len(payload))
	sink := &sliceWriter{buf: wire}
	w := fr.NewWriter(sink, fr.WithProtocol(fr.BinaryStream))
	if n, err := w.Write(payload); err != nil || n != len(payload) {
		panic("unexpected encode failure")
	}
	return wire[:sink.off]
}

// --- A) Stream write hot path (BinaryStream) ---

func benchmarkStreamWrite(b *testing.B, size int) {
	payload := make([]byte, size)
	sink := &sliceWriter{buf: make([]byte, headerSize(size)+size)}
	w := fr.NewWriter(sink, fr.WithProtocol(fr.BinaryStream))

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink.Reset()
		n, err := w.Write(payload)
		if err != nil || n != len(payload) {
			b.Fatalf("write: n=%d err=%v", n, err)
		}
	}
}

func BenchmarkStreamWrite_Small32B(b *testing.B)   { benchmarkStreamWrite(b, 32) }
func BenchmarkStreamWrite_Medium260B(b *testing.B) { benchmarkStreamWrite(b, 260) }
func BenchmarkStreamWrite_4KiB(b *testing.B)       { benchmarkStreamWrite(b, 4<<10) }
func BenchmarkStreamWrite_64KiB(b *testing.B)      { benchmarkStreamWrite(b, 64<<10) }

// --- B) Stream read hot path (BinaryStream) ---

func benchmarkStreamRead(b *testing.B, size int, chunk int) {
	payload := make([]byte, size)
	wire := frameOnce(payload)
	rr := &replayReader{b: wire, chunkLimit: chunk}
	r := fr.NewReader(rr, fr.WithProtocol(fr.BinaryStream))
	dst := make([]byte, size)

	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n, err := r.Read(dst)
		if err != nil || n != len(dst) {
			b.Fatalf("read: n=%d err=%v", n, err)
		}
	}
}

func BenchmarkStreamRead_FullChunk_32B(b *testing.B)   { benchmarkStreamRead(b, 32, 0) }
func BenchmarkStreamRead_FullChunk_260B(b *testing.B)  { benchmarkStreamRead(b, 260, 0) }
func BenchmarkStreamRead_FullChunk_4KiB(b *testing.B)  { benchmarkStreamRead(b, 4<<10, 0) }
func BenchmarkStreamRead_FullChunk_64KiB(b *testing.B) { benchmarkStreamRead(b, 64<<10, 0) }

func BenchmarkStreamRead_SmallChunks_32B(b *testing.B)   { benchmarkStreamRead(b, 32, 7) }
func BenchmarkStreamRead_SmallChunks_260B(b *testing.B)  { benchmarkStreamRead(b, 260, 7) }
func BenchmarkStreamRead_SmallChunks_4KiB(b *testing.B)  { benchmarkStreamRead(b, 4<<10, 7) }
func BenchmarkStreamRead_SmallChunks_64KiB(b *testing.B) { benchmarkStreamRead(b, 64<<10, 7) }

// --- C) Packet pass-through (SeqPacket / Datagram) ---

func benchmarkPacketWrite(b *testing.B, size int, proto fr.Protocol) {
	payload := make([]byte, size)
	sink := &sliceWriter{buf: make([]byte, size)}
	w := fr.NewWriter(sink, fr.WithProtocol(proto))

	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink.Reset()
		n, err := w.Write(payload)
		if err != nil || n != size {
			b.Fatalf("write: n=%d err=%v", n, err)
		}
	}
}

func benchmarkPacketRead(b *testing.B, size int, proto fr.Protocol) {
	payload := make([]byte, size)
	rr := &replayReader{b: payload, chunkLimit: size}
	r := fr.NewReader(rr, fr.WithProtocol(proto))
	dst := make([]byte, size)

	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		n, err := r.Read(dst)
		if err != nil || n != size {
			b.Fatalf("read: n=%d err=%v", n, err)
		}
	}
}

func BenchmarkPacketWrite_SeqPacket_32B(b *testing.B)  { benchmarkPacketWrite(b, 32, fr.SeqPacket) }
func BenchmarkPacketWrite_SeqPacket_4KiB(b *testing.B) { benchmarkPacketWrite(b, 4<<10, fr.SeqPacket) }
func BenchmarkPacketWrite_Datagram_32B(b *testing.B)   { benchmarkPacketWrite(b, 32, fr.Datagram) }
func BenchmarkPacketWrite_Datagram_4KiB(b *testing.B)  { benchmarkPacketWrite(b, 4<<10, fr.Datagram) }

func BenchmarkPacketRead_SeqPacket_32B(b *testing.B)  { benchmarkPacketRead(b, 32, fr.SeqPacket) }
func BenchmarkPacketRead_SeqPacket_4KiB(b *testing.B) { benchmarkPacketRead(b, 4<<10, fr.SeqPacket) }
func BenchmarkPacketRead_Datagram_32B(b *testing.B)   { benchmarkPacketRead(b, 32, fr.Datagram) }
func BenchmarkPacketRead_Datagram_4KiB(b *testing.B)  { benchmarkPacketRead(b, 4<<10, fr.Datagram) }

// --- D) Would-block behavior (non-blocking semantics) ---

func benchmarkWouldBlockWrite(b *testing.B, size, limit int) {
	payload := make([]byte, size)
	under := benchWBWriter{limit: limit}
	w := fr.NewWriter(under, fr.WithNonblock(), fr.WithProtocol(fr.BinaryStream))

	var totalRetries int64
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		written := 0
		retries := 0
		for {
			n, err := w.Write(payload)
			written += n
			if err == nil {
				break
			}
			if err != iox.ErrWouldBlock {
				b.Fatalf("write: err=%v", err)
			}
			retries++
		}
		if written != len(payload) {
			b.Fatalf("written=%d want=%d", written, len(payload))
		}
		totalRetries += int64(retries)
	}
	if b.N > 0 {
		b.ReportMetric(float64(totalRetries)/float64(b.N), "retries/op")
	}
}

func benchmarkWouldBlockRead(b *testing.B, size, limit int) {
	payload := make([]byte, size)
	wire := frameOnce(payload)
	rr := &replayReader{b: wire, chunkLimit: limit, wouldBlock: true}
	r := fr.NewReader(rr, fr.WithNonblock(), fr.WithProtocol(fr.BinaryStream))
	dst := make([]byte, size)

	var totalRetries int64
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got := 0
		retries := 0
		for {
			n, err := r.Read(dst)
			got += n
			if err == nil {
				break
			}
			if err != iox.ErrWouldBlock {
				b.Fatalf("read: err=%v", err)
			}
			retries++
		}
		if got != len(dst) {
			b.Fatalf("got=%d want=%d", got, len(dst))
		}
		totalRetries += int64(retries)
	}
	if b.N > 0 {
		b.ReportMetric(float64(totalRetries)/float64(b.N), "retries/op")
	}
}

func BenchmarkWouldBlock_Write_32B_Limit5(b *testing.B)  { benchmarkWouldBlockWrite(b, 32, 5) }
func BenchmarkWouldBlock_Write_4KiB_Limit7(b *testing.B) { benchmarkWouldBlockWrite(b, 4<<10, 7) }
func BenchmarkWouldBlock_Read_32B_Limit5(b *testing.B)   { benchmarkWouldBlockRead(b, 32, 5) }
func BenchmarkWouldBlock_Read_4KiB_Limit7(b *testing.B)  { benchmarkWouldBlockRead(b, 4<<10, 7) }

// --- E) Forwarder hot path ---

func benchmarkForwardOnce(b *testing.B, size int, chunk int) {
	payload := make([]byte, size)
	wire := frameOnce(payload)
	rr := &replayReader{b: wire, chunkLimit: chunk}
	sink := &sliceWriter{buf: make([]byte, len(wire))}
	fwd := fr.NewForwarder(sink, rr, fr.WithProtocol(fr.BinaryStream))

	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr.off = 0
		sink.Reset()
		for {
			_, err := fwd.ForwardOnce()
			if err == nil {
				// One message forwarded; this benchmark measures "one ForwardOnce message".
				break
			}
			if err == io.EOF {
				// Not expected with replayReader, but keep the guard.
				break
			}
			if err != iox.ErrWouldBlock && err != iox.ErrMore {
				b.Fatalf("forward: %v", err)
			}
		}
	}
}

func BenchmarkForwardOnce_32B(b *testing.B)  { benchmarkForwardOnce(b, 32, 0) }
func BenchmarkForwardOnce_4KiB(b *testing.B) { benchmarkForwardOnce(b, 4<<10, 0) }

func BenchmarkForwardOnce_WouldBlock(b *testing.B) {
	size := 4 << 10
	payload := make([]byte, size)
	wire := frameOnce(payload)
	rr := &replayReader{b: wire, chunkLimit: len(wire)}
	// Simulate partial writes.
	dst := benchWBWriter{limit: 7}
	fwd := fr.NewForwarder(dst, rr, fr.WithNonblock(), fr.WithProtocol(fr.BinaryStream))

	var totalRetries int64
	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr.off = 0
		retries := 0
		for {
			_, err := fwd.ForwardOnce()
			if err == nil {
				break
			}
			if err != iox.ErrWouldBlock && err != iox.ErrMore {
				b.Fatalf("forward: %v", err)
			}
			retries++
		}
		totalRetries += int64(retries)
	}
	if b.N > 0 {
		b.ReportMetric(float64(totalRetries)/float64(b.N), "retries/op")
	}
}
