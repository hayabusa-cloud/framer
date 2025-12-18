// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
	"unsafe"

	"code.hybscloud.com/framer"
	fr "code.hybscloud.com/framer"
)

// --- Core Framing Tests ---

func TestStreamRoundTrip_BigEndian(t *testing.T) {
	var raw bytes.Buffer
	w := framer.NewWriter(&raw, framer.WithByteOrder(binary.BigEndian), framer.WithProtocol(framer.BinaryStream))
	r := framer.NewReader(&raw, framer.WithByteOrder(binary.BigEndian), framer.WithProtocol(framer.BinaryStream))

	msgs := [][]byte{
		{},
		[]byte("hello"),
		bytes.Repeat([]byte{'a'}, 253),
		bytes.Repeat([]byte{'b'}, 254),
		bytes.Repeat([]byte{'c'}, 4096),
	}

	for i, m := range msgs {
		n, err := w.Write(m)
		if err != nil {
			t.Fatalf("write[%d]: %v", i, err)
		}
		if n != len(m) {
			t.Fatalf("write[%d]: n=%d want=%d", i, n, len(m))
		}
	}

	for i, m := range msgs {
		buf := make([]byte, len(m))
		n, err := r.Read(buf)
		if err != nil {
			t.Fatalf("read[%d]: %v", i, err)
		}
		if n != len(m) {
			t.Fatalf("read[%d]: n=%d want=%d", i, n, len(m))
		}
		if !bytes.Equal(buf, m) {
			t.Fatalf("read[%d]: payload mismatch", i)
		}
	}
}

// --- Tests from options_test.go ---

func TestHelpers_SetExpectedReadWriteAndByteOrder(t *testing.T) {
	// Read TCP
	var o fr.Options
	fr.WithReadTCP()(&o)
	if o.ReadProto != fr.BinaryStream {
		t.Fatalf("ReadProto want BinaryStream, got %v", o.ReadProto)
	}
	if o.ReadByteOrder != binary.BigEndian {
		t.Fatalf("ReadByteOrder want BigEndian")
	}
	// Write UDP
	fr.WithWriteUDP()(&o)
	if o.WriteProto != fr.Datagram {
		t.Fatalf("WriteProto want Datagram, got %v", o.WriteProto)
	}
	if o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("WriteByteOrder want BigEndian")
	}
	// Unrelated fields should remain untouched by helpers
	if o.ReadLimit != 0 {
		t.Fatalf("ReadLimit changed: %d", o.ReadLimit)
	}
}

func TestHelpers_ComposeCleanly(t *testing.T) {
	var o fr.Options
	fr.WithReadTCP()(&o)
	fr.WithWriteUDP()(&o)
	if o.ReadProto != fr.BinaryStream || o.WriteProto != fr.Datagram {
		t.Fatalf("compose mismatch: read=%v write=%v", o.ReadProto, o.WriteProto)
	}
	if o.ReadByteOrder != binary.BigEndian || o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("byte order mismatch: read=%T write=%T", o.ReadByteOrder, o.WriteByteOrder)
	}
	// Now switch write side to TCP and verify read side remains unchanged.
	fr.WithWriteTCP()(&o)
	if o.ReadProto != fr.BinaryStream {
		t.Fatalf("read side changed unexpectedly: %v", o.ReadProto)
	}
	if o.WriteProto != fr.BinaryStream {
		t.Fatalf("write side not updated: %v", o.WriteProto)
	}
}

func TestSmoke_TcpRoundTrip(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	w := fr.NewWriter(c1, fr.WithWriteTCP())
	r := fr.NewReader(c2, fr.WithReadTCP())
	msg := []byte("hello, framer")
	done := make(chan struct{})
	go func() {
		n, err := w.Write(msg)
		if err != nil {
			t.Errorf("write error: %v", err)
		}
		if n != len(msg) {
			t.Errorf("short write: %d/%d", n, len(msg))
		}
		close(done)
	}()
	buf := make([]byte, 64)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	<-done
	got := string(buf[:n])
	if got != string(msg) {
		t.Fatalf("roundtrip mismatch: got %q want %q", got, string(msg))
	}
}

func TestSmoke_UdpPassThrough(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	w := fr.NewWriter(c1, fr.WithWriteUDP())
	r := fr.NewReader(c2, fr.WithReadUDP())
	msg := []byte("datagram payload")
	done := make(chan struct{})
	go func() {
		n, err := w.Write(msg)
		if err != nil {
			t.Errorf("write error: %v", err)
		}
		if n != len(msg) {
			t.Errorf("short write: %d/%d", n, len(msg))
		}
		close(done)
	}()
	buf := make([]byte, 64)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	<-done
	got := string(buf[:n])
	if got != string(msg) {
		t.Fatalf("pass-through mismatch: got %q want %q", got, string(msg))
	}
}

