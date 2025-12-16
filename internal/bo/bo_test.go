package bo

import (
	"encoding/binary"
	"testing"
)

func TestNativeReturnsValidByteOrder(t *testing.T) {
	b := Native()
	if b != binary.BigEndian && b != binary.LittleEndian {
		t.Fatalf("unexpected byte order: %T", b)
	}
}
