// ©Hayabusa Cloud Co., Ltd. 2026. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"runtime"
	"testing"

	fr "code.hybscloud.com/framer"
)

// The direct baseline encodes the same valid lengths and copies both header and
// payload into the same kind of ready sink as the public Writer.
type benchmarkDirectWriter struct{ dst *bytes.Buffer }

func (w benchmarkDirectWriter) Write(p []byte) (int, error) {
	var header [fr.MaxHeaderSize]byte
	n := benchmarkBinaryHeader(header[:], uint64(len(p)), false)
	w.dst.Write(header[:n]) // bytes.Buffer accepts the whole header.
	return w.dst.Write(p)
}

func BenchmarkWriteEqualPayload(b *testing.B) {
	block := benchmarkPayload(64 * 1024)
	for _, size := range []int{benchmarkSmallFrameSize, len(block)} {
		for _, peers := range []int{1, 64, 1024} {
			for _, api := range []string{"Framer", "Direct"} {
				b.Run(fmt.Sprintf("size=%d/peers=%d/%s", size, peers, api), func(b *testing.B) {
					benchmarkWriteEqualPayload(b, block, size, peers, api)
				})
			}
		}
	}
}

func benchmarkWriteEqualPayload(b *testing.B, block []byte, size, peerCount int, api string) {
	frames := len(block) / size
	var header [fr.MaxHeaderSize]byte
	width, err := fr.EncodeHeader(header[:], uint64(size), binary.BigEndian)
	if err != nil {
		b.Fatal(err)
	}
	// A small-frame batch has distinct output per peer. A large-frame batch
	// sends the same whole block to whichever peer is next.
	want := make([][]byte, min(frames, peerCount))
	for frame := range frames {
		i := frame % len(want)
		offset := frame * size
		want[i] = append(want[i], header[:width]...)
		want[i] = append(want[i], block[offset:offset+size]...)
	}
	blank := make([]byte, len(want[0]))
	peers := make([]struct {
		dst bytes.Buffer
		w   io.Writer
	}, peerCount)
	for i := range peers {
		p := &peers[i]
		p.dst.Write(blank) // Allocate and touch every destination before timing.
		p.dst.Reset()
		if api == "Direct" {
			p.w = benchmarkDirectWriter{&p.dst}
		} else {
			p.w = fr.NewWriter(&p.dst)
		}
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(block)))
	next := 0
	for b.Loop() {
		if frames == 1 {
			peers[next].dst.Reset()
		} else {
			for i := range peers {
				peers[i].dst.Reset()
			}
		}
		for offset := 0; offset < len(block); offset += size {
			if n, err := peers[next].w.Write(block[offset : offset+size]); n != size || err != nil {
				b.Fatalf("peer %d wrote %d bytes: %v", next, n, err)
			}
			next++
			if next == peerCount {
				next = 0
			}
		}
	}
	visited := peerCount
	if frames == 1 {
		visited = min(visited, b.N)
	}
	for i := range peers[:visited] {
		if !bytes.Equal(peers[i].dst.Bytes(), want[i%len(want)]) {
			b.Fatalf("peer %d has incorrect framed output", i)
		}
	}
	for i := visited; i < len(peers); i++ {
		if peers[i].dst.Len() != 0 {
			b.Fatalf("unvisited peer %d received output", i)
		}
	}
	reportFrames(b, frames)
}

type benchmarkDecoderPeer struct {
	decoder fr.Decoder
	wire    [33]byte // One length byte and a 32-byte payload.
	split   int
	frames  uint64
}

func newBenchmarkDecoderPeer(id int) benchmarkDecoderPeer {
	p := benchmarkDecoderPeer{split: 3 + id%28}
	p.wire[0] = 32
	binary.BigEndian.PutUint16(p.wire[1:3], uint16(id))
	binary.BigEndian.PutUint16(p.wire[31:], uint16(id))
	return p
}

func (p *benchmarkDecoderPeer) beginFrame() bool {
	n, c, err := p.decoder.Decode(p.wire[:p.split])
	return err == fr.ErrMore && n == p.split && c.Begin && !c.End && c.Length == 32 &&
		len(c.Data) == n-1 && &c.Data[0] == &p.wire[1]
}

func (p *benchmarkDecoderPeer) endFrame() bool {
	n, c, err := p.decoder.Decode(p.wire[p.split:])
	return err == nil && n == len(p.wire)-p.split && !c.Begin && c.End && c.Length == 32 &&
		len(c.Data) == n && &c.Data[0] == &p.wire[p.split]
}

func BenchmarkDecoderPeers(b *testing.B) {
	const frames = 1024
	for _, count := range []int{1, 64, 1024} {
		b.Run(fmt.Sprintf("peers=%d", count), func(b *testing.B) {
			peers := make([]benchmarkDecoderPeer, count)
			for i := range peers {
				peers[i] = newBenchmarkDecoderPeer(i)
			}
			b.ReportAllocs()
			b.SetBytes(frames * 32)
			for b.Loop() {
				for range frames / count {
					for i := range peers {
						if !peers[i].beginFrame() {
							b.Fatalf("peer %d failed to begin its frame", i)
						}
					}
					for i := range peers {
						if !peers[i].endFrame() {
							b.Fatalf("peer %d failed to complete its frame", i)
						}
						peers[i].frames++
					}
				}
			}
			want := uint64(b.N) * uint64(frames/count)
			for i := range peers {
				p := &peers[i]
				if p.frames != want || p.decoder.Finish() != nil {
					b.Fatalf("peer %d completed %d frames, want %d at a boundary", i, p.frames, want)
				}
				if binary.BigEndian.Uint16(p.wire[1:3]) != uint16(i) || binary.BigEndian.Uint16(p.wire[31:]) != uint16(i) {
					b.Fatalf("peer %d input changed", i)
				}
			}
			reportFrames(b, frames)
		})
	}
}

func BenchmarkDecoderParallel32(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(32)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		peer := newBenchmarkDecoderPeer(0)
		for pb.Next() {
			if !peer.beginFrame() || !peer.endFrame() {
				b.Error("worker failed to complete its frame")
				return
			}
		}
		if err := peer.decoder.Finish(); err != nil {
			b.Error(err)
		}
	})
	b.StopTimer()
	reportFrames(b, 1)
}

func BenchmarkForwarderSetup(b *testing.B) {
	src := bytes.NewReader(nil)
	var live [64]*fr.Forwarder
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := range live {
		live[i] = fr.NewForwarder(io.Discard, src)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)

	b.ReportAllocs()
	next := 0
	for b.Loop() {
		live[next] = fr.NewForwarder(io.Discard, src)
		next = (next + 1) % len(live)
	}
	runtime.KeepAlive(&live)
	bytesPerPeer := (float64(after.HeapAlloc) - float64(before.HeapAlloc)) / float64(len(live))
	b.ReportMetric(bytesPerPeer, "live-B/peer")
}
