package framer

import (
	"encoding/binary"
	"testing"
)

func TestDefaultsFor_DefaultBranch(t *testing.T) {
	p, bo := defaultsFor(netKind(255))
	if p != BinaryStream || bo != binary.BigEndian {
		t.Fatalf("unexpected defaults: p=%v bo=%T", p, bo)
	}
}
