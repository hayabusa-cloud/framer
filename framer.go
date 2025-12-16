// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

// Package framer provides a portable message framing layer exposed via io.Reader
// and io.Writer.
//
// Semantics and design:
//   - Protocol adaptation: on stream transports (e.g., TCP), framer adds a compact
//     length prefix and preserves one-message-per-Read/Write. On boundary-preserving
//     transports (SeqPacket/Datagram: e.g., SCTP, UDP, WebSocket), framer is pass-through.
//   - Non-blocking first: iox.ErrWouldBlock and iox.ErrMore are surfaced as control-flow
//     signals (and re-exposed as framer.ErrWouldBlock / framer.ErrMore). Hot paths avoid
//     allocations and return promptly.
//   - io compatibility: Reader, Writer, and ReadWriter implement standard io interfaces
//     and honor io.Writer short-write contracts and io.Reader buffer semantics.
//
// Wire format (stream mode): a 1-byte header followed by optional extended length bytes
// and then the payload. Let L be payload length in bytes:
//   - 0 <= L <= 253: header[0] = L (no extended length)
//   - 254 <= L <= 65535: header[0] = 0xFE; next 2 bytes encode L (configured byte order)
//   - 65536 <= L <= 2^56-1: header[0] = 0xFF; next 7 bytes encode lower 56 bits of L
//     in the configured byte order
// Maximum supported payload is 2^56-1; larger values produce ErrTooLong. A per-reader
// limit can be set via WithReadLimit.

package framer

import (
	"io"

	"code.hybscloud.com/iox"
)

// NewReader returns an io.Reader that reads framed messages from r.
func NewReader(r io.Reader, opts ...Option) io.Reader {
	return &Reader{fr: newFramer(r, nil, opts...)}
}

// NewWriter returns an io.Writer that writes framed messages to w.
func NewWriter(w io.Writer, opts ...Option) io.Writer {
	return &Writer{fr: newFramer(nil, w, opts...)}
}

// NewReadWriter returns an io.ReadWriter that reads and writes framed messages.
func NewReadWriter(r io.Reader, w io.Writer, opts ...Option) io.ReadWriter {
	fr := newFramer(r, w, opts...)
	return &ReadWriter{Reader: &Reader{fr: fr}, Writer: &Writer{fr: fr}}
}

// NewPipe returns a synchronous in-memory framing pipe.
func NewPipe(opts ...Option) (reader io.Reader, writer io.Writer) {
	r, w := io.Pipe()
	pipe := NewReadWriter(r, w, opts...)
	return pipe, pipe
}

// Reader reads framed messages.
type Reader struct{ fr *framer }

func (r *Reader) Read(p []byte) (int, error) { return r.fr.read(p) }

