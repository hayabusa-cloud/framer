// ©Hayabusa Cloud Co., Ltd. 2026. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"runtime"
	"testing"

	fr "code.hybscloud.com/framer"
)

func TestStreamStandardAndDelegatingByteOrders(t *testing.T) {
	for _, tc := range []struct {
		name             string
		order, reference binary.ByteOrder
	}{
		{"big", binary.BigEndian, binary.BigEndian},
		{"little", binary.LittleEndian, binary.LittleEndian},
		{"native", binary.NativeEndian, binary.NativeEndian},
		{"wrapped-big", codecOtherOrder{ByteOrder: binary.BigEndian}, binary.BigEndian},
		{"wrapped-little", codecOtherOrder{ByteOrder: binary.LittleEndian}, binary.LittleEndian},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, size := range []int{65535, 65536, 66051} {
				payload := make([]byte, size)
				for i := range payload {
					payload[i] = byte(i * 31)
				}
				var header [fr.MaxHeaderSize]byte
				h, err := fr.EncodeHeader(header[:], uint64(size), tc.reference)
				if err != nil {
					t.Fatal(err)
				}
				want := append(bytes.Clone(header[:h]), payload...)
				t.Run("write", func(t *testing.T) {
					var dst bytes.Buffer
					w := fr.NewWriter(&dst, fr.WithWriteByteOrder(tc.order))
					if n, err := w.Write(payload); n != size || err != nil {
						t.Fatalf("write: %d %v", n, err)
					}
					if !bytes.Equal(dst.Bytes(), want) {
						t.Fatalf("length %d: header %x, want %x", size, dst.Bytes()[:h], header[:h])
					}
				})
				t.Run("read", func(t *testing.T) {
					r := fr.NewReader(bytes.NewReader(want), fr.WithReadByteOrder(tc.order), fr.WithReadLimit(size))
					got := make([]byte, size)
					if n, err := r.Read(got); n != size || err != nil || !bytes.Equal(got, payload) {
						t.Fatalf("length %d: read %d %v", size, n, err)
					}
				})
			}
		})
	}
}

// signalBoundaryReader returns a transport control with the byte ending a
// chosen prefix; later calls read the remainder normally.
type signalBoundaryReader struct {
	src       *bytes.Reader
	remaining int
	control   error
}

func (r *signalBoundaryReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return r.src.Read(p)
	}
	n, err := r.src.Read(p[:min(len(p), r.remaining)])
	r.remaining -= n
	if r.remaining == 0 {
		return n, r.control
	}
	return n, err
}

func TestForwarderFinishesFullReadBeforeNextFrame(t *testing.T) {
	wire := []byte{3, 'a', 'b', 'c', 2, 'x', 'y', 0}
	source := &signalBoundaryReader{bytes.NewReader(wire), 4, fr.ErrWouldBlock}
	var dst bytes.Buffer
	f := fr.NewForwarder(&dst, source)
	if n, err := f.ForwardOnce(); n != 3 || err != fr.ErrWouldBlock || dst.Len() != 0 {
		t.Fatalf("read suspension: %d %v, output %x", n, err, dst.Bytes())
	}
	for i, size := range []int{3, 2, 0} {
		if n, err := f.ForwardOnce(); n != size || err != nil {
			t.Fatalf("frame %d: %d %v", i, n, err)
		}
		if i == 0 && source.src.Len() != 4 {
			t.Fatal("source advanced before the next message")
		}
	}
	if n, err := f.ForwardOnce(); n != 0 || err != io.EOF {
		t.Fatalf("end: %d %v", n, err)
	}
	if !bytes.Equal(dst.Bytes(), wire) {
		t.Fatalf("wire %x, want %x", dst.Bytes(), wire)
	}
}

func TestReaderReadCountsNewBytesAcrossResume(t *testing.T) {
	for _, prefix := range []int{2, 4} {
		r := fr.NewReader(&signalBoundaryReader{bytes.NewReader([]byte{3, 'a', 'b', 'c'}), prefix, fr.ErrWouldBlock})
		var buf [3]byte
		n, err := r.Read(buf[:])
		if n != prefix-1 || err != fr.ErrWouldBlock {
			t.Fatalf("suspended: %d %v", n, err)
		}
		total := n
		n, err = r.Read(buf[:])
		if n != 4-prefix || err != nil {
			t.Fatalf("resumed: %d %v", n, err)
		}
		total += n
		if string(buf[:total]) != "abc" {
			t.Fatal("message was not accumulated in the original buffer")
		}
	}
}

func TestStreamAndForwarderFreshAndSteadyAllocations(t *testing.T) {
	payload := make([]byte, 65536)
	var header [fr.MaxHeaderSize]byte
	h, _ := fr.EncodeHeader(header[:], uint64(len(payload)), binary.NativeEndian)
	wire := append(bytes.Clone(header[:h]), payload...)
	source := bytes.NewReader(wire)
	r := fr.NewReader(source, fr.WithByteOrder(binary.NativeEndian))
	w := fr.NewWriter(io.Discard, fr.WithByteOrder(binary.NativeEndian))
	buf := make([]byte, len(payload))
	forwardWire := []byte{3, 'a', 'b', 'c', 2, 'x', 'y', 0}
	forwardSource := &signalBoundaryReader{bytes.NewReader(forwardWire), 4, fr.ErrWouldBlock}
	f := fr.NewForwarder(io.Discard, forwardSource)
	bad := false
	run := func() {
		source.Reset(wire)
		n, err := r.Read(buf)
		bad = bad || n != len(payload) || err != nil
		n, err = w.Write(payload)
		bad = bad || n != len(payload) || err != nil
		forwardSource.src.Reset(forwardWire)
		forwardSource.remaining = 4
		n, err = f.ForwardOnce()
		bad = bad || n != 3 || err != fr.ErrWouldBlock
		for _, size := range [...]int{3, 2, 0} {
			n, err = f.ForwardOnce()
			bad = bad || n != size || err != nil
		}
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	if after.Mallocs != before.Mallocs {
		t.Fatalf("fresh hot operations allocated %d objects", after.Mallocs-before.Mallocs)
	}
	if got := testing.AllocsPerRun(1000, run); got != 0 {
		t.Fatalf("steady hot allocations: %v", got)
	}
	if bad {
		t.Fatal("allocation workload failed")
	}
}