func TestFastPathInterfacesImplemented(t *testing.T) {
	r, w := fr.NewPipe()
	if _, ok := r.(io.WriterTo); !ok {
		t.Fatalf("Reader should implement io.WriterTo for fast path")
	}
	if _, ok := w.(io.ReaderFrom); !ok {
		t.Fatalf("Writer should implement io.ReaderFrom for fast path")
	}
}

func detectNative() binary.ByteOrder {
	var x uint16 = 0x1
	b := (*[2]byte)(unsafe.Pointer(&x))
	if b[0] == 0x1 {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

func TestLocalHelpersUseNativeEndianness(t *testing.T) {
	// Read side
	var o fr.Options
	fr.WithReadLocal()(&o)
	if o.ReadProto != fr.BinaryStream {
		t.Fatalf("ReadProto want BinaryStream, got %v", o.ReadProto)
	}
	if o.ReadByteOrder != detectNative() {
		t.Fatalf("ReadByteOrder want native endianness")
	}
	// Write side
	fr.WithWriteLocal()(&o)
	if o.WriteProto != fr.BinaryStream {
		t.Fatalf("WriteProto want BinaryStream, got %v", o.WriteProto)
	}
	if o.WriteByteOrder != detectNative() {
		t.Fatalf("WriteByteOrder want native endianness")
	}
}

func TestOptions_Setters(t *testing.T) {
	var o fr.Options

	fr.WithReadByteOrder(binary.LittleEndian)(&o)
	if o.ReadByteOrder != binary.LittleEndian {
		t.Fatalf("ReadByteOrder not set")
	}
	fr.WithWriteByteOrder(binary.LittleEndian)(&o)
	if o.WriteByteOrder != binary.LittleEndian {
		t.Fatalf("WriteByteOrder not set")
	}

	fr.WithReadProtocol(fr.SeqPacket)(&o)
	if o.ReadProto != fr.SeqPacket {
		t.Fatalf("ReadProto not set")
	}
	fr.WithWriteProtocol(fr.Datagram)(&o)
	if o.WriteProto != fr.Datagram {
		t.Fatalf("WriteProto not set")
	}

	fr.WithReadLimit(123)(&o)
	if o.ReadLimit != 123 {
		t.Fatalf("ReadLimit not set")
	}

	fr.WithRetryDelay(99 * time.Microsecond)(&o)
	if o.RetryDelay != 99*time.Microsecond {
		t.Fatalf("RetryDelay not set")
	}

	fr.WithBlock()(&o)
	if o.RetryDelay != 0 {
		t.Fatalf("WithBlock not applied")
	}
	fr.WithNonblock()(&o)
	if o.RetryDelay >= 0 {
		t.Fatalf("WithNonblock not applied")
	}
}

func TestNetOpts_AllHelpers(t *testing.T) {
	var o fr.Options

	fr.WithReadWebSocket()(&o)
	if o.ReadProto != fr.SeqPacket || o.ReadByteOrder != binary.BigEndian {
		t.Fatalf("ReadWebSocket mismatch")
	}

	fr.WithWriteWebSocket()(&o)
	if o.WriteProto != fr.SeqPacket || o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("WriteWebSocket mismatch")
	}

	fr.WithReadSCTP()(&o)
	if o.ReadProto != fr.SeqPacket || o.ReadByteOrder != binary.BigEndian {
		t.Fatalf("ReadSCTP mismatch")
	}

	fr.WithWriteSCTP()(&o)
	if o.WriteProto != fr.SeqPacket || o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("WriteSCTP mismatch")
	}

	fr.WithReadUnix()(&o)
	if o.ReadProto != fr.BinaryStream || o.ReadByteOrder != binary.BigEndian {
		t.Fatalf("ReadUnix mismatch")
	}

	fr.WithWriteUnix()(&o)
	if o.WriteProto != fr.BinaryStream || o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("WriteUnix mismatch")
	}

	fr.WithReadUnixPacket()(&o)
	if o.ReadProto != fr.Datagram || o.ReadByteOrder != binary.BigEndian {
		t.Fatalf("ReadUnixPacket mismatch")
	}

	fr.WithWriteUnixPacket()(&o)
	if o.WriteProto != fr.Datagram || o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("WriteUnixPacket mismatch")
	}

	// Local (native endianness)
	fr.WithReadLocal()(&o)
	if o.ReadProto != fr.BinaryStream || o.ReadByteOrder != detectNative() {
		t.Fatalf("ReadLocal mismatch")
	}

	fr.WithWriteLocal()(&o)
	if o.WriteProto != fr.BinaryStream || o.WriteByteOrder != detectNative() {
		t.Fatalf("WriteLocal mismatch")
	}
}
