// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer

import (
	"io"
)

// Forwarder relays framed messages from a source to a destination while
// preserving message boundaries.
//
// Semantics (BinaryStream):
//   - One call to ForwardOnce processes at most one logical message.
//   - Two-phase state machine per message:
//     1) Read a whole framed message payload from src into an internal buffer
//     (non-blocking; may return early with partial progress and ErrWouldBlock
//     or ErrMore).
//     2) Write that same payload as exactly one framed message to dst
//     (non-blocking; may return early with partial progress and ErrWouldBlock
//     or ErrMore).
//   - Returns (n, nil) when a whole message payload has been forwarded to dst.
//   - Returns ErrWouldBlock or ErrMore when the current phase cannot continue
//     without a later retry. The returned n is the current-phase progress and can
//     be zero when that phase accepted no payload bytes.
//   - Message boundaries are preserved: the destination sees exactly the same
//     payload bytes as the source, encoded as one framed message on stream
//     transports.
//
// Semantics (SeqPacket/Datagram):
//   - Treats one packet as one message unit per call. Reads one packet from src
//     and writes one packet to dst.
//   - If the packet source returns (n > 0, err), ForwardOnce forwards that packet
//     before reporting err. A final (n > 0, io.EOF) packet is reported as nil
//     after the packet is forwarded, and the next call reports io.EOF.
//   - Destination packet writes are atomic: zero-progress ErrWouldBlock or ErrMore
//     keeps the whole packet pending, while a partial packet write fails with
//     io.ErrShortWrite rather than retrying a suffix as a second packet.
//   - Return counts follow the current ForwardOnce phase documented on
//     [Forwarder.ForwardOnce].
//
// Limits and buffer sizing:
//   - The internal payload buffer is allocated during construction based on
//     read-side limit (WithReadLimit). If ReadLimit is zero, a conservative
//     default (64KiB) is used. There are no heap allocations in the steady-state
//     forwarding path.
//   - If a stream message or required packet read capacity exceeds the internal
//     buffer capacity, ForwardOnce returns io.ErrShortBuffer. Callers can construct
//     a new Forwarder with a larger ReadLimit to accommodate larger messages.
//   - If a packet exceeds ReadLimit, or exceeds the default packet transfer cap
//     when ReadLimit is zero, ForwardOnce returns ErrTooLong before forwarding bytes.
//
// Retry rule:
//   - On ErrWouldBlock or ErrMore, the caller must retry ForwardOnce on the same
//     Forwarder instance. The instance may hold an in-flight write or a pending
//     source signal that must remain ordered with the forwarded packet.
type Forwarder struct {
	// Read and write framers (directional state).
	rr *framer // read-side state machine (uses rr.rd, rr.rpr)
	ww *framer // write-side state machine (uses ww.wr, ww.wpr)

	// Internal payload buffer reused across messages to ensure zero-alloc steady state.
	buf []byte

	// Per-message state.
	need  int   // payload length for current message
	got   int   // bytes read into buf so far
	state uint8 // 0: parse header, 1: read payload, 2: write frame

	// Packet-source signal returned with n > 0. The packet is forwarded before
	// this signal is reported. io.EOF is normalized to nil for the forwarded packet
	// and reported on the next idle call.
	sourceAfter error
	eofPending  bool
}

// NewForwarder returns a Forwarder that relays messages from src to dst.
//
// Options apply to both directions unless a read-side or write-side option is
// used.
func NewForwarder(dst io.Writer, src io.Reader, opts ...Option) *Forwarder {
	rr := newFramer(src, nil, opts...)
	ww := newFramer(nil, dst, opts...)
	// Allocate internal buffer once to avoid allocations in steady state.
	capHint, _, err := rr.ensureForwardBufferCap()
	if err != nil {
		capHint = defaultPacketTransferMax
	}
	return &Forwarder{rr: rr, ww: ww, buf: make([]byte, capHint)}
}

