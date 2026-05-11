// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

// Package framer provides portable message framing over io.Reader and io.Writer.
//
// Semantics and design:
//   - Protocol adaptation: on stream transports such as TCP, framer adds a compact
//     length prefix and preserves one-message-per-Read/Write. On boundary-preserving
//     transports such as SCTP, UDP, and WebSocket, framer is pass-through.
//   - Packet-mode limit semantics: [Reader.Read] checks [WithReadLimit] after one
//     transport read; oversized packets may return (n > limit, [ErrTooLong]), where n
//     is the number of bytes copied from that packet. [Reader.WriteTo] and [Forwarder]
//     use a sentinel byte to reject oversized packets before forwarding bytes.
//   - Non-blocking first: [ErrWouldBlock] and [ErrMore] are control-flow signals,
//     not failures. Hot paths avoid allocations and return promptly.
//   - Performance contract: hot paths keep runtime validation minimal; callers are
//     responsible for valid option and buffer usage plus operation-specific retry
//     after ErrWouldBlock or ErrMore.
//   - io compatibility: [Reader], [Writer], and [ReadWriter] implement standard io
//     interfaces and honor [io.Writer] short-write contracts and [io.Reader] buffer
//     semantics.
//
// Wire format (stream mode): a 1-byte header followed by optional extended length bytes
// and then the payload. Let L be payload length in bytes:
//   - 0 <= L <= 253: header[0] = L (no extended length)
//   - 254 <= L <= 65535: header[0] = 0xFE; next 2 bytes encode L (configured byte order)
//   - 65536 <= L <= 2^56-1: header[0] = 0xFF; next 7 bytes encode lower 56 bits of L
//     in the configured byte order
//
// Maximum supported payload is 2^56-1; larger values produce [ErrTooLong].
// A per-reader limit can be set via [WithReadLimit].
package framer

import (
	"io"

	"code.hybscloud.com/iox"
)

// NewReader returns an io.Reader that reads framed messages from r.
//
// The returned dynamic type is *Reader. Callers that need [Reader.WriteTo] can
// type assert the result to *Reader.
func NewReader(r io.Reader, opts ...Option) io.Reader {
	return &Reader{fr: newFramer(r, nil, opts...)}
}

// NewWriter returns an io.Writer that writes framed messages to w.
//
// The returned dynamic type is *Writer. Callers that need [Writer.ReadFrom] can
// type assert the result to *Writer.
func NewWriter(w io.Writer, opts ...Option) io.Writer {
	return &Writer{fr: newFramer(nil, w, opts...)}
}

// NewReadWriter returns an io.ReadWriter that reads and writes framed messages.
//
// The returned value shares one state machine for both directions and is intended
// for coordinated use. Concurrent access requires caller synchronization.
func NewReadWriter(r io.Reader, w io.Writer, opts ...Option) io.ReadWriter {
	fr := newFramer(r, w, opts...)
	return &ReadWriter{Reader: &Reader{fr: fr}, Writer: &Writer{fr: fr}}
}

// NewPipe returns a synchronous in-memory framing pipe.
//
// The pipe is backed by [io.Pipe]. Both returned interfaces refer to the same
// state-bearing [ReadWriter], so callers coordinate reads and writes as one
// synchronous pipe rather than treating them as independent endpoints.
func NewPipe(opts ...Option) (reader io.Reader, writer io.Writer) {
	r, w := io.Pipe()
	pipe := NewReadWriter(r, w, opts...)
	return pipe, pipe
}

// Reader reads framed messages from a stream or packet-preserving transport.
//
// A Reader carries in-flight state after ErrWouldBlock or ErrMore. Retry the
// interrupted operation on the same Reader instance before switching between Read
// and WriteTo.
type Reader struct{ fr *framer }

