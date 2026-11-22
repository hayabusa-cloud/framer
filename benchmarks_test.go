// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	fr "code.hybscloud.com/framer"
)

const benchmarkSmallFrameSize = 32

var benchmarkTransports = []struct {
	name     string
	protocol fr.Protocol
	size     int
}{
	{"Stream32B", fr.BinaryStream, benchmarkSmallFrameSize},
	{"Stream4K", fr.BinaryStream, 4096},
	{"Stream64K", fr.BinaryStream, 65536},
	{"Packet4K", fr.SeqPacket, 4096},
}

func benchmarkPayload(size int) []byte {
	p := make([]byte, size)
	for i := range p {
		p[i] = byte(i)
	}
	// Mark each complete 32-byte segment so repeated or reordered data is visible.
	for i := range size / benchmarkSmallFrameSize {
		binary.BigEndian.PutUint16(p[i*benchmarkSmallFrameSize:], uint16(i))
	}
	return p
}

// Call after the timer stops. One Go benchmark operation can complete a batch.
func reportFrames(b *testing.B, perOp int) {
	frames := float64(b.N) * float64(perOp)
	elapsed := b.Elapsed()
	b.ReportMetric(float64(perOp), "frames/op")
	b.ReportMetric(frames/elapsed.Seconds(), "frames/s")
	b.ReportMetric(float64(elapsed.Nanoseconds())/frames, "ns/frame")
}

func BenchmarkWrite(b *testing.B) {
	for _, tc := range benchmarkTransports {
		b.Run(tc.name, func(b *testing.B) {
			payload := benchmarkPayload(tc.size)
			want := payload
			if tc.protocol == fr.BinaryStream {
				want = codecWire(b, binary.BigEndian, payload)
			}
			var dst bytes.Buffer
			dst.Grow(len(want))
			w := fr.NewWriter(&dst, fr.WithProtocol(tc.protocol))
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for b.Loop() {
				dst.Reset()
				if n, err := w.Write(payload); n != len(payload) || err != nil {
					b.Fatalf("Write: n=%d err=%v", n, err)
				}
			}
			if !bytes.Equal(dst.Bytes(), want) {
				b.Fatal("output bytes differ")
			}
			reportFrames(b, 1)
		})
	}
}

func BenchmarkRead(b *testing.B) {
	for _, tc := range benchmarkTransports {
		b.Run(tc.name, func(b *testing.B) {
			payload := benchmarkPayload(tc.size)
			wire := payload
			if tc.protocol == fr.BinaryStream {
				wire = codecWire(b, binary.BigEndian, payload)
			}
			src := bytes.NewReader(wire)
			benchmarkRead(b, fr.NewReader(src, fr.WithProtocol(tc.protocol)), src, wire, payload)
		})
	}
	b.Run("Stream64KIn4KReads", func(b *testing.B) {
		payload := benchmarkPayload(64 * 1024)
		wire := codecWire(b, binary.BigEndian, payload)
		src := bytes.NewReader(wire)
		benchmarkRead(b, fr.NewReader(benchmark4KReader{src}), src, wire, payload)
	})
}

type benchmark4KReader struct{ src *bytes.Reader }

func (r benchmark4KReader) Read(p []byte) (int, error) {
	return r.src.Read(p[:min(len(p), 4096)])
}

func benchmarkRead(b *testing.B, r io.Reader, src *bytes.Reader, wire, payload []byte) {
	dst := make([]byte, len(payload))
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		src.Reset(wire)
		if n, err := r.Read(dst); n != len(payload) || err != nil {
			b.Fatalf("Read: n=%d err=%v", n, err)
		}
	}
	if !bytes.Equal(dst, payload) {
		b.Fatal("payload bytes differ")
	}
	reportFrames(b, 1)
}

