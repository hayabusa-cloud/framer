//go:build !amd64 && !arm64 && !386 && !riscv64 && !ppc64le && !mips64le && !mipsle && !loong64 && !wasm && !arm && !s390x && !ppc64 && !mips && !mips64

// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package bo

import (
	"encoding/binary"
	"unsafe"
)

// detectNative determines the machine's byte order once at init time.
func detectNative() binary.ByteOrder {
	var x uint16 = 0x0102
	b := *(*[2]byte)(unsafe.Pointer(&x))
	if b[0] == 0x01 {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

var native = detectNative()

// Native returns the machine's native byte order on otherwise-unsupported ports.
func Native() binary.ByteOrder { return native }