// Read reads one message payload into p.
//
// In SeqPacket and Datagram modes, Read passes through one transport read. In
// BinaryStream mode, p must fit the whole message payload or Read returns
// io.ErrShortBuffer without consuming the payload.
//
// In SeqPacket/Datagram mode, WithReadLimit is enforced after one transport
// read, so an oversized packet may return (n > limit, ErrTooLong); n reports
// bytes copied from that packet.
//
// If Read returns ErrWouldBlock or ErrMore after partial stream progress, retry
// Read on the same Reader instance with the same buffer until the message
// completes or a terminal error is returned.
func (r *Reader) Read(p []byte) (int, error) { return r.fr.read(p) }

// WriteTo writes messages from r to dst until the source reaches EOF or returns
// an error.
//
// Semantics:
//   - Stream (BinaryStream): transfers one framed message payload at a time from the
//     underlying reader into dst. The payload bytes are written as-is; this method does
//     not attempt to preserve or reconstruct framer wire format on the destination unless
//     dst is itself a framer.Writer. It uses an internal reusable scratch buffer sized by
//     the Reader's ReadLimit; when ReadLimit is zero, a conservative default cap is used
//     (64KiB) and messages exceeding this cap result in ErrTooLong.
//   - Packet (SeqPacket/Datagram): pass-through byte transfer for arbitrary dst.
//     Partial dst writes resume from an internal byte cursor on the next WriteTo call.
//     When dst is a framer packet Writer, whole-packet retry is used after zero-progress
//     ErrWouldBlock/ErrMore. When dst is a framer stream Writer, same-message frame
//     retry is used so the destination frame length remains stable. ReadLimit is checked
//     post-read with one sentinel byte, so oversized packets are rejected before forwarding.
//
// Non-blocking semantics: if the underlying reader or writer returns
// iox.ErrWouldBlock or iox.ErrMore, WriteTo returns immediately with the progress
// count and the same control signal. Retry WriteTo on the same Reader instance and
// destination to resume the interrupted transfer. Short writes on dst are handled per
// io.Writer contract. If a packet source returns bytes with an error, that source
// error is reported after the admitted packet is emitted, even if write-side
// suspension requires a later WriteTo call.
//
// The returned count is bytes written to dst. A rejected oversized packet does
// not add to the count.
func (r *Reader) WriteTo(dst io.Writer) (int64, error) {
	fr := r.fr
	sink := classifyWriteToSink(dst)

	if fr.rpr.preserveBoundary() {
		return r.writeToPacketSource(sink)
	}

	return r.writeToStreamSource(sink)
}

