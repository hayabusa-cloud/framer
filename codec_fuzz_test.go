// ©Hayabusa Cloud Co., Ltd. 2026. All rights reserved.
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

func FuzzDecoder(f *testing.F) {
	for _, wire := range [][]byte{nil, {0}, {3, 'a', 'b', 'c', 0}, {0xfe, 0, 3, 'a'}, {0xff, 0, 0, 0, 0, 0, 0, 1, 'x'}, {0xff, 0xff, 0xff}} {
		f.Add(wire, uint8(1), false)
		f.Add(wire, uint8(7), true)
	}
	f.Fuzz(func(t *testing.T, wire []byte, stride uint8, little bool) {
		if len(wire) > 4096 {
			t.Skip()
		}
		var order binary.ByteOrder = binary.BigEndian
		if little {
			order = binary.LittleEndian
		}
		before := bytes.Clone(wire)
		d, _ := fr.NewDecoder(order, 4096)
		got, end := codecTrace(t, &d, wire, int(stride)+1)
		if end == nil {
			end = io.EOF
		}
		r := fr.NewReader(bytes.NewReader(wire), fr.WithByteOrder(order), fr.WithReadLimit(4096))
		buf := make([]byte, 4096)
		count := 0
		for {
			n, err := r.Read(buf)
			if err != nil {
				if err != end {
					t.Fatalf("terminal mismatch: decoder=%v reader=%v", end, err)
				}
				break
			}
			if count >= len(got) || !bytes.Equal(buf[:n], got[count]) {
				t.Fatalf("message %d mismatch", count)
			}
			count++
		}
		if count != len(got) || !bytes.Equal(before, wire) {
			t.Fatal("message count or source mutation mismatch")
		}
	})
}
