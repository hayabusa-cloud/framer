// ©Hayabusa Cloud Co., Ltd. 2026. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"
	"runtime"
	"testing"

	fr "code.hybscloud.com/framer"
)

type codecOtherOrder struct {
	binary.ByteOrder
	marker []byte
}

func TestCodecHeaderWire(t *testing.T) {
	cases := []struct {
		length      uint64
		big, little string
	}{
		{0, "00", "00"}, {1, "01", "01"}, {253, "fd", "fd"},
		{254, "fe00fe", "fefe00"}, {65535, "feffff", "feffff"},
		{65536, "ff00000000010000", "ff00000100000000"},
		{0x01020304050607, "ff01020304050607", "ff07060504030201"},
		{1<<56 - 1, "ffffffffffffffff", "ffffffffffffffff"},
	}
	for _, order := range []binary.ByteOrder{nil, binary.BigEndian, binary.LittleEndian, binary.NativeEndian} {
		little := order == binary.LittleEndian || order == binary.NativeEndian && binary.NativeEndian.Uint16([]byte{1, 0}) == 1
		for _, tc := range cases {
			wantHex := tc.big
			if little {
				wantHex = tc.little
			}
			want, _ := hex.DecodeString(wantHex)
			dst := bytes.Repeat([]byte{0xa5}, fr.MaxHeaderSize+2)
			n, err := fr.EncodeHeader(dst, tc.length, order)
			if err != nil || n != len(want) || !bytes.Equal(dst[:n], want) || !bytes.Equal(dst[n:], bytes.Repeat([]byte{0xa5}, len(dst)-n)) {
				t.Fatalf("header order=%v length=%d: %x, %d, %v", order, tc.length, dst, n, err)
			}
			d, err := fr.NewDecoder(order, 0)
			if err != nil {
				t.Fatal(err)
			}
			consumed, c, err := d.Decode(dst[:n])
			wantErr := fr.ErrMore
			if tc.length == 0 {
				wantErr = nil
			}
			if consumed != n || err != wantErr || !c.Begin || c.End != (tc.length == 0) || c.Length != tc.length || len(c.Data) != 0 {
				t.Fatalf("decode header length=%d: n=%d chunk=%+v err=%v", tc.length, consumed, c, err)
			}
			if tc.length > 0 {
				consumed, c, err = d.Decode([]byte{42})
				wantErr = fr.ErrMore
				if tc.length == 1 {
					wantErr = nil
				}
				if consumed != 1 || c.Length != tc.length || c.Begin || c.End != (tc.length == 1) || err != wantErr {
					t.Fatalf("body after header: %d %+v %v", consumed, c, err)
				}
			}
		}
	}
}

func TestCodecArgumentsAndHeaderFailure(t *testing.T) {
	invalid := codecOtherOrder{ByteOrder: binary.BigEndian, marker: []byte{1}}
	for _, tc := range []struct {
		length uint64
		order  binary.ByteOrder
		size   int
		want   error
	}{
		{1, invalid, 8, fr.ErrInvalidArgument}, {1 << 56, nil, 8, fr.ErrTooLong},
		{1, nil, 0, io.ErrShortBuffer}, {254, nil, 2, io.ErrShortBuffer},
		{65536, nil, 7, io.ErrShortBuffer},
	} {
		dst := bytes.Repeat([]byte{0xa5}, tc.size)
		before := bytes.Clone(dst)
		if n, err := fr.EncodeHeader(dst, tc.length, tc.order); n != 0 || err != tc.want || !bytes.Equal(dst, before) {
			t.Fatalf("rejection: n=%d err=%v dst=%x", n, err, dst)
		}
	}
	if _, err := fr.NewDecoder(invalid, 0); err != fr.ErrInvalidArgument {
		t.Fatal(err)
	}
	if _, err := fr.NewDecoder(nil, 1<<56); err != fr.ErrInvalidArgument {
		t.Fatal(err)
	}
}

func codecWire(t testing.TB, order binary.ByteOrder, messages ...[]byte) []byte {
	t.Helper()
	var dst bytes.Buffer
	w := fr.NewWriter(&dst, fr.WithByteOrder(order))
	for _, msg := range messages {
		if n, err := w.Write(msg); n != len(msg) || err != nil {
			t.Fatalf("legacy encode: %d %v", n, err)
		}
	}
	return dst.Bytes()
}

