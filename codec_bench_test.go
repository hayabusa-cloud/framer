// ©Hayabusa Cloud Co., Ltd. 2026. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	fr "code.hybscloud.com/framer"
)

func BenchmarkDecoder(b *testing.B) {
	for _, tc := range []struct {
		name                 string
		size, stride, frames int
		order                binary.ByteOrder
	}{
		{"Empty", 0, 1, 1, binary.BigEndian},
		{"Separate8x32", 32, 33, 8, binary.BigEndian},
		{"Coalesced8x32", 32, 8 * 33, 8, binary.BigEndian},
		{"Whole253", 253, 254, 1, binary.BigEndian},
		{"Whole254", 254, 257, 1, binary.BigEndian},
		{"Whole4K", 4096, 4099, 1, binary.BigEndian},
		{"Whole65535", 65535, 65538, 1, binary.BigEndian},
		{"Whole64K", 65536, 65544, 1, binary.BigEndian},
		{"Fragment64KIn4K", 65536, 4096, 1, binary.BigEndian},
		{"LittleEndian4K", 4096, 4099, 1, binary.LittleEndian},
		{"Fragment64", 4096, 64, 1, binary.BigEndian},
		{"OneByte", 254, 1, 1, binary.BigEndian},
		{"Coalesced8x4K", 4096, 8 * 4099, 8, binary.BigEndian},
	} {
		b.Run(tc.name, func(b *testing.B) {
			want := benchmarkPayload(tc.size)
			frame := codecWire(b, tc.order, want)
			wire := bytes.Repeat(frame, tc.frames)
			d, err := fr.NewDecoder(tc.order, 0)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(tc.size * tc.frames))
			for b.Loop() {
				payload, begins, ends := 0, 0, 0
				for start := 0; start < len(wire); start += tc.stride {
					input := wire[start:min(start+tc.stride, len(wire))]
					for len(input) > 0 {
						n, chunk, err := d.Decode(input)
						if n <= 0 || n > len(input) || (err != nil && err != fr.ErrMore) || chunk.End != (err == nil) {
							b.Fatalf("Decode: consumed=%d end=%t err=%v", n, chunk.End, err)
						}
						input = input[n:]
						payload += len(chunk.Data)
						if chunk.Begin {
							begins++
						}
						if chunk.End {
							ends++
						}
					}
				}
				if payload != tc.size*tc.frames || begins != tc.frames || ends != tc.frames {
					b.Fatalf("Decode: payload=%d begins=%d ends=%d", payload, begins, ends)
				}
			}
			if err := d.Finish(); err != nil {
				b.Fatal(err)
			}
			d.Reset()
			messages, err := codecTrace(b, &d, wire, tc.stride)
			if err != nil || len(messages) != tc.frames {
				b.Fatalf("decoded %d messages: %v", len(messages), err)
			}
			for _, msg := range messages {
				if !bytes.Equal(msg, want) {
					b.Fatal("decoded payload differs")
				}
			}
			reportFrames(b, tc.frames)
		})
	}
}

func BenchmarkEncodeHeader(b *testing.B) {
	for _, order := range []struct {
		name   string
		value  binary.ByteOrder
		little bool
	}{
		{"BigEndian", binary.BigEndian, false},
		{"LittleEndian", binary.LittleEndian, true},
	} {
		for _, tc := range []struct {
			name   string
			length uint64
		}{
			{"1Byte", 253}, {"3Bytes", 4096}, {"8Bytes", 65536},
		} {
			var want [fr.MaxHeaderSize]byte
			width := benchmarkBinaryHeader(want[:], tc.length, order.little)
			b.Run(order.name+"/"+tc.name+"/Framer", func(b *testing.B) {
				var dst [fr.MaxHeaderSize]byte
				b.ReportAllocs()
				b.SetBytes(int64(width))
				for b.Loop() {
					n, err := fr.EncodeHeader(dst[:], tc.length, order.value)
					if n != width || err != nil {
						b.Fatalf("EncodeHeader: n=%d err=%v", n, err)
					}
				}
				if !bytes.Equal(dst[:width], want[:width]) {
					b.Fatal("header bytes differ")
				}
			})
			b.Run(order.name+"/"+tc.name+"/Binary", func(b *testing.B) {
				var dst [fr.MaxHeaderSize]byte
				b.ReportAllocs()
				b.SetBytes(int64(width))
				for b.Loop() {
					if n := benchmarkBinaryHeader(dst[:], tc.length, order.little); n != width {
						b.Fatalf("binary header: n=%d", n)
					}
				}
				if !bytes.Equal(dst[:width], want[:width]) {
					b.Fatal("header bytes differ")
				}
			})
		}
	}
}

// benchmarkBinaryHeader encodes the same wire format using encoding/binary.
// Its prepared inputs have sufficient storage and a representable length.
func benchmarkBinaryHeader(dst []byte, length uint64, little bool) int {
	if length < 254 {
		dst[0] = byte(length)
		return 1
	}
	if length <= 65535 {
		dst[0] = 0xfe
		if little {
			binary.LittleEndian.PutUint16(dst[1:3], uint16(length))
		} else {
			binary.BigEndian.PutUint16(dst[1:3], uint16(length))
		}
		return 3
	}
	if little {
		binary.LittleEndian.PutUint64(dst[:8], length<<8)
	} else {
		binary.BigEndian.PutUint64(dst[:8], length)
	}
	dst[0] = 0xff
	return 8
}
