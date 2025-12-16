// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer

import (
	"encoding/binary"

	"code.hybscloud.com/framer/internal/bo"
)

// Network option helpers and mapping.
//
// Single source of truth — transport → (Protocol, ByteOrder):
//   - TCP         → BinaryStream, BigEndian (network byte order)
//   - UDP         → Datagram,     BigEndian
//   - WebSocket   → SeqPacket,    BigEndian  // boundaries preserved; pass-through
//   - SCTP        → SeqPacket,    BigEndian  // boundaries preserved
//   - Unix (stream)     → BinaryStream, BigEndian
//   - UnixPacket  → Datagram,     BigEndian
//   - Local (stream)    → BinaryStream, native byte order
//
// Byte-order policy:
//   - Network-named helpers (TCP/UDP/WebSocket/SCTP/Unix/UnixPacket) use BigEndian.
//   - Local helpers use native byte order (multi-arch friendly).

type netKind uint8

const (
	netTCP netKind = iota
	netUDP
	netWebSocket
	netSCTP
	netUnixStream
	netUnixPacket
	netLocalStream
)

func defaultsFor(kind netKind) (Protocol, binary.ByteOrder) {
	switch kind {
	case netTCP:
		return BinaryStream, binary.BigEndian
	case netUDP:
		return Datagram, binary.BigEndian
	case netWebSocket:
		// WebSocket frames preserve boundaries; framer is pass-through.
		return SeqPacket, binary.BigEndian
	case netSCTP:
		// SCTP preserves message boundaries.
		return SeqPacket, binary.BigEndian
	case netUnixStream:
		return BinaryStream, binary.BigEndian
	case netUnixPacket:
		return Datagram, binary.BigEndian
	case netLocalStream:
		return BinaryStream, bo.Native()
	default:
		return BinaryStream, binary.BigEndian
	}
}

// WithReadTCP configures the reader side for TCP: BinaryStream with BigEndian length prefix.
func WithReadTCP() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netTCP)
		o.ReadProto = p
		o.ReadByteOrder = bo
	}
}

// WithWriteTCP configures the writer side for TCP: BinaryStream with BigEndian length prefix.
func WithWriteTCP() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netTCP)
		o.WriteProto = p
		o.WriteByteOrder = bo
	}
}

// WithReadUDP configures the reader side for UDP: Datagram (pass-through), BigEndian default.
func WithReadUDP() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netUDP)
		o.ReadProto = p
		o.ReadByteOrder = bo
	}
}

// WithWriteUDP configures the writer side for UDP: Datagram (pass-through), BigEndian default.
func WithWriteUDP() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netUDP)
		o.WriteProto = p
		o.WriteByteOrder = bo
	}
}

// WithReadWebSocket configures the reader side for WebSocket: SeqPacket (boundaries preserved), BigEndian.
func WithReadWebSocket() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netWebSocket)
		o.ReadProto = p
		o.ReadByteOrder = bo
	}
}

// WithWriteWebSocket configures the writer side for WebSocket: SeqPacket (boundaries preserved), BigEndian.
func WithWriteWebSocket() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netWebSocket)
		o.WriteProto = p
		o.WriteByteOrder = bo
	}
}

// WithReadSCTP configures the reader side for SCTP: SeqPacket (boundaries preserved), BigEndian.
func WithReadSCTP() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netSCTP)
		o.ReadProto = p
		o.ReadByteOrder = bo
	}
}

// WithWriteSCTP configures the writer side for SCTP: SeqPacket (boundaries preserved), BigEndian.
func WithWriteSCTP() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netSCTP)
		o.WriteProto = p
		o.WriteByteOrder = bo
	}
}

// WithReadUnix configures the reader side for Unix stream sockets: BinaryStream, BigEndian.
func WithReadUnix() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netUnixStream)
		o.ReadProto = p
		o.ReadByteOrder = bo
	}
}

// WithWriteUnix configures the writer side for Unix stream sockets: BinaryStream, BigEndian.
func WithWriteUnix() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netUnixStream)
		o.WriteProto = p
		o.WriteByteOrder = bo
	}
}

// WithReadUnixPacket configures the reader side for Unix datagram sockets: Datagram (pass-through), BigEndian.
func WithReadUnixPacket() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netUnixPacket)
		o.ReadProto = p
		o.ReadByteOrder = bo
	}
}

// WithWriteUnixPacket configures the writer side for Unix datagram sockets: Datagram (pass-through), BigEndian.
func WithWriteUnixPacket() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netUnixPacket)
		o.WriteProto = p
		o.WriteByteOrder = bo
	}
}

// WithReadLocal configures the reader side for local (stream) transports: BinaryStream, native byte order.
func WithReadLocal() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netLocalStream)
		o.ReadProto = p
		o.ReadByteOrder = bo
	}
}

// WithWriteLocal configures the writer side for local (stream) transports: BinaryStream, native byte order.
func WithWriteLocal() Option {
	return func(o *Options) {
		p, bo := defaultsFor(netLocalStream)
		o.WriteProto = p
		o.WriteByteOrder = bo
	}
}
