//go:build 386 || arm || wasm || mips || mipsle
// +build 386 arm wasm mips mipsle

package framer

import "testing"

func TestOversizeGuardContract_32bit(t *testing.T) {
	// On 32-bit architectures, the caller cannot construct a []byte with len(p) > MaxInt.
	// Therefore, the wire-format 56-bit maximum payload length guard cannot be
	// meaningfully exercised via write-path oversize slices on these platforms.
	maxInt := int(^uint(0) >> 1)
	if maxInt <= 0 {
		t.Fatalf("maxInt=%d; expected positive", maxInt)
	}
	if int64(maxInt) >= framePayloadMaxLen56 {
		t.Fatalf("maxInt=%d; expected maxInt < framePayloadMaxLen56=%d", maxInt, int64(framePayloadMaxLen56))
	}
	// Sanity: on 32-bit targets, MaxInt must be 2^31-1.
	if maxInt != int(^uint32(0)>>1) {
		t.Fatalf("maxInt=%d; expected 2^31-1=%d", maxInt, int(^uint32(0)>>1))
	}
}