func benchmarkReadFrom(b *testing.B, fresh bool) {
	payload := benchmarkPayload(32 * 1024)
	want := codecWire(b, binary.BigEndian, payload)
	src := bytes.NewReader(payload)
	var dst bytes.Buffer
	dst.Grow(len(want))
	w := fr.NewWriter(&dst).(io.ReaderFrom)
	if n, err := w.ReadFrom(src); n != int64(len(payload)) || err != nil {
		b.Fatalf("warm ReadFrom: n=%d err=%v", n, err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		src.Reset(payload)
		dst.Reset()
		if fresh {
			w = fr.NewWriter(&dst).(io.ReaderFrom)
		}
		if n, err := w.ReadFrom(src); n != int64(len(payload)) || err != nil {
			b.Fatalf("ReadFrom: n=%d err=%v", n, err)
		}
	}
	if !bytes.Equal(dst.Bytes(), want) {
		b.Fatal("output bytes differ")
	}
	reportFrames(b, 1)
}

func BenchmarkTransfer(b *testing.B) {
	b.Run("ReadFrom32K", func(b *testing.B) { benchmarkReadFrom(b, false) })
	b.Run("FirstReadFrom32K", func(b *testing.B) { benchmarkReadFrom(b, true) })
	b.Run("WriteTo4K", func(b *testing.B) {
		payload := benchmarkPayload(4096)
		wire := codecWire(b, binary.BigEndian, payload)
		src := bytes.NewReader(wire)
		var dst bytes.Buffer
		dst.Grow(len(payload))
		r := fr.NewReader(src).(io.WriterTo)
		// Prepare the reusable scratch buffer before timing.
		if n, err := r.WriteTo(&dst); n != int64(len(payload)) || err != nil {
			b.Fatalf("warm WriteTo: n=%d err=%v", n, err)
		}
		b.ReportAllocs()
		b.SetBytes(int64(len(payload)))
		for b.Loop() {
			src.Reset(wire)
			dst.Reset()
			if n, err := r.WriteTo(&dst); n != int64(len(payload)) || err != nil {
				b.Fatalf("WriteTo: n=%d err=%v", n, err)
			}
		}
		if !bytes.Equal(dst.Bytes(), payload) {
			b.Fatal("payload bytes differ")
		}
		reportFrames(b, 1)
	})
	b.Run("Forward4K", func(b *testing.B) {
		payload := benchmarkPayload(4096)
		wire := codecWire(b, binary.BigEndian, payload)
		src := bytes.NewReader(wire)
		var dst bytes.Buffer
		dst.Grow(len(wire))
		f := fr.NewForwarder(&dst, src)
		b.ReportAllocs()
		b.SetBytes(int64(len(payload)))
		for b.Loop() {
			src.Reset(wire)
			dst.Reset()
			if n, err := f.ForwardOnce(); n != len(payload) || err != nil {
				b.Fatalf("ForwardOnce: n=%d err=%v", n, err)
			}
		}
		if !bytes.Equal(dst.Bytes(), wire) {
			b.Fatal("output bytes differ")
		}
		reportFrames(b, 1)
	})
}

type benchmarkQuotaWriter struct {
	bytes.Buffer
	quota int
}

func (w *benchmarkQuotaWriter) Write(p []byte) (int, error) {
	n, _ := w.Buffer.Write(p[:min(len(p), w.quota)])
	w.quota -= n
	if n != len(p) {
		return n, fr.ErrWouldBlock
	}
	return n, nil
}

func BenchmarkWriteResume(b *testing.B) {
	payload := benchmarkPayload(4096)
	wire := codecWire(b, binary.BigEndian, payload)
	headerSize := len(wire) - len(payload)
	dst := &benchmarkQuotaWriter{}
	dst.Grow(len(wire))
	w := fr.NewWriter(dst)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		dst.Reset()
		dst.quota = headerSize + len(payload)/2
		if n, err := w.Write(payload); n != len(payload)/2 || err != fr.ErrWouldBlock {
			b.Fatalf("suspended Write: n=%d err=%v", n, err)
		}
		dst.quota = len(payload) / 2
		if n, err := w.Write(payload); n != len(payload)/2 || err != nil {
			b.Fatalf("resumed Write: n=%d err=%v", n, err)
		}
	}
	if !bytes.Equal(dst.Bytes(), wire) {
		b.Fatal("output bytes differ")
	}
	reportFrames(b, 1)
}
