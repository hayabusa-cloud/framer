// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer

import (
	"encoding/binary"
	"time"
)

// Protocol describes the expected message-boundary behavior of the underlying transport.
//
// The framer logic adapts its algorithm based on this setting:
//   - BinaryStream: boundaries are not preserved (e.g., TCP). Framer adds a length prefix.
//   - SeqPacket / Datagram: boundaries are preserved. Framer is pass-through.
type Protocol uint8

const (
	BinaryStream Protocol = 1
	SeqPacket    Protocol = 2
	Datagram     Protocol = 3
)

func (p Protocol) preserveBoundary() bool {
	switch p {
	case SeqPacket, Datagram:
		return true
	default:
		return false
	}
}

// Options configures framing behavior.
type Options struct {
	ReadByteOrder  binary.ByteOrder
	WriteByteOrder binary.ByteOrder
	ReadProto      Protocol
	WriteProto     Protocol

	// ReadLimit caps the maximum allowed payload size (bytes). Zero means no limit.
	ReadLimit int

	// RetryDelay controls how the framer handles iox.ErrWouldBlock from the underlying transport:
	//   - negative: nonblock, return ErrWouldBlock immediately
	//   - zero: yield (runtime.Gosched) and retry
	//   - positive: sleep for the duration and retry
	RetryDelay time.Duration
}

var defaultOptions = Options{
	ReadByteOrder:  binary.BigEndian,
	WriteByteOrder: binary.BigEndian,
	ReadProto:      BinaryStream,
	WriteProto:     BinaryStream,
	ReadLimit:      0,
	RetryDelay:     -1, // default: nonblock
}

type Option func(*Options)

func WithByteOrder(order binary.ByteOrder) Option {
	return func(o *Options) {
		o.ReadByteOrder = order
		o.WriteByteOrder = order
	}
}

func WithReadByteOrder(order binary.ByteOrder) Option {
	return func(o *Options) { o.ReadByteOrder = order }
}

func WithWriteByteOrder(order binary.ByteOrder) Option {
	return func(o *Options) { o.WriteByteOrder = order }
}

func WithProtocol(proto Protocol) Option {
	return func(o *Options) {
		o.ReadProto = proto
		o.WriteProto = proto
	}
}

func WithReadProtocol(proto Protocol) Option {
	return func(o *Options) { o.ReadProto = proto }
}

func WithWriteProtocol(proto Protocol) Option {
	return func(o *Options) { o.WriteProto = proto }
}

func WithReadLimit(limit int) Option {
	return func(o *Options) { o.ReadLimit = limit }
}

// WithRetryDelay sets the retry/wait policy used when the underlying transport returns iox.ErrWouldBlock.
func WithRetryDelay(d time.Duration) Option {
	return func(o *Options) { o.RetryDelay = d }
}

// WithBlock enables cooperative blocking (yield-and-retry) on iox.ErrWouldBlock.
func WithBlock() Option {
	return func(o *Options) { o.RetryDelay = 0 }
}

// WithNonblock forces non-blocking behavior (return iox.ErrWouldBlock immediately).
func WithNonblock() Option {
	return func(o *Options) { o.RetryDelay = -1 }
}
