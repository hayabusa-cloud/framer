package framer_test

import (
	"encoding/binary"
	"testing"
	"time"

	fr "code.hybscloud.com/framer"
)

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