// WriteTo implements io.WriterTo.
//
// Semantics:
//   - Stream (BinaryStream): transfers one framed message payload at a time from the
//     underlying reader into dst. The payload bytes are written as-is; this method does
//     not attempt to preserve or reconstruct framer wire format on the destination unless
//     dst is itself a framer.Writer. It uses an internal reusable scratch buffer sized by
//     the Reader's ReadLimit; when ReadLimit is zero, a conservative default cap is used
//     (64KiB) and messages exceeding this cap result in ErrTooLong.
//   - Packet (SeqPacket/Datagram): pass-through, reads bytes and writes them to dst.
//
// Non-blocking semantics: if the underlying reader or writer returns iox.ErrWouldBlock
// or iox.ErrMore, WriteTo returns immediately with the progress count (bytes written) and
// the same semantic error. Short writes on dst are handled per io.Writer contract.
func (r *Reader) WriteTo(dst io.Writer) (int64, error) {
	fr := r.fr
	var total int64

	// Packet-preserving protocols: pass-through copy using a stack buffer.
	if fr.rpr.preserveBoundary() {
		var buf [32 * 1024]byte
		for {
			n, err := fr.read(buf[:])
			if n > 0 {
				off := 0
				for off < n {
					wn, we := dst.Write(buf[off:n])
					if wn > 0 {
						total += int64(wn)
						off += wn
					}
					if we != nil {
						// Propagate semantic control-flow unchanged.
						if we == ErrWouldBlock || we == ErrMore {
							return total, we
						}
						return total, we
					}
					if wn == 0 {
						// Avoid potential infinite loop on pathological writers.
						return total, io.ErrShortWrite
					}
				}
			}
			if err != nil {
				if err == io.EOF {
					return total, nil
				}
				if err == ErrWouldBlock || err == ErrMore {
					return total, err
				}
				return total, err
			}
		}
	}

	// Stream protocol: copy one framed message at a time.
	if fr.rbuf == nil {
		// Allocate scratch buffer once per framer instance. Zero alloc steady-state.
		capHint := fr.readLimit
		if capHint <= 0 {
			capHint = 64 * 1024
		}
		fr.rbuf = make([]byte, capHint)
	}

	for {
		// Drive header parse with a zero-length read to establish fr.length.
		// This may return io.ErrShortBuffer once the header is fully parsed and
		// a non-zero payload length is known.
		_, err := fr.read(nil)
		if err != nil {
			if err == io.ErrShortBuffer {
				// Header parsed; payload length available in fr.length.
				if fr.length > int64(cap(fr.rbuf)) {
					// When ReadLimit==0, enforce a conservative cap for WriteTo.
					return total, ErrTooLong
				}
				// proceed to read payload
			} else {
				if err == io.EOF {
					return total, nil
				}
				// Propagate io.ErrUnexpectedEOF - stream ended mid-header.
				if err == ErrWouldBlock || err == ErrMore {
					return total, err
				}
				return total, err
			}
		} else {
			// Zero-length message completed; nothing to write.
			// Continue to next message.
			// (If no data was available, fr.read would have returned ErrWouldBlock.)
			// Fall through to next iteration.
		}

		// If length is zero, skip payload read/write.
		if fr.length == 0 {
			continue
		}

		need := int(fr.length)
		got := 0
		for got < need {
			n, e := fr.read(fr.rbuf[got:need])
			got += n
			if e != nil {
				if e == ErrWouldBlock || e == ErrMore {
					return total, e
				}
				if e == io.EOF {
					return total, io.ErrUnexpectedEOF
				}
				return total, e
			}
		}

		// Write payload to dst, honoring short-write and semantic errors.
		off := 0
		for off < need {
			wn, we := dst.Write(fr.rbuf[off:need])
			if wn > 0 {
				total += int64(wn)
				off += wn
			}
			if we != nil {
				if we == ErrWouldBlock || we == ErrMore {
					return total, we
				}
				return total, we
			}
			if wn == 0 {
				return total, io.ErrShortWrite
			}
		}
		// loop for next message
	}
}

// Writer writes framed messages.
type Writer struct{ fr *framer }

func (w *Writer) Write(p []byte) (int, error) { return w.fr.write(p) }

// ReadFrom implements io.ReaderFrom.
//
// Semantics:
//   - Chunk-to-message: each chunk read from src (a successful src.Read call) is encoded
//     as a single framed message and written via w.Write. This is efficient but does not
//     preserve upstream application message boundaries. For protocols that already preserve
//     boundaries (SeqPacket/Datagram), this is effectively pass-through.
//
// Non-blocking semantics: if src.Read or the underlying writer returns iox.ErrWouldBlock
// or iox.ErrMore, ReadFrom returns immediately with the progress count and the same error.
// No heap allocations in the steady-state path.
func (w *Writer) ReadFrom(src io.Reader) (int64, error) {
	fr := w.fr
	// Reuse a per-framer buffer to guarantee zero allocs/op.
	if fr.wbuf == nil {
		fr.wbuf = make([]byte, 32*1024)
	}
	buf := fr.wbuf

	var total int64
	for {
		n, er := src.Read(buf)
		if n > 0 {
			// Encode this chunk as one framed message.
			wn, we := fr.write(buf[:n])
			if wn > 0 {
				total += int64(wn)
			}
			if we != nil {
				if we == ErrWouldBlock || we == ErrMore {
					return total, we
				}
				return total, we
			}
			if wn != n {
				// fr.write never returns short write without an error in stream mode,
				// but guard against pathological writers.
				return total, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return total, nil
			}
			if er == ErrWouldBlock || er == ErrMore {
				return total, er
			}
			return total, er
		}
	}
}

// ReadWriter groups Reader and Writer.
type ReadWriter struct {
	*Reader
	*Writer
}

// These are provided as package-level aliases so callers can reference the
// semantic control-flow errors without importing iox directly.
var (
	// ErrWouldBlock means “no further progress without waiting”.
	//
	// It is an expected, non-failure control-flow signal for non-blocking I/O.
	// Any returned byte count (n) still represents real progress.
	//
	// Caller action: stop the current attempt and retry later (after readiness/event),
	// or configure RetryDelay to emulate cooperative blocking on top of a non-blocking transport.
	ErrWouldBlock = iox.ErrWouldBlock

	// ErrMore means “this completion is usable and more completions will follow”.
	//
	// It is not io.EOF and not “try later”. The operation remains active and additional
	// data/results are expected from the same ongoing operation.
	//
	// Caller action: process the returned bytes/result, then call again to obtain the next chunk.
	ErrMore = iox.ErrMore
)
