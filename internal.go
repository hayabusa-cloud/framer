// ©Hayabusa Cloud Co., Ltd. 2025. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package framer

import (
	"encoding/binary"
	"io"
	"runtime"
	"time"
)

const (
	frameHeaderLen          = 1
	framePayloadMaxLen8Bits = 1<<8 - 3
	framePayloadMaxLen16    = 1<<16 - 1
	framePayloadMaxLen56    = 1<<56 - 1
)

type framer struct {
	rd  io.Reader
	rbo binary.ByteOrder
	rpr Protocol
	wr  io.Writer
	wbo binary.ByteOrder
	wpr Protocol

	readLimit int64

	retryDelay time.Duration

	// stream state
	header [8]byte
	length int64 // payload length for current message
	offset int64 // bytes processed in (header+payload)

	// reusable scratch buffer for Reader.WriteTo fast path
	rbuf []byte

	// WriteTo partial-write resume state: when dst.Write returns a
	// partial result with ErrWouldBlock/ErrMore, wtOff..wtLen marks
	// the unwritten region inside rbuf so the next WriteTo call can
	// finish draining before reading a new message.
	wtOff int
	wtLen int

	// reusable scratch buffer for Writer.ReadFrom fast path
	wbuf []byte
}

func newFramer(r io.Reader, w io.Writer, opts ...Option) *framer {
	o := defaultOptions
	for _, fn := range opts {
		fn(&o)
	}

	fr := &framer{
		rd:        r,
		wr:        w,
		rbo:       o.ReadByteOrder,
		wbo:       o.WriteByteOrder,
		rpr:       o.ReadProto,
		wpr:       o.WriteProto,
		readLimit: int64(o.ReadLimit),

		retryDelay: o.RetryDelay,
	}
	return fr
}

func (fr *framer) reset() {
	fr.offset = 0
	fr.length = 0
}

func (fr *framer) yieldOnce() {
	// Cooperative yield to avoid burning a full core when emulating blocking
	// on top of a non-blocking transport.
	runtime.Gosched()
}

func (fr *framer) read(p []byte) (n int, err error) {
	if fr.rd == nil {
		return 0, ErrInvalidArgument
	}
	if fr.rpr.preserveBoundary() {
		return fr.readPacket(p)
	}
	return fr.readStream(p)
}

func (fr *framer) write(p []byte) (n int, err error) {
	if fr.wr == nil {
		return 0, ErrInvalidArgument
	}
	if fr.wpr.preserveBoundary() {
		return fr.writePacket(p)
	}
	return fr.writeStream(p)
}

func (fr *framer) waitOnceOnWouldBlock() bool {
	// returns whether the caller should retry
	if fr.retryDelay < 0 {
		return false
	}
	if fr.retryDelay == 0 {
		runtime.Gosched()
		return true
	}
	time.Sleep(fr.retryDelay)
	return true
}

func (fr *framer) readOnce(p []byte) (n int, err error) {
	for {
		n, err = fr.rd.Read(p)
		// Guard against broken Readers that violate the io.Reader contract by
		// returning (0, nil) on a non-empty buffer. Without this, the stream
		// state machine can spin indefinitely.
		if len(p) != 0 && n == 0 && err == nil {
			return 0, io.ErrNoProgress
		}
		if n > 0 {
			return n, err
		}
		if err != ErrWouldBlock {
			return n, err
		}
		if !fr.waitOnceOnWouldBlock() {
			return n, err
		}
	}
}

func (fr *framer) writeOnce(p []byte) (n int, err error) {
	for {
		n, err = fr.wr.Write(p)
		// Guard against broken Writers that violate the io.Writer contract by
		// returning (0, nil) on a non-empty buffer. Without this, the stream
		// writer can spin indefinitely.
		if len(p) != 0 && n == 0 && err == nil {
			return 0, io.ErrShortWrite
		}
		if n > 0 {
			return n, err
		}
		if err != ErrWouldBlock {
			return n, err
		}
		if !fr.waitOnceOnWouldBlock() {
			return n, err
		}
	}
}

func (fr *framer) readPacket(p []byte) (n int, err error) {
	n, err = fr.readOnce(p)
	if fr.readLimit > 0 && int64(n) > fr.readLimit {
		return n, ErrTooLong
	}
	return n, err
}