// codecTrace validates the borrowed surface while collecting completed messages.
// Unfinished payload is deliberately excluded from the completed-message trace.
func codecTrace(t testing.TB, d *fr.Decoder, wire []byte, stride int) ([][]byte, error) {
	t.Helper()
	var messages [][]byte
	var current []byte
	var length uint64
	active := false
	for off := 0; off < len(wire); {
		end := min(off+stride, len(wire))
		for off < end {
			src := wire[off:end]
			n, c, err := d.Decode(src)
			if n <= 0 || n > len(src) || len(c.Data) > n || cap(c.Data) != len(c.Data) {
				t.Fatalf("invalid progress: %d %+v %v", n, c, err)
			}
			if len(c.Data) > 0 && &c.Data[0] != &src[n-len(c.Data)] {
				t.Fatal("payload was not borrowed from consumed input")
			}
			if c.Begin {
				if active {
					t.Fatal("repeated Begin")
				}
				active, length = true, c.Length
			}
			if c.Begin || c.End || len(c.Data) > 0 {
				if !active || c.Length != length {
					t.Fatal("chunk without matching message")
				}
				current = append(current, c.Data...)
			}
			if c.End {
				if uint64(len(current)) != length || err != nil {
					t.Fatal("premature End")
				}
				messages = append(messages, current)
				current, active = nil, false
			}
			off += n
			switch err {
			case nil:
				if !c.End {
					t.Fatal("success without End")
				}
			case fr.ErrMore:
				if c.End {
					t.Fatal("continuation after End")
				}
			default:
				if c.Begin || c.End || len(c.Data) != 0 {
					t.Fatal("failure emitted data")
				}
				return messages, err
			}
		}
		if n, c, err := d.Decode(nil); n != 0 || c.Begin || c.End || c.Length != 0 || len(c.Data) != 0 || err != fr.ErrWouldBlock {
			t.Fatalf("empty input: %d %+v %v", n, c, err)
		}
	}
	return messages, d.Finish()
}

func TestDecoderFragmentationAndLegacyWire(t *testing.T) {
	messages := [][]byte{nil, []byte("hello"), make([]byte, 254), make([]byte, 65535), make([]byte, 65536), nil}
	for _, msg := range messages {
		for i := range msg {
			msg[i] = byte(i * 31)
		}
	}
	for _, order := range []binary.ByteOrder{binary.BigEndian, binary.LittleEndian} {
		wire := codecWire(t, order, messages...)
		before := bytes.Clone(wire)
		for _, stride := range []int{1, 2, 7, 4096, len(wire)} {
			d, _ := fr.NewDecoder(order, 0)
			got, err := codecTrace(t, &d, wire, stride)
			if err != nil || len(got) != len(messages) {
				t.Fatalf("stride=%d messages=%d err=%v", stride, len(got), err)
			}
			for i := range messages {
				if !bytes.Equal(got[i], messages[i]) {
					t.Fatalf("message %d mismatch", i)
				}
			}
		}
		if !bytes.Equal(before, wire) {
			t.Fatal("input mutated")
		}
	}
}

func TestDecoderEveryHeaderSplitAndBorrowLifetime(t *testing.T) {
	for _, length := range []int{3, 254, 65536} {
		wire := codecWire(t, binary.BigEndian, make([]byte, length))
		for split := 1; split <= min(len(wire)-1, 10); split++ {
			var d fr.Decoder
			n, first, err := d.Decode(wire[:split])
			if n != split || err != fr.ErrMore {
				t.Fatalf("split=%d: n=%d err=%v", split, n, err)
			}
			firstCopy := bytes.Clone(first.Data)
			n, last, err := d.Decode(wire[split:])
			if n != len(wire)-split || err != nil || !last.End || first.Begin == last.Begin {
				t.Fatalf("resumed: %d %+v %v", n, last, err)
			}
			if err = d.Finish(); err != nil {
				t.Fatal(err)
			}
			d.Reset()
			if !bytes.Equal(first.Data, firstCopy) {
				t.Fatal("old borrow changed")
			}
		}
	}
	var d fr.Decoder
	src := []byte{1, 'x', 0, 1, 'y'}
	n, c, err := d.Decode(src)
	if n != 2 || !c.Begin || !c.End || err != nil {
		t.Fatalf("crossed frame: %d %+v %v", n, c, err)
	}
	src[1] = 'z'
	if string(c.Data) != "z" {
		t.Fatal("view does not alias source")
	}
}

