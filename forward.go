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
//   - Returns (n>0, ErrWouldBlock|ErrMore) when progress happened in the current
//     phase (read or write) but the forwarding of this message is incomplete.
//   - Message boundaries are preserved: the destination sees exactly the same
//     payload bytes as the source, encoded as one framed message on stream
//     transports.
//
// Semantics (SeqPacket/Datagram):
//   - Treats one packet as one message unit per call. Reads one packet from src
//     and writes one packet to dst.
//   - Returns values and non-blocking semantics as above.
//
// Limits and buffer sizing:
//   - The internal payload buffer is allocated during construction based on
//     read-side limit (WithReadLimit). If ReadLimit is zero, a conservative
//     default (64KiB) is used. There are no heap allocations in the steady-state
//     forwarding path.
//   - If the current message exceeds the internal buffer capacity, ForwardOnce
//     returns io.ErrShortBuffer. Callers can construct a new Forwarder with a
//     larger ReadLimit to accommodate larger messages.
//   - If the current message exceeds the configured ReadLimit, ForwardOnce
//     returns ErrTooLong.
//
// Retry rule:
//   - On ErrWouldBlock or ErrMore, the caller must retry ForwardOnce on the SAME
//     Forwarder instance to complete the in-flight message. Do not reuse a
//     different instance because the in-flight state (read/write progress) is
//     maintained internally.
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

	// EOF handling for packet-preserving protocols:
	// some io.Reader implementations may return (n>0, io.EOF) on the final read.
	// ForwardOnce forwards that final message and then returns io.EOF on the next call.
	eofAfterThis bool
	eofPending   bool
}

// NewForwarder constructs a Forwarder that relays messages from src to dst.
// Options apply per direction (read/write) following the same rules as Reader/Writer.
func NewForwarder(dst io.Writer, src io.Reader, opts ...Option) *Forwarder {
	rr := newFramer(src, nil, opts...)
	ww := newFramer(nil, dst, opts...)
	// Allocate internal buffer once to avoid allocations in steady state.
	capHint := rr.readLimit
	if capHint <= 0 {
		capHint = 64 * 1024
	}
	return &Forwarder{rr: rr, ww: ww, buf: make([]byte, capHint)}
}

// ForwardOnce forwards at most one message. See Forwarder docs for semantics.
//
// Return value n reflects progress in the current phase:
//   - During the read phase, n is the number of payload bytes read into the
//     internal buffer in this call.
//   - During the write phase, n is the number of payload bytes written to dst
//     in this call.
func (f *Forwarder) ForwardOnce() (n int, err error) {
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
			// Enforce limits: if readLimit > 0 and capacity exceeds limit, we still only
			// accept up to readLimit bytes for a single packet.
			max := cap(f.buf)
			if f.rr.readLimit > 0 && int64(max) > f.rr.readLimit {
				max = int(f.rr.readLimit)
			}
			// Attempt a single read; may be short if underlying is non-blocking.
			// Use f.buf[f.got:max] to correctly accumulate partial reads across
			// ErrWouldBlock boundaries without overwriting already-read data.
			rn, re := f.rr.read(f.buf[f.got:max])
			f.got += rn
			if re != nil {
				switch re {
				case ErrWouldBlock, ErrMore, ErrTooLong:
					return rn, re
				case io.EOF:
					if f.got == 0 {
						return 0, io.EOF
					}
					// Final message: (n>0, io.EOF) is treated like a normal completion.
					// After forwarding this message, the next ForwardOnce returns io.EOF.
					f.eofAfterThis = true
					// Proceed to the write phase.
				default:
					return rn, re
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
			if we == ErrWouldBlock || we == ErrMore {
				return wn, we
			}
			return wn, we
		}
		// Message fully forwarded; reset for next call.
		if f.eofAfterThis {
			f.eofAfterThis = false
			f.eofPending = true
		}
		f.state = 0
		f.need = 0
		f.got = 0
		return wn, nil
	}

	// If we reached here, the call advanced state but produced no I/O.
	return 0, nil
}
