package framer

import (
	"bytes"
	"io"
	"testing"
)

func TestForwarder_Packet_ReadLimitLessThanInternalBuffer_AdjustsMax(t *testing.T) {
	// Cover:
	//   if f.rr.readLimit > 0 && int64(max) > f.rr.readLimit { max = int(f.rr.readLimit) }
	// This is not reachable via public construction because NewForwarder sizes buf by ReadLimit;
	// so we enlarge f.buf after construction.
	f := NewForwarder(io.Discard, bytes.NewReader([]byte("abcd")), WithProtocol(SeqPacket), WithReadLimit(8))
	f.buf = make([]byte, 64)

	if _, err := f.ForwardOnce(); err != nil {
		t.Fatalf("err=%v", err)
	}
}

func TestForwarder_DefensiveInvalidState_ReturnsZeroNil(t *testing.T) {
	// Cover the defensive tail:
	//   return 0, nil
	f := NewForwarder(io.Discard, bytes.NewReader(nil), WithProtocol(SeqPacket))
	f.state = 3

	n, err := f.ForwardOnce()
	if n != 0 || err != nil {
		t.Fatalf("want (0, nil), got (%d, %v)", n, err)
	}
}

func TestForwarder_Stream_DefensiveEOFInPayloadPhase_ReturnsUnexpectedEOF(t *testing.T) {
	// Cover the defensive branch in ForwardOnce stream phase:
	//   if re == io.EOF { return f.got, io.ErrUnexpectedEOF }
	//
	// Under normal operation, readStream converts premature EOF to io.ErrUnexpectedEOF.
	// This branch is reachable only if the internal framer state is inconsistent.
	f := NewForwarder(io.Discard, bytes.NewReader(nil), WithProtocol(BinaryStream))
	f.state = 1
	f.need = 8
	f.got = 3
	// Corrupt the read-side state so rr.read observes io.EOF while "reading payload".
	f.rr.offset = 0

	n, err := f.ForwardOnce()
	if n != f.got || err != io.ErrUnexpectedEOF {
		t.Fatalf("want (%d, io.ErrUnexpectedEOF), got (%d, %v)", f.got, n, err)
	}
}
