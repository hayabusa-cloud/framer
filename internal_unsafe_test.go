//go:build (amd64 || arm64 || ppc64le || ppc64 || s390x || riscv64 || loong64 || mips64le || mips64) && !race
// +build amd64 arm64 ppc64le ppc64 s390x riscv64 loong64 mips64le mips64
// +build !race

// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer

import (
	"errors"
	"testing"
	"unsafe"
)

// fabricateOversizedSlice returns a []byte whose len is greater than
// framePayloadMaxLen56 without allocating memory for it.
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
