// ©Hayabusa Cloud Co., Ltd. 2026. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer

import (
	"encoding/binary"
	"io"
)

// MaxHeaderSize is the maximum BinaryStream length-prefix size in bytes.
const MaxHeaderSize = 8

// EncodeHeader writes the shortest BinaryStream header for length into dst.
// It returns the number of header bytes written; it does not write payload.
// A nil order selects big endian. Only binary.BigEndian, binary.LittleEndian,
// and binary.NativeEndian are supported; other orders return ErrInvalidArgument.
// Lengths above 2^56-1 return ErrTooLong. An insufficient dst returns
// io.ErrShortBuffer. On error, no bytes are written. Success leaves the suffix
// after the header unchanged.
//
// EncodeHeader does not retain dst. Caller must keep header storage valid until
// any asynchronous I/O using it has released that storage.
func EncodeHeader(dst []byte, length uint64, order binary.ByteOrder) (int, error) {
	little, valid := codecByteOrder(order)
	if !valid {
		return 0, ErrInvalidArgument
	}
	if length > framePayloadMaxLen56 {
		return 0, ErrTooLong
	}
	size := 1
	if length > framePayloadMaxLen16 {
		size = MaxHeaderSize
	} else if length > framePayloadMaxLen8Bits {
		size = 3
	}
	if len(dst) < size {
		return 0, io.ErrShortBuffer
	}
	switch size {
	case 1:
		dst[0] = byte(length)
	case 3:
		dst[0] = 0xfe
		if little {
			binary.LittleEndian.PutUint16(dst[1:3], uint16(length))
		} else {
			binary.BigEndian.PutUint16(dst[1:3], uint16(length))
		}
	default:
		if little {
			binary.LittleEndian.PutUint64(dst[:MaxHeaderSize], length<<8)
		} else {
			binary.BigEndian.PutUint64(dst[:MaxHeaderSize], length)
		}
		dst[0] = 0xff
	}
	return size, nil
}

func codecByteOrder(order binary.ByteOrder) (little, valid bool) {
	switch order {
	case nil, binary.BigEndian:
		return false, true
	case binary.LittleEndian:
		return true, true
	case binary.NativeEndian:
		return binary.NativeEndian.Uint16([]byte{1, 0}) == 1, true
	default:
		return false, false
	}
}
