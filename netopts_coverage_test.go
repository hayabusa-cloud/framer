package framer_test

import (
	"encoding/binary"
	"testing"

	fr "code.hybscloud.com/framer"
)

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

	// Local (native endianness) — detect using helper from options_test.go
	fr.WithReadLocal()(&o)
	if o.ReadProto != fr.BinaryStream || o.ReadByteOrder != detectNative() {
		t.Fatalf("ReadLocal mismatch")
	}

	fr.WithWriteLocal()(&o)
	if o.WriteProto != fr.BinaryStream || o.WriteByteOrder != detectNative() {
		t.Fatalf("WriteLocal mismatch")
	}
}
