//go:build s390x || ppc64 || mips || mips64

// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package bo

import "encoding/binary"

// Native returns the native byte order for common big-endian Go ports.
func Native() binary.ByteOrder { return binary.BigEndian }