func (fr *framer) writePacket(p []byte) (n int, err error) {
	if int64(len(p)) > framePayloadMaxLen56 {
		return 0, ErrTooLong
	}
	n, err = fr.writeOnce(p)
	if err != nil {
		return n, err
	}
	if n != len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

func (fr *framer) readStream(p []byte) (n int, err error) {
	// Stream framing contract:
	// In Nonblock mode, partial progress may be returned with iox.ErrWouldBlock.
	// The caller must retry with the same buffer to preserve already-copied bytes.

	// 1) Read minimal header byte.
	for fr.offset < frameHeaderLen {
		rn, re := fr.readOnce(fr.header[fr.offset:frameHeaderLen])
		fr.offset += int64(rn)
		if re != nil {
			if re == io.EOF {
				if fr.offset == 0 {
					// Clean EOF at message boundary.
					return 0, io.EOF
				}
				if fr.offset < frameHeaderLen {
					// Partial header read; stream truncated.
					return 0, io.ErrUnexpectedEOF
				}
				break
			}
			return 0, re
		}
	}

	// 2) Determine extended length bytes.
	exLen := int64(0)
	if fr.offset >= frameHeaderLen {
		switch fr.header[0] {
		case framePayloadMaxLen8Bits + 1:
			exLen = 2
		case framePayloadMaxLen8Bits + 2:
			exLen = 7
		}
	}

	// 3) Read extended length bytes (if any).
	for fr.offset < frameHeaderLen+exLen {
		rn, re := fr.readOnce(fr.header[fr.offset : frameHeaderLen+exLen])
		fr.offset += int64(rn)
		if re != nil {
			if re == io.EOF {
				if fr.offset < frameHeaderLen+exLen {
					return 0, io.ErrUnexpectedEOF
				}
				break
			}
			return 0, re
		}
	}

	// 4) Parse payload length.
	if fr.offset == frameHeaderLen+exLen {
		if exLen == 2 {
			fr.length = int64(fr.rbo.Uint16(fr.header[frameHeaderLen : frameHeaderLen+exLen]))
		} else if exLen == 7 {
			u64 := fr.rbo.Uint64(fr.header[:])
			if fr.rbo == binary.LittleEndian {
				fr.length = int64(u64 >> 8)
			} else {
				fr.length = int64(u64 & framePayloadMaxLen56)
			}
		} else {
			fr.length = int64(fr.header[0])
		}
	}

	if fr.length < 0 || fr.length > framePayloadMaxLen56 {
		return 0, ErrTooLong
	}
	if fr.readLimit > 0 && fr.length > fr.readLimit {
		return 0, ErrTooLong
	}
	if int64(len(p)) < fr.length {
		return 0, io.ErrShortBuffer
	}

	// 5) Read payload directly into p.
	hdrSize := frameHeaderLen + exLen
	for fr.offset < hdrSize+fr.length {
		payloadOff := fr.offset - hdrSize
		rn, re := fr.readOnce(p[payloadOff:fr.length])
		fr.offset += int64(rn)
		n += rn
		if re != nil {
			if re == io.EOF {
				if fr.offset < hdrSize+fr.length {
					return n, io.ErrUnexpectedEOF
				}
				break
			}
			// Preserve semantic control-flow errors.
			return n, re
		}
	}

	fr.reset()
	return n, nil
}

func (fr *framer) writeStream(p []byte) (n int, err error) {
	if int64(len(p)) > framePayloadMaxLen56 {
		return 0, ErrTooLong
	}

	// Initialize per-message state on the first call.
	if fr.offset == 0 {
		fr.length = int64(len(p))
	}
	if fr.length != int64(len(p)) {
		// The caller changed the message buffer mid-frame.
		return 0, io.ErrShortWrite
	}

	exLen := int64(0)
	if fr.length <= framePayloadMaxLen8Bits {
		exLen = 0
	} else if fr.length <= framePayloadMaxLen16 {
		exLen = 2
	} else {
		exLen = 7
	}

	// Fill header once.
	if fr.offset == 0 {
		if fr.length <= framePayloadMaxLen8Bits {
			fr.header[0] = byte(fr.length)
		} else if fr.length <= framePayloadMaxLen16 {
			fr.header[0] = framePayloadMaxLen8Bits + 1
			fr.wbo.PutUint16(fr.header[frameHeaderLen:frameHeaderLen+exLen], uint16(fr.length))
		} else {
			if fr.wbo == binary.LittleEndian {
				fr.wbo.PutUint64(fr.header[:], uint64(fr.length)<<8)
			} else {
				fr.wbo.PutUint64(fr.header[:], uint64(fr.length&framePayloadMaxLen56))
			}
			fr.header[0] = framePayloadMaxLen8Bits + 2
		}
	}

	hdrSize := frameHeaderLen + exLen
	for fr.offset < hdrSize {
		wn, we := fr.writeOnce(fr.header[fr.offset:hdrSize])
		fr.offset += int64(wn)
		if we != nil {
			return 0, we
		}
	}

	for fr.offset < hdrSize+fr.length {
		payloadOff := fr.offset - hdrSize
		wn, we := fr.writeOnce(p[payloadOff:])
		fr.offset += int64(wn)
		n += wn
		if we != nil {
			return n, we
		}
	}

	fr.reset()
	return n, nil
}
