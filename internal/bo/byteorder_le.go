//go:build amd64 || arm64 || 386 || riscv64 || ppc64le || mips64le || mipsle || loong64 || wasm || arm

// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package bo

import "encoding/binary"

// Native returns the native byte order for common little-endian Go ports.
func Native() binary.ByteOrder { return binary.LittleEndian }
