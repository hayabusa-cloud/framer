// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer

import (
	"encoding/binary"
	"time"
)

// Protocol describes the message-boundary behavior expected from the underlying
// transport.
//
// The framer logic adapts its algorithm based on this setting:
//   - BinaryStream: boundaries are not preserved, for example TCP; framer adds a
//     length prefix.
//   - SeqPacket and Datagram: boundaries are preserved; framer is pass-through.
type Protocol uint8

const (
	// BinaryStream selects length-prefixed framing for byte-stream transports.
	BinaryStream Protocol = 1

	// SeqPacket selects pass-through framing for sequenced-packet transports.
	SeqPacket Protocol = 2

	// Datagram selects pass-through framing for datagram transports.
	Datagram Protocol = 3
)

func (p Protocol) preserveBoundary() bool {
	switch p {
	case SeqPacket, Datagram:
		return true
	default:
		return false
	}
}

// Options configures framing behavior used by constructors.
//
// Constructors start from package defaults before applying Option values.
// Callers normally pass Option helpers rather than constructing Options
// directly; the zero Options value is not a complete configuration because byte
// order fields may be nil.
type Options struct {
	// ReadByteOrder controls BinaryStream length-prefix decoding.
	ReadByteOrder binary.ByteOrder

	// WriteByteOrder controls BinaryStream length-prefix encoding.
	WriteByteOrder binary.ByteOrder

	// ReadProto selects the reader-side protocol mode.
	ReadProto Protocol

	// WriteProto selects the writer-side protocol mode.
	WriteProto Protocol

	// ReadLimit caps the maximum accepted payload size in bytes.
	// A non-positive value means no caller-configured read limit.
	ReadLimit int

	// RetryDelay controls how the framer handles zero-progress ErrWouldBlock
	// from the underlying transport:
	//   - negative: nonblock, return ErrWouldBlock immediately
	//   - zero: yield (runtime.Gosched) and retry
	//   - positive: sleep for the duration and retry
	//
	// If the underlying read or write transfers bytes before returning
	// ErrWouldBlock, the operation returns that positive count with ErrWouldBlock
	// so callers can process progress before retrying.
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

// Option configures Reader, Writer, ReadWriter, and Forwarder construction.
//
// Later options override earlier settings for the same field.
type Option func(*Options)

// WithByteOrder sets both read and write byte order for BinaryStream length prefixes.
func WithByteOrder(order binary.ByteOrder) Option {
	return func(o *Options) {
		o.ReadByteOrder = order
		o.WriteByteOrder = order
	}
}

// WithReadByteOrder sets the read-side byte order for BinaryStream length prefixes.
func WithReadByteOrder(order binary.ByteOrder) Option {
	return func(o *Options) { o.ReadByteOrder = order }
}

// WithWriteByteOrder sets the write-side byte order for BinaryStream length prefixes.
func WithWriteByteOrder(order binary.ByteOrder) Option {
	return func(o *Options) { o.WriteByteOrder = order }
}

// WithProtocol sets both read and write protocol modes.
func WithProtocol(proto Protocol) Option {
	return func(o *Options) {
		o.ReadProto = proto
		o.WriteProto = proto
	}
}

// WithReadProtocol sets the reader-side protocol mode.
func WithReadProtocol(proto Protocol) Option {
	return func(o *Options) { o.ReadProto = proto }
}

// WithWriteProtocol sets the writer-side protocol mode.
func WithWriteProtocol(proto Protocol) Option {
	return func(o *Options) { o.WriteProto = proto }
}

// WithReadLimit sets the maximum accepted payload size on the read side.
//
// In SeqPacket and Datagram modes, the limit is checked after a packet read, so
// an oversized packet may return (n > limit, ErrTooLong). A non-positive limit
// means no caller-configured limit; transfer helpers may still apply their
// documented internal buffer cap.
func WithReadLimit(limit int) Option {
	return func(o *Options) { o.ReadLimit = limit }
}

// WithRetryDelay sets the wait policy used when the underlying transport returns
// ErrWouldBlock without transferring bytes.
//
// A negative duration returns ErrWouldBlock immediately. A zero duration yields
// and retries. A positive duration sleeps for d before retrying. Positive-progress
// ErrWouldBlock is returned to the caller with its byte count.
func WithRetryDelay(d time.Duration) Option {
	return func(o *Options) { o.RetryDelay = d }
}

// WithBlock enables cooperative blocking by yielding and retrying on
// zero-progress ErrWouldBlock.
func WithBlock() Option {
	return func(o *Options) { o.RetryDelay = 0 }
}

// WithNonblock forces non-blocking behavior by returning zero-progress
// ErrWouldBlock immediately.
func WithNonblock() Option {
	return func(o *Options) { o.RetryDelay = -1 }
}
