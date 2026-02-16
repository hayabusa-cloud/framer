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
	var o framer.Options
	framer.WithReadTCP()(&o)
	if o.ReadProto != framer.BinaryStream {
		t.Fatalf("ReadProto want BinaryStream, got %v", o.ReadProto)
	}
	if o.ReadByteOrder != binary.BigEndian {
		t.Fatalf("ReadByteOrder want BigEndian")
	}
	// Write UDP
	framer.WithWriteUDP()(&o)
	if o.WriteProto != framer.Datagram {
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
	var o framer.Options
	framer.WithReadTCP()(&o)
	framer.WithWriteUDP()(&o)
	if o.ReadProto != framer.BinaryStream || o.WriteProto != framer.Datagram {
		t.Fatalf("compose mismatch: read=%v write=%v", o.ReadProto, o.WriteProto)
	}
	if o.ReadByteOrder != binary.BigEndian || o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("byte order mismatch: read=%T write=%T", o.ReadByteOrder, o.WriteByteOrder)
	}
	// Now switch write side to TCP and verify read side remains unchanged.
	framer.WithWriteTCP()(&o)
	if o.ReadProto != framer.BinaryStream {
		t.Fatalf("read side changed unexpectedly: %v", o.ReadProto)
	}
	if o.WriteProto != framer.BinaryStream {
		t.Fatalf("write side not updated: %v", o.WriteProto)
	}
}

func TestSmoke_TcpRoundTrip(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	w := framer.NewWriter(c1, framer.WithWriteTCP())
	r := framer.NewReader(c2, framer.WithReadTCP())
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
	w := framer.NewWriter(c1, framer.WithWriteUDP())
	r := framer.NewReader(c2, framer.WithReadUDP())
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
	r, w := framer.NewPipe()
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
	var o framer.Options
	framer.WithReadLocal()(&o)
	if o.ReadProto != framer.BinaryStream {
		t.Fatalf("ReadProto want BinaryStream, got %v", o.ReadProto)
	}
	if o.ReadByteOrder != detectNative() {
		t.Fatalf("ReadByteOrder want native endianness")
	}
	// Write side
	framer.WithWriteLocal()(&o)
	if o.WriteProto != framer.BinaryStream {
		t.Fatalf("WriteProto want BinaryStream, got %v", o.WriteProto)
	}
	if o.WriteByteOrder != detectNative() {
		t.Fatalf("WriteByteOrder want native endianness")
	}
}

func TestOptions_Setters(t *testing.T) {
	var o framer.Options

	framer.WithReadByteOrder(binary.LittleEndian)(&o)
	if o.ReadByteOrder != binary.LittleEndian {
		t.Fatalf("ReadByteOrder not set")
	}
	framer.WithWriteByteOrder(binary.LittleEndian)(&o)
	if o.WriteByteOrder != binary.LittleEndian {
		t.Fatalf("WriteByteOrder not set")
	}

	framer.WithReadProtocol(framer.SeqPacket)(&o)
	if o.ReadProto != framer.SeqPacket {
		t.Fatalf("ReadProto not set")
	}
	framer.WithWriteProtocol(framer.Datagram)(&o)
	if o.WriteProto != framer.Datagram {
		t.Fatalf("WriteProto not set")
	}

	framer.WithReadLimit(123)(&o)
	if o.ReadLimit != 123 {
		t.Fatalf("ReadLimit not set")
	}

	framer.WithRetryDelay(99 * time.Microsecond)(&o)
	if o.RetryDelay != 99*time.Microsecond {
		t.Fatalf("RetryDelay not set")
	}

	framer.WithBlock()(&o)
	if o.RetryDelay != 0 {
		t.Fatalf("WithBlock not applied")
	}
	framer.WithNonblock()(&o)
	if o.RetryDelay >= 0 {
		t.Fatalf("WithNonblock not applied")
	}
}

func TestNetOpts_AllHelpers(t *testing.T) {
	var o framer.Options

	framer.WithReadWebSocket()(&o)
	if o.ReadProto != framer.SeqPacket || o.ReadByteOrder != binary.BigEndian {
		t.Fatalf("ReadWebSocket mismatch")
	}

	framer.WithWriteWebSocket()(&o)
	if o.WriteProto != framer.SeqPacket || o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("WriteWebSocket mismatch")
	}

	framer.WithReadSCTP()(&o)
	if o.ReadProto != framer.SeqPacket || o.ReadByteOrder != binary.BigEndian {
		t.Fatalf("ReadSCTP mismatch")
	}

	framer.WithWriteSCTP()(&o)
	if o.WriteProto != framer.SeqPacket || o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("WriteSCTP mismatch")
	}

	framer.WithReadUnix()(&o)
	if o.ReadProto != framer.BinaryStream || o.ReadByteOrder != binary.BigEndian {
		t.Fatalf("ReadUnix mismatch")
	}

	framer.WithWriteUnix()(&o)
	if o.WriteProto != framer.BinaryStream || o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("WriteUnix mismatch")
	}

	framer.WithReadUnixPacket()(&o)
	if o.ReadProto != framer.Datagram || o.ReadByteOrder != binary.BigEndian {
		t.Fatalf("ReadUnixPacket mismatch")
	}

	framer.WithWriteUnixPacket()(&o)
	if o.WriteProto != framer.Datagram || o.WriteByteOrder != binary.BigEndian {
		t.Fatalf("WriteUnixPacket mismatch")
	}

	// Local (native endianness)
	framer.WithReadLocal()(&o)
	if o.ReadProto != framer.BinaryStream || o.ReadByteOrder != detectNative() {
		t.Fatalf("ReadLocal mismatch")
	}

	framer.WithWriteLocal()(&o)
	if o.WriteProto != framer.BinaryStream || o.WriteByteOrder != detectNative() {
		t.Fatalf("WriteLocal mismatch")
	}
}