// ForwardOnce forwards at most one message.
//
// See Forwarder documentation for stream, packet, limit, and retry semantics.
//
// Return value n reflects progress in the current phase:
//   - During the read phase, n is the number of payload bytes read into the
//     internal buffer in this call.
//   - During the write phase, n is the number of payload bytes written to dst
//     in this call.
//   - In packet mode, if the source returns n > 0 with an error, ForwardOnce
//     emits that packet before reporting the source error. If write-side
//     suspension happens first, the source error remains pending for a later
//     ForwardOnce call on the same Forwarder.
func (f *Forwarder) ForwardOnce() (n int, err error) {
	if f.state == 0 && f.sourceAfter != nil {
		after := f.sourceAfter
		f.sourceAfter = nil
		if after == io.EOF {
			f.eofPending = true
			return 0, nil
		}
		return 0, after
	}

	// If the source signaled EOF together with the previous (final) message,
	// report EOF on the first idle call after that message was forwarded.
	if f.state == 0 && f.eofPending {
		return 0, io.EOF
	}

	// Phase 0: drive header parse to learn payload length.
	if f.state == 0 {
		// For packet-preserving protocols, there is no header parsing; we will
		// read directly into the payload buffer sized by need once we know it.
		// For streams, read(nil) drives header parsing and sets rr.length.
		if !f.rr.rpr.preserveBoundary() {
			_, e := f.rr.read(nil)
			if e != nil {
				if e == io.ErrShortBuffer {
					// Header parsed; rr.length holds the payload length.
					if f.rr.length > int64(cap(f.buf)) {
						return 0, io.ErrShortBuffer
					}
					f.need = int(f.rr.length)
					f.got = 0
					f.state = 1
				} else {
					// EOF => no next message.
					if e == io.EOF {
						return 0, io.EOF
					}
					// Propagate io.ErrUnexpectedEOF - stream ended mid-header.
					// Propagate non-blocking and other errors as-is.
					return 0, e
				}
			} else {
				// Zero-length message: proceed to write phase.
				f.need = 0
				f.got = 0
				f.state = 2
			}
		} else {
			// Packet-preserving: we don't know the size upfront; we will read a
			// whole packet into the buffer up to capacity. Enforce read limit.
			f.got = 0
			f.need = 0 // unknown; treat as up to cap(buf)
			f.state = 1
		}
	}

	// Phase 1: read payload into the internal buffer.
	if f.state == 1 {
		if f.rr.rpr.preserveBoundary() {
			// Read one packet into the buffer (bounded by capacity / ReadLimit).
			acceptedMax := packetTransferAcceptedMax(f.rr)
			readCap, capErr := packetTransferCap(f.rr)
			if capErr != nil {
				f.state = 0
				f.got = 0
				f.need = 0
				f.sourceAfter = nil
				return 0, capErr
			}
			if cap(f.buf) < readCap {
				return 0, io.ErrShortBuffer
			}
			// Attempt a single packet read. A positive count is a packet source
			// signal; any accompanying source signal is deferred until after the
			// packet is emitted.
			rn, re := f.rr.read(f.buf[:readCap])
			f.got = rn
			if f.got > acceptedMax {
				f.state = 0
				f.need = 0
				f.got = 0
				f.sourceAfter = nil
				return rn, ErrTooLong
			}
			if re != nil {
				switch re {
				case ErrWouldBlock, ErrMore:
					if rn == 0 {
						return 0, re
					}
					f.sourceAfter = re
				case ErrTooLong:
					f.state = 0
					f.need = 0
					f.got = 0
					f.sourceAfter = nil
					return rn, re
				case io.EOF:
					if f.got == 0 {
						return 0, io.EOF
					}
					f.sourceAfter = io.EOF
				default:
					if rn == 0 {
						return 0, re
					}
					f.sourceAfter = re
				}
			}
			// Packet read complete in one call (best effort). Proceed to write.
			f.need = f.got
			f.state = 2
		} else {
			// Stream payload read; need is known. Pass the full payload slice on
			// every call to satisfy the reader's contract (len(p) must equal length).
			for f.got < f.need {
				rn, re := f.rr.read(f.buf[:f.need])
				f.got += rn
				if re != nil {
					if re == ErrWouldBlock || re == ErrMore {
						return rn, re
					}
					if re == io.EOF {
						return f.got, io.ErrUnexpectedEOF
					}
					return rn, re
				}
			}
			f.state = 2
		}
	}

	// Phase 2: write the payload as one framed message to destination.
	if f.state == 2 {
		wn, we := f.ww.write(f.buf[:f.need])
		if we != nil {
			if isSemanticControl(we) {
				if !f.ww.wpr.preserveBoundary() {
					return wn, we
				}
				if wn == 0 {
					return wn, we
				}
				if wn == f.need && f.sourceAfter != nil {
					f.state = 0
					f.need = 0
					f.got = 0
					return wn, we
				}
			}
			f.state = 0
			f.need = 0
			f.got = 0
			f.sourceAfter = nil
			return wn, we
		}
		after := f.sourceAfter
		// Message fully forwarded; reset for next call.
		f.sourceAfter = nil
		if after == io.EOF {
			f.eofPending = true
		}
		f.state = 0
		f.need = 0
		f.got = 0
		if after != nil && after != io.EOF {
			return wn, after
		}
		return wn, nil
	}

	// If we reached here, the call advanced state but produced no I/O.
	return 0, nil
}