func (r *Reader) writeToStreamSource(sink writeToSink) (int64, error) {
	fr := r.fr
	var total int64

	for {
		if fr.wtMode != writeToNone {
			wn, we, done := resumeWriteToPending(fr, sink)
			total += int64(wn)
			if done || we != nil {
				return total, we
			}
		}

		// Drive header parse with a zero-length read to establish fr.length.
		// This may return io.ErrShortBuffer once the header is fully parsed and
		// a non-zero payload length is known.
		_, err := fr.read(nil)
		if err != nil {
			if err == io.ErrShortBuffer {
				// Header parsed; payload length available in fr.length.
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
			// Zero-length message completed.
			// (If no data was available, fr.read would have returned ErrWouldBlock.)
			// Fall through to next iteration.
		}

		// If length is zero, arbitrary byte destinations have nothing to receive.
		// Known framer destinations still receive an empty message.
		if fr.length == 0 {
			if sink.mode != writeToByte {
				wn, we, done := emitWriteToMessage(fr, sink, nil, nil)
				total += int64(wn)
				if done || we != nil {
					return total, we
				}
			}
			continue
		}

		// State-driven payload read loop. Pass the full buffer to readStream;
		// readStream uses fr.offset internally to write to p[payloadOff:] where
		// payloadOff = fr.offset - hdrSize. This ensures correct accumulation
		// across ErrWouldBlock boundaries without losing progress.
		need := int(fr.length)
		buf := fr.ensureStreamReadBuffer()
		if need > cap(buf) {
			// When ReadLimit==0, enforce a conservative cap for WriteTo.
			return total, ErrTooLong
		}
		for {
			_, e := fr.read(buf[:need])
			if e != nil {
				if e == ErrWouldBlock || e == ErrMore {
					return total, e
				}
				if e == io.EOF {
					return total, io.ErrUnexpectedEOF
				}
				return total, e
			}
			// readStream calls fr.reset() on completion, so fr.offset becomes 0.
			// Check if message is complete.
			if fr.offset == 0 {
				// Message complete, fr.reset() was called
				break
			}
		}

		wn, we, done := emitWriteToMessage(fr, sink, buf[:need], nil)
		total += int64(wn)
		if done || we != nil {
			return total, we
		}
		// loop for next message
	}
}

func (r *Reader) writeToPacketSource(sink writeToSink) (int64, error) {
	fr := r.fr
	var total int64

	if fr.wtMode != writeToNone {
		wn, we, done := resumeWriteToPending(fr, sink)
		total += int64(wn)
		if done || we != nil {
			return total, we
		}
	}

	buf, acceptedMax, err := fr.ensurePacketReadBuffer()
	if err != nil {
		return 0, err
	}

	for {
		n, err := fr.read(buf)
		if n > acceptedMax {
			clearWriteToPending(fr)
			return total, ErrTooLong
		}
		if n > 0 {
			wn, we, done := emitWriteToMessage(fr, sink, buf[:n], err)
			total += int64(wn)
			if done || we != nil {
				return total, we
			}
		}
		if err != nil {
			if err == io.EOF {
				return total, nil
			}
			return total, err
		}
	}
}

// Writer writes framed messages to a stream or packet-preserving transport.
//
// A Writer can carry in-flight state after ErrWouldBlock or ErrMore. Retry an
// interrupted BinaryStream Write or ReadFrom operation on the same Writer before
// switching operations. Direct packet-mode Write carries no hidden replay state
// after the whole packet has been accepted.
type Writer struct{ fr *framer }

// Write writes p as one message.
//
// In BinaryStream mode, Write emits a length prefix before p. In SeqPacket and
// Datagram modes, Write passes p through as one transport packet and reports
// io.ErrShortWrite if the destination accepts only a prefix.
//
// The returned count is bytes accepted from p; BinaryStream header bytes are not
// included. In BinaryStream mode, if Write returns ErrWouldBlock or ErrMore,
// retry Write on the same Writer instance with the same message length before
// starting another message. In packet modes, n == len(p) with ErrWouldBlock or
// ErrMore means the packet was accepted; do not replay p. If packet-mode Write
// returns ErrWouldBlock or ErrMore with n == 0, retry with the same packet.
func (w *Writer) Write(p []byte) (int, error) { return w.fr.write(p) }

type writeToSink struct {
	mode writeToMode
	dst  io.Writer
	fr   *framer
}

func classifyWriteToSink(dst io.Writer) writeToSink {
	var dstFr *framer
	switch w := dst.(type) {
	case *Writer:
		if w != nil {
			dstFr = w.fr
		}
	case *ReadWriter:
		if w != nil && w.Writer != nil {
			dstFr = w.Writer.fr
		}
	}
	if dstFr != nil {
		if dstFr.wpr.preserveBoundary() {
			return writeToSink{mode: writeToPacket, fr: dstFr}
		}
		return writeToSink{mode: writeToFrame, fr: dstFr}
	}
	return writeToSink{mode: writeToByte, dst: dst}
}

func resumeWriteToPending(fr *framer, sink writeToSink) (int, error, bool) {
	if fr.wtMode == writeToNone {
		return 0, nil, false
	}
	if fr.wtMode == writeToControl {
		err, done := finishWriteToAfter(fr)
		return 0, err, done
	}
	if fr.wtMode != sink.mode {
		clearWriteToPending(fr)
		return 0, io.ErrShortWrite, true
	}

	switch sink.mode {
	case writeToByte:
		after := fr.wtAfter
		cur := byteCursor{off: fr.wtOff, end: fr.wtLen}
		n, err := drainByteCursor(&cur, fr.rbuf, sink.dst)
		if err != nil {
			if isSemanticControl(err) && cur.off < cur.end {
				saveWriteToByteCursor(fr, cur.off, cur.end, after)
				return n, err, true
			}
			if isSemanticControl(err) && after != nil {
				saveWriteToControl(fr, after)
				return n, err, true
			}
			clearWriteToPending(fr)
			return n, err, true
		}
		clearWriteToPending(fr)
		afterErr, done := reportWriteToAfter(after)
		return n, afterErr, done
	case writeToPacket:
		after := fr.wtAfter
		n, err := sink.fr.writePacket(fr.rbuf[:fr.wtLen])
		if err != nil {
			if isSemanticControl(err) && n == 0 {
				saveWriteToPacket(fr, fr.wtLen, after)
				return n, err, true
			}
			if isSemanticControl(err) && n == fr.wtLen && after != nil {
				saveWriteToControl(fr, after)
				return n, err, true
			}
			clearWriteToPending(fr)
			return n, err, true
		}
		clearWriteToPending(fr)
		afterErr, done := reportWriteToAfter(after)
		return n, afterErr, done
	case writeToFrame:
		after := fr.wtAfter
		n, err := sink.fr.write(fr.rbuf[:fr.wtLen])
		if err != nil {
			if isSemanticControl(err) {
				saveWriteToFrame(fr, fr.wtLen, after)
				return n, err, true
			}
			clearWriteToPending(fr)
			return n, err, true
		}
		clearWriteToPending(fr)
		afterErr, done := reportWriteToAfter(after)
		return n, afterErr, done
	default:
		clearWriteToPending(fr)
		return 0, ErrInvalidArgument, true
	}
}

func emitWriteToMessage(fr *framer, sink writeToSink, msg []byte, sourceAfter error) (int, error, bool) {
	switch sink.mode {
	case writeToByte:
		cur := byteCursor{end: len(msg)}
		n, err := drainByteCursor(&cur, msg, sink.dst)
		if err != nil {
			if isSemanticControl(err) && cur.off < cur.end {
				saveWriteToByteCursor(fr, cur.off, cur.end, sourceAfter)
				return n, err, true
			}
			if isSemanticControl(err) && sourceAfter != nil {
				saveWriteToControl(fr, sourceAfter)
				return n, err, true
			}
			clearWriteToPending(fr)
			return n, err, true
		}
		clearWriteToPending(fr)
		afterErr, done := reportWriteToAfter(sourceAfter)
		return n, afterErr, done
	case writeToPacket:
		saveWriteToPacket(fr, len(msg), sourceAfter)
		n, err := sink.fr.writePacket(msg)
		if err != nil {
			if isSemanticControl(err) && n == 0 {
				return n, err, true
			}
			if isSemanticControl(err) && n == len(msg) && sourceAfter != nil {
				saveWriteToControl(fr, sourceAfter)
				return n, err, true
			}
			clearWriteToPending(fr)
			return n, err, true
		}
		clearWriteToPending(fr)
		afterErr, done := reportWriteToAfter(sourceAfter)
		return n, afterErr, done
	case writeToFrame:
		saveWriteToFrame(fr, len(msg), sourceAfter)
		n, err := sink.fr.write(msg)
		if err != nil {
			if isSemanticControl(err) {
				return n, err, true
			}
			clearWriteToPending(fr)
			return n, err, true
		}
		clearWriteToPending(fr)
		afterErr, done := reportWriteToAfter(sourceAfter)
		return n, afterErr, done
	default:
		clearWriteToPending(fr)
		return 0, ErrInvalidArgument, true
	}
}

// ReadFrom reads chunks from src and writes each chunk as a framed message.
//
// Semantics:
//   - Chunk-to-message: each chunk read from src (a successful src.Read call) is encoded
//     as a single framed message and written via w.Write. This is efficient but does not
//     preserve upstream application message boundaries. For protocols that already preserve
//     boundaries (SeqPacket/Datagram), this is effectively pass-through.
//
// Non-blocking semantics: if src.Read or the underlying writer returns
// iox.ErrWouldBlock or iox.ErrMore, ReadFrom returns immediately with the
// same control signal. Retry ReadFrom on the same Writer instance to resume the
// interrupted message. Pure write-side resume calls can return n == 0 because
// the pending bytes were already read from src and admitted to the Writer.
//
// The returned count is bytes read from src and admitted to this Writer. If
// src.Read returns n > 0 with an error, the source error is reported after that
// admitted chunk is emitted. If write-side ErrWouldBlock or ErrMore is observed
// first, the source signal remains pending for a later ReadFrom call.
// No heap allocations occur in the steady-state path.
func (w *Writer) ReadFrom(src io.Reader) (int64, error) {
	fr := w.fr
	if hasReadFromControl(fr) {
		if after, done := finishReadFromPending(fr); done || after != nil {
			return 0, after
		}
	}
	if fr.wpr.preserveBoundary() {
		return w.readFromPacket(src)
	}
	// Reuse a per-framer buffer to guarantee zero allocs/op.
	buf := fr.ensureReadFromBuffer()

	var total int64
	for {
		if fr.rfLen > 0 {
			if fr.rfLen > len(buf) {
				return total, io.ErrShortBuffer
			}
			_, we := fr.write(buf[:fr.rfLen])
			if we != nil {
				if isSemanticControl(we) {
					return total, we
				}
				clearReadFromPending(fr)
				return total, we
			}
			if after, done := finishReadFromPending(fr); done || after != nil {
				return total, after
			}
			continue
		}

		if fr.length > 0 {
			if int(fr.length) > len(buf) {
				return total, io.ErrShortBuffer
			}
			return total, io.ErrShortWrite
		}

		n, er := src.Read(buf)
		if n > 0 {
			saveReadFromPending(fr, n, er)
			total += int64(n)
			_, we := fr.write(buf[:n])
			if we != nil {
				if isSemanticControl(we) {
					return total, we
				}
				clearReadFromPending(fr)
				return total, we
			}
			if after, done := finishReadFromPending(fr); done || after != nil {
				return total, after
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

func (w *Writer) readFromPacket(src io.Reader) (int64, error) {
	fr := w.fr
	buf := fr.ensureReadFromBuffer()

	var total int64
	for {
		if fr.rfLen > 0 {
			n, err := fr.writePacket(buf[:fr.rfLen])
			if err != nil {
				if isSemanticControl(err) {
					if n == 0 {
						return total, err
					}
					if n == fr.rfLen && fr.rfAfter != nil {
						saveReadFromControl(fr, fr.rfAfter)
						return total, err
					}
				}
				clearReadFromPending(fr)
				return total, err
			}
			if after, done := finishReadFromPending(fr); done || after != nil {
				return total, after
			}
			continue
		}

		n, er := src.Read(buf)
		if n > 0 {
			saveReadFromPending(fr, n, er)
			total += int64(n)
			wn, we := fr.writePacket(buf[:n])
			if we != nil {
				if isSemanticControl(we) {
					if wn == 0 {
						return total, we
					}
					if wn == n && fr.rfAfter != nil {
						saveReadFromControl(fr, fr.rfAfter)
						return total, we
					}
				}
				clearReadFromPending(fr)
				return total, we
			}
			if after, done := finishReadFromPending(fr); done || after != nil {
				return total, after
			}
		}
		if er != nil {
			if er == io.EOF {
				return total, nil
			}
			return total, er
		}
	}
}

// ReadWriter groups a Reader and Writer that share one framer state.
//
// ReadWriter is a convenience wrapper for coordinated use. Concurrent access
// requires caller synchronization.
type ReadWriter struct {
	*Reader
	*Writer
}

var (
	// ErrWouldBlock reports that the current non-blocking transport attempt cannot
	// progress without waiting.
	//
	// It is a control-flow signal, not a failure. Aggregate helpers may return a
	// positive count with ErrWouldBlock when earlier steps in the same call made
	// progress before the current attempt suspended.
	//
	// ErrWouldBlock is an alias for iox.ErrWouldBlock.
	ErrWouldBlock = iox.ErrWouldBlock

	// ErrMore reports that the same operation has additional progress to deliver.
	//
	// It is not io.EOF and not readiness suspension. Process any returned
	// progress, then call the same operation again.
	//
	// ErrMore is an alias for iox.ErrMore.
	ErrMore = iox.ErrMore
)
