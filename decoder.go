// ©Hayabusa Cloud Co., Ltd. 2026. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer

import (
	"encoding/binary"
	"io"
)

// Chunk describes part of one BinaryStream message. Data is a read-only view
// into the input supplied to Decoder.Decode, with capacity equal to its length.
// Caller must keep that backing valid while using Data. The decoder neither
// owns nor retains it; subsequent Decode, Finish, and Reset calls do not revoke it.
type Chunk struct {
	Data   []byte // Payload bytes only; no framing header.
	Length uint64 // Full declared payload length when a chunk or boundary is emitted.
	Begin  bool   // The validated length header was completed in this call.
	End    bool   // The message completed in this call, including an empty one.
}

// Decoder incrementally decodes BinaryStream framing from borrowed input.
// Its zero value uses big endian and the wire limit of 2^56-1 bytes. It keeps
// fixed parser state and at most eight header bytes, never input slices or
// payload storage. It performs no I/O, waiting, or payload copies.
//
// Caller must serialize access. A live Decoder must not be copied to resume
// the same stream independently.
type Decoder struct {
	header    [MaxHeaderSize]byte
	length    uint64
	remaining uint64
	limit     uint64
	read      uint8
	width     uint8
	little    bool
	terminal  uint8
}

const (
	decoderEnded = iota + 1
	decoderTooLong
	decoderTruncated
)

// NewDecoder returns a value configured for order and limit. A nil order
// selects big endian; the three standard encoding/binary byte orders are
// supported. Zero limit selects the wire maximum. An unsupported order or a
// limit above 2^56-1 returns ErrInvalidArgument.
func NewDecoder(order binary.ByteOrder, limit uint64) (Decoder, error) {
	little, valid := codecByteOrder(order)
	if !valid || limit > framePayloadMaxLen56 {
		return Decoder{}, ErrInvalidArgument
	}
	return Decoder{little: little, limit: limit}, nil
}

// Decode consumes part of src and returns at most one borrowed payload chunk,
// without crossing the current message's end. Consumed counts header and payload
// bytes. Always process the returned chunk and advance by consumed before
// considering err; never replay consumed bytes.
//
// A nil error completes one message (End is true). ErrMore reports positive
// progress in an incomplete message, including a partial header with no Chunk.
// It does not promise more input is available. Empty input on a live decoder
// returns ErrWouldBlock with zero progress and unchanged state. Length rejection
// returns ErrTooLong after consuming the header, without emitting Begin or Data.
// Failures persist until Reset; after successful Finish, Decode returns io.EOF.
//
// Begin is emitted once when the header passes the limit check, possibly before
// any payload arrives. An empty message emits Begin and End with empty Data.
// No event is emitted when both flags are false and Data is empty. Caller must
// have capacity to handle the result before calling Decode: progress is committed
// on return. Bound per-turn work by limiting src and the number of calls.
func (d *Decoder) Decode(src []byte) (consumed int, chunk Chunk, err error) {
	if d.terminal != 0 {
		return 0, Chunk{}, d.terminalError()
	}
	if len(src) == 0 {
		return 0, Chunk{}, ErrWouldBlock
	}
	if d.remaining == 0 {
		if d.read == 0 {
			d.header[0] = src[0]
			d.read, d.width, consumed = 1, 1, 1
			switch src[0] {
			case 0xfe:
				d.width = 3
			case 0xff:
				d.width = MaxHeaderSize
			}
		}
		n := copy(d.header[d.read:d.width], src[consumed:])
		d.read += uint8(n)
		consumed += n
		if d.read != d.width {
			return consumed, Chunk{}, ErrMore
		}
		switch d.width {
		case 1:
			d.length = uint64(d.header[0])
		case 3:
			if d.little {
				d.length = uint64(binary.LittleEndian.Uint16(d.header[1:3]))
			} else {
				d.length = uint64(binary.BigEndian.Uint16(d.header[1:3]))
			}
		default:
			if d.little {
				d.length = binary.LittleEndian.Uint64(d.header[:]) >> 8
			} else {
				d.length = binary.BigEndian.Uint64(d.header[:]) & framePayloadMaxLen56
			}
		}
		if d.limit != 0 && d.length > d.limit {
			d.terminal = decoderTooLong
			return consumed, Chunk{}, ErrTooLong
		}
		d.read, d.width = 0, 0
		d.remaining = d.length
		chunk.Begin = true
	}
	chunk.Length = d.length
	n := int(min(d.remaining, uint64(len(src)-consumed)))
	chunk.Data = src[consumed : consumed+n : consumed+n]
	consumed += n
	d.remaining -= uint64(n)
	if d.remaining != 0 {
		return consumed, chunk, ErrMore
	}
	chunk.End = true
	return consumed, chunk, nil
}

// Finish declares input end after the caller has processed all supplied bytes.
// It returns nil at a message boundary, or io.ErrUnexpectedEOF for an unfinished
// header or body. Repeated calls preserve the result, including an earlier
// length failure. Earlier chunks are not rolled back by a truncated message.
func (d *Decoder) Finish() error {
	if d.terminal == decoderEnded {
		return nil
	}
	if d.terminal != 0 {
		return d.terminalError()
	}
	if d.read != 0 || d.remaining != 0 {
		d.terminal = decoderTruncated
		return io.ErrUnexpectedEOF
	}
	d.terminal = decoderEnded
	return nil
}

// Reset clears parser and terminal state while preserving configuration.
// Caller must start a new stream or independently establish a frame boundary;
// Reset does not resynchronize arbitrary bytes or release caller-owned storage.
func (d *Decoder) Reset() {
	*d = Decoder{little: d.little, limit: d.limit}
}

func (d *Decoder) terminalError() error {
	switch d.terminal {
	case decoderEnded:
		return io.EOF
	case decoderTooLong:
		return ErrTooLong
	default:
		return io.ErrUnexpectedEOF
	}
}
