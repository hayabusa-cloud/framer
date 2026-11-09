// ©Hayabusa Cloud Co., Ltd. 2026. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"encoding/binary"
	"testing"

	fr "code.hybscloud.com/framer"
)

var codecBenchBytes int

func BenchmarkDecoder(b *testing.B) {
	for _, tc := range []struct {
		name         string
		size, stride int
	}{
		{"Empty", 0, 1}, {"Whole4K", 4096, 4099}, {"Fragment64", 4096, 64}, {"OneByte", 254, 1},
	} {
		b.Run(tc.name, func(b *testing.B) {
			wire := codecWire(b, binary.BigEndian, make([]byte, tc.size))
			var d fr.Decoder
			b.ReportAllocs()
			b.SetBytes(int64(tc.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for offset := 0; offset < len(wire); {
					n, c, _ := d.Decode(wire[offset:min(offset+tc.stride, len(wire))])
					offset += n
					codecBenchBytes = len(c.Data)
				}
			}
		})
	}
}

func BenchmarkEncodeHeader(b *testing.B) {
	var dst [fr.MaxHeaderSize]byte
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		n, _ := fr.EncodeHeader(dst[:], 65536, binary.BigEndian)
		codecBenchBytes = n + int(dst[0])
	}
}
