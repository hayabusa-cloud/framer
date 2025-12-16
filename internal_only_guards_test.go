//go:build amd64 || arm64 || ppc64le || ppc64 || s390x || riscv64 || loong64 || mips64le || mips64
// +build amd64 arm64 ppc64le ppc64 s390x riscv64 loong64 mips64le mips64

package framer

import (
	"errors"
	"testing"
	"unsafe"
)

// TestYieldOnce just executes the cooperative yield helper.
func TestYieldOnce(t *testing.T) {
	fr := newFramer(nil, nil)
	fr.yieldOnce()
}

// fabricateOversizedSlice returns a []byte whose len is greater than framePayloadMaxLen56
// without allocating memory for it.
//
// This is safe here because the tested code only inspects len(p) and returns
// before dereferencing the slice data.
func fabricateOversizedSlice() []byte {
	var dummy byte
	huge := int(framePayloadMaxLen56 + 1)
	return unsafe.Slice((*byte)(unsafe.Pointer(&dummy)), huge)
}

func TestWriteStream_GuardTooLongUnsafe(t *testing.T) {
	fr := newFramer(nil, nil)
	p := fabricateOversizedSlice()
	if _, err := fr.writeStream(p); !errors.Is(err, ErrTooLong) {
		t.Fatalf("err=%v want ErrTooLong", err)
	}
}

func TestWritePacket_GuardTooLongUnsafe(t *testing.T) {
	fr := newFramer(nil, nil)
	p := fabricateOversizedSlice()
	if _, err := fr.writePacket(p); !errors.Is(err, ErrTooLong) {
		t.Fatalf("err=%v want ErrTooLong", err)
	}
}

func TestReadStream_LengthGuardViaState(t *testing.T) {
	fr := newFramer(nil, nil)
	// Emulate "header and ext already consumed, do not parse length from header".
	fr.header[0] = 0               // exLen = 0
	fr.offset = frameHeaderLen + 1 // skip the parse-length block
	fr.length = framePayloadMaxLen56 + 1
	buf := make([]byte, 1)
	if _, err := fr.readStream(buf); !errors.Is(err, ErrTooLong) {
		t.Fatalf("err=%v want ErrTooLong", err)
	}
}