func TestDecoderCallerBudget(t *testing.T) {
	wire := make([]byte, 1025) // Each zero byte is a complete empty frame.
	var d fr.Decoder
	frames := 0
	for len(wire) > 0 {
		for budget := 0; budget < 7 && len(wire) > 0; budget++ {
			n, c, err := d.Decode(wire)
			if n != 1 || err != nil || !c.Begin || !c.End || c.Length != 0 || len(c.Data) != 0 {
				t.Fatalf("empty-frame budget: %d %+v %v", n, c, err)
			}
			wire = wire[n:]
			frames++
		}
	}
	if frames != 1025 || d.Finish() != nil {
		t.Fatal("budgeted decoding lost a frame")
	}
}

func TestDecoderExtendedSmallLengths(t *testing.T) {
	for _, wire := range [][]byte{{0xfe, 0, 0}, {0xff, 0, 0, 0, 0, 0, 0, 0}, {0xfe, 0, 1, 'x'}, {0xff, 0, 0, 0, 0, 0, 0, 1, 'x'}} {
		var d fr.Decoder
		got, err := codecTrace(t, &d, wire, 1)
		if err != nil || len(got) != 1 {
			t.Fatalf("extended encoding: %x %v", wire, err)
		}
		buf := make([]byte, 1)
		n, e := fr.NewReader(bytes.NewReader(wire)).Read(buf)
		if e != nil || !bytes.Equal(got[0], buf[:n]) {
			t.Fatal("extended length differs from Reader")
		}
	}
}

func TestDecoderFinishAndReset(t *testing.T) {
	for _, size := range []int{3, 254, 65536} {
		wire := codecWire(t, binary.BigEndian, make([]byte, size))
		for cut := 1; cut < min(len(wire), 12); cut++ {
			var d fr.Decoder
			if _, err := codecTrace(t, &d, wire[:cut], 1); err != io.ErrUnexpectedEOF {
				t.Fatalf("cut=%d: %v", cut, err)
			}
			if err := d.Finish(); err != io.ErrUnexpectedEOF {
				t.Fatal(err)
			}
			if n, _, err := d.Decode([]byte{0}); n != 0 || err != io.ErrUnexpectedEOF {
				t.Fatal("truncation not sticky")
			}
			d.Reset()
			if err := d.Finish(); err != nil {
				t.Fatal(err)
			}
			if err := d.Finish(); err != nil {
				t.Fatal(err)
			}
			if n, _, err := d.Decode([]byte{0}); n != 0 || err != io.EOF {
				t.Fatal("finished decoder accepted input")
			}
		}
	}
	d, _ := fr.NewDecoder(binary.LittleEndian, 300)
	_, _, _ = d.Decode([]byte{0xfe})
	_ = d.Finish()
	d.Reset()
	got, err := codecTrace(t, &d, codecWire(t, binary.LittleEndian, make([]byte, 256)), 7)
	if err != nil || len(got) != 1 || len(got[0]) != 256 {
		t.Fatal("Reset lost byte order")
	}
	d.Reset()
	wire := codecWire(t, binary.LittleEndian, make([]byte, 301))
	if n, c, err := d.Decode(wire); n != 3 || c.Begin || len(c.Data) != 0 || err != fr.ErrTooLong {
		t.Fatalf("limit: %d %+v %v", n, c, err)
	}
	if err := d.Finish(); err != fr.ErrTooLong {
		t.Fatal("Finish lost limit failure")
	}
	if n, _, err := d.Decode(wire); n != 0 || err != fr.ErrTooLong {
		t.Fatal("limit failure not sticky")
	}
}

func TestCodecFreshAndSteadyAllocations(t *testing.T) {
	var before, after runtime.MemStats
	var header [fr.MaxHeaderSize]byte
	wire := [...]byte{0xfe, 0, 2, 'o', 'k'}
	bad := false
	run := func() {
		d, err := fr.NewDecoder(nil, 2)
		bad = bad || err != nil
		_, _, _ = d.Decode(nil)
		for i := range wire {
			_, _, _ = d.Decode(wire[i : i+1])
		}
		bad = bad || d.Finish() != nil
		d.Reset()
		_, _, err = d.Decode([]byte{3})
		bad = bad || err != fr.ErrTooLong
		d.Reset()
		_, _, _ = d.Decode([]byte{0xfe})
		bad = bad || d.Finish() != io.ErrUnexpectedEOF
		_, err = fr.EncodeHeader(header[:], 65536, binary.NativeEndian)
		bad = bad || err != nil
	}
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	if after.Mallocs != before.Mallocs {
		t.Fatalf("fresh codec allocated %d objects", after.Mallocs-before.Mallocs)
	}
	if got := testing.AllocsPerRun(1000, run); got != 0 {
		t.Fatalf("steady allocations: %v", got)
	}
	if bad {
		t.Fatal("allocation workload failed")
	}
}
