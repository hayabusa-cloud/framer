# framer — message boundaries over stream I/O

[![Go Reference](https://pkg.go.dev/badge/code.hybscloud.com/framer.svg)](https://pkg.go.dev/code.hybscloud.com/framer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hayabusa-cloud/framer)](https://goreportcard.com/report/github.com/hayabusa-cloud/framer)
[![Coverage Status](https://codecov.io/gh/hayabusa-cloud/framer/graph/badge.svg)](https://codecov.io/gh/hayabusa-cloud/framer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Languages:** English | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md) | [Français](README.fr.md)

Portable message framing for Go. Preserve one-message-per-Read/Write over stream transports.

Scope: message boundary preservation for stream transports.

## Overview

Many transports are byte streams (TCP, Unix stream, pipes). A single `Read` may return a partial application message, or several messages concatenated. `framer` restores message boundaries: in stream mode, one `Read` returns exactly one message payload, and one `Write` emits exactly one framed message.

- Message boundary preservation for byte streams (TCP, Unix stream, pipes).
- Pass-through on boundary-preserving transports (UDP, Unix datagram, WebSocket, SCTP).
- Portable wire format; configurable byte order.

## Protocol adaptation

- `BinaryStream` (stream transports: TCP, TLS-over-TCP, Unix stream, pipes): adds a length prefix; reads/writes whole messages.
- `SeqPacket` (e.g., SCTP, WebSocket): pass-through; the transport already preserves boundaries.
- `Datagram` (e.g., UDP, Unix datagram): pass-through; boundary already preserved.
- For `Reader.Read`, packet modes are pass-through: `WithReadLimit` is checked after one receive, so an oversized packet may return `n > limit` with `ErrTooLong`; `n` is the bytes copied from that packet.
- Packet-output paths retry whole packets only after zero-progress `ErrWouldBlock` / `ErrMore`; a fully accepted packet returned with `ErrWouldBlock` or `ErrMore` is not replayed, and partial packet writes are reported as `io.ErrShortWrite`.

Select at construction time via `WithProtocol(...)` (read/write variants exist) or via transport helpers (see Options).

## Wire format

Compact variable-length length prefix, followed by payload bytes. Byte order for the extended length is configurable: `WithByteOrder`, or per-direction `WithReadByteOrder` / `WithWriteByteOrder`.

## Frame data format

The framing scheme used by `framer` is intentionally compact:

- Header byte `H0` + optional extended length bytes.
- Let `L` be the payload length in bytes.
  - If `0 ≤ L ≤ 253` (`0x00..0xFD`): `H0 = L`. No extra length bytes.
  - If `254 ≤ L ≤ 65535` (`0x0000..0xFFFF`): `H0 = 0xFE` and the next 2 bytes encode `L` as an unsigned 16‑bit integer in the configured byte order.
  - If `65536 ≤ L ≤ 2^56-1`: `H0 = 0xFF` and the next 7 bytes carry `L` as a 56‑bit integer, laid out in the configured byte order.
    - Big‑endian: bytes `[1..7]` are the big‑endian lower 56 bits of `L`.
    - Little‑endian: bytes `[1..7]` are the little‑endian lower 56 bits of `L`.

Limits and errors:
- The maximum supported payload length is `2^56-1`; larger values result in `framer.ErrTooLong`.
- When a read‑side limit is configured (`WithReadLimit`), lengths exceeding the limit fail with `framer.ErrTooLong`.

## Installation

Install with `go get`:
```shell
go get code.hybscloud.com/framer
```

```go
c1, c2 := net.Pipe()
defer c1.Close()
defer c2.Close()

w := framer.NewWriter(c1, framer.WithWriteTCP())
r := framer.NewReader(c2, framer.WithReadTCP())

go func() { _, _ = w.Write([]byte("hello")) }()

buf := make([]byte, 64)
n, err := r.Read(buf)
if err != nil {
	panic(err)
}
fmt.Printf("got: %q\n", buf[:n])
```

## Non-blocking usage

`framer` defaults to non-blocking mode. In an event-driven loop:

```go
for {
	n, err := r.Read(buf)
	if n > 0 {
		process(buf[:n])
	}
	if err != nil {
		if err == framer.ErrWouldBlock {
			// No data now; wait for readability (epoll, io_uring, etc.)
			continue
		}
		if err == io.EOF {
			break
		}
		log.Fatal(err)
	}
}
```

## Options

- `WithProtocol(proto Protocol)`: choose `BinaryStream`, `SeqPacket`, or `Datagram` (read/write variants available).
- Byte order: `WithByteOrder`, or `WithReadByteOrder` / `WithWriteByteOrder`.
- `WithReadLimit(n int)`: cap maximum message payload size when reading; `Reader.Read` enforces it post-read in packet modes and may return `n > limit` with `ErrTooLong`.
- `WithRetryDelay(d time.Duration)`: configure zero-progress `ErrWouldBlock` policy; a negative value returns `ErrWouldBlock` immediately, zero yields and retries, and a positive value sleeps for `d` before retrying. If an operation already transferred bytes, it returns the positive count with `ErrWouldBlock` so the caller can process progress before retrying; related options: `WithNonblock()` / `WithBlock()`.

Transport helpers (presets):
- `WithReadTCP` / `WithWriteTCP` (BinaryStream, network‑order BigEndian)
- `WithReadUDP` / `WithWriteUDP` (Datagram, BigEndian)
- `WithReadWebSocket` / `WithWriteWebSocket` (SeqPacket, BigEndian)
- `WithReadSCTP` / `WithWriteSCTP` (SeqPacket, BigEndian)
- `WithReadUnix` / `WithWriteUnix` (BinaryStream, BigEndian)
- `WithReadUnixPacket` / `WithWriteUnixPacket` (Datagram, BigEndian)
- `WithReadLocal` / `WithWriteLocal` (BinaryStream, native byte order)

Everything else: see GoDoc: https://pkg.go.dev/code.hybscloud.com/framer

## Semantics Contract

### Packet mode note (`SeqPacket` / `Datagram`)

- Packet mode preserves transport boundaries and does not split packets.
- `Reader.Read` enforces `WithReadLimit` after one packet read; transfer helpers use one sentinel byte to reject oversized packets before forwarding bytes.
- Packet-preserving destinations retry the whole packet only after zero-progress `ErrWouldBlock` / `ErrMore`; a fully accepted packet returned with `ErrWouldBlock` or `ErrMore` is not replayed, and partial packet writes are boundary failures reported as `io.ErrShortWrite`.
- `Reader.WriteTo` to an arbitrary `io.Writer` is a byte-copy transfer with suffix resume. When the destination is a `framer.Writer`, it uses the destination algebra: packet writers retry the whole packet after zero progress, and stream writers retry the same in-flight frame.
- If a packet source returns `(n > 0, err)`, `Reader.WriteTo` emits the admitted packet before reporting `err`; write-side suspension keeps that source signal pending across retry.
- Progress counts are operation-indexed: `Reader.Read` reports bytes copied into `p`, `Reader.WriteTo` reports bytes written to `dst`, `Writer.ReadFrom` reports bytes read from `src` and admitted to the writer state, and `Forwarder.ForwardOnce` reports progress in its current phase.

### Retry discipline

- `ErrWouldBlock` is readiness suspension, not failure; aggregate helpers may return a positive count when earlier loop steps made progress before suspension.
- `ErrMore` means the same operation has more progress to deliver; it is not `io.EOF` and not readiness suspension. Process any returned progress, then call the same operation again.
- Retry `Reader.Read` after partial stream progress on the same `Reader` with the same buffer.
- Retry `Writer.Write` after BinaryStream suspension on the same `Writer` with the same message length; BinaryStream header bytes are not included in `n`. In packet modes, `n == len(p)` with `ErrWouldBlock` or `ErrMore` means the packet was accepted, so do not replay `p`.
- Retry `Reader.WriteTo` on the same `Reader` and same destination, `Writer.ReadFrom` on the same `Writer`, and `Forwarder.ForwardOnce` on the same `Forwarder`.

### Performance contract

- Hot paths keep runtime checks minimal for steady-state throughput.
- Callers are responsible for valid option/buffer usage and operation-specific retry after `ErrWouldBlock` or `ErrMore`.

### Error taxonomy

| Error | Meaning | Caller action |
|-------|---------|---------------|
| `nil` | Operation completed successfully | Proceed; `n` reflects full progress |
| `io.EOF` | End of stream (no more messages) | Stop reading; normal termination |
| `io.ErrUnexpectedEOF` | Stream ended mid-message (header or payload incomplete) | Treat as fatal; data corruption or disconnect |
| `io.ErrShortBuffer` | Destination buffer too small for message payload | Retry with larger buffer |
| `io.ErrShortWrite` | Destination accepted fewer bytes than provided | Retry or treat as fatal per context |
| `io.ErrNoProgress` | Underlying Reader made no progress (`n==0, err==nil`) on a non-empty buffer | Treat as fatal; indicates a broken `io.Reader` implementation |
| `framer.ErrWouldBlock` | No progress possible now without waiting | Retry later (after poll/event); `n` may be >0 |
| `framer.ErrMore` | Same operation has more progress to deliver, distinct from EOF and readiness suspension | Process returned progress, then call the same operation again |
| `framer.ErrTooLong` | Message exceeds a configured limit, transfer cap, or wire-format bound | Reject message; possibly fatal |
| `framer.ErrInvalidArgument` | Nil reader/writer or invalid config | Fix configuration |

### Outcome tables

**`Reader.Read(p []byte) (n int, err error)`** (BinaryStream mode)

| Condition | n | err |
|-----------|---|-----|
| Complete message delivered | payload length | `nil` |
| `len(p) < payload length` | 0 | `io.ErrShortBuffer` |
| Payload exceeds ReadLimit | 0 | `ErrTooLong` |
| Underlying returns would-block | bytes read so far | `ErrWouldBlock` |
| Underlying returns more | bytes read so far | `ErrMore` |
| EOF at message boundary | 0 | `io.EOF` |
| EOF mid-header or mid-payload | bytes read | `io.ErrUnexpectedEOF` |

**`Writer.Write(p []byte) (n int, err error)`** (BinaryStream mode)

| Condition | n | err |
|-----------|---|-----|
| Complete framed message emitted | `len(p)` | `nil` |
| Payload exceeds max (2^56-1) | 0 | `ErrTooLong` |
| Underlying returns would-block | payload bytes written so far | `ErrWouldBlock` |
| Underlying returns more | payload bytes written so far | `ErrMore` |

**`Reader.WriteTo(dst io.Writer) (n int64, err error)`**

| Condition | n | err |
|-----------|---|-----|
| All messages transferred until EOF | total payload bytes | `nil` |
| Underlying reader returns would-block | payload bytes written | `ErrWouldBlock` |
| Underlying reader returns more | payload bytes written | `ErrMore` |
| dst returns would-block | payload bytes written | `ErrWouldBlock` |
| Packet source exceeds ReadLimit before forwarding | bytes already written before that packet | `ErrTooLong` |
| Message exceeds internal buffer (64KiB default) | bytes so far | `ErrTooLong` |
| Stream ended mid-message | bytes so far | `io.ErrUnexpectedEOF` |

**`Writer.ReadFrom(src io.Reader) (n int64, err error)`**

| Condition | n | err |
|-----------|---|-----|
| All chunks encoded until src EOF | total bytes read from src | `nil` |
| src returns would-block | bytes read from src before the signal | `ErrWouldBlock` |
| src returns more | bytes read from src before the signal | `ErrMore` |
| Underlying writer returns would-block | bytes read from src and admitted before suspension; 0 on pure write-side resume | `ErrWouldBlock` |
| Underlying writer returns more | bytes read from src and admitted before suspension; 0 on pure write-side resume | `ErrMore` |

**`Forwarder.ForwardOnce() (n int, err error)`**

| Condition | n | err |
|-----------|---|-----|
| One message fully forwarded | payload bytes (write phase) | `nil` |
| Packet source returns `(n > 0, io.EOF)` | payload bytes (write phase) | `nil` (next call returns `io.EOF`) |
| No more messages | 0 | `io.EOF` |
| Source returns would-block | read bytes if no packet was emitted; packet-source `n > 0` is emitted first and returns payload bytes (write phase) | `ErrWouldBlock` |
| Source returns more | read bytes if no packet was emitted; packet-source `n > 0` is emitted first and returns payload bytes (write phase) | `ErrMore` |
| Write phase would-block | bytes written this call | `ErrWouldBlock` |
| Write phase more | bytes written this call | `ErrMore` |
| Stream message or required packet read capacity exceeds internal buffer | 0 | `io.ErrShortBuffer` |
| Packet exceeds ReadLimit/default packet transfer cap before forwarding | bytes read from the packet, not forwarded | `ErrTooLong` |
| Stream ended mid-message | bytes so far | `io.ErrUnexpectedEOF` |

### Operation classification

| Operation | Boundary behavior | Use case |
|-----------|-------------------|----------|
| `Reader.Read` | **Message-preserving**: one call = one message | Application-level message processing |
| `Writer.Write` | **Message-preserving**: one call = one framed message | Application-level message sending |
| `Reader.WriteTo` | **Byte transfer** to arbitrary writers; known `framer` destinations preserve packet/frame retry law | Efficient bulk transfer with suffix resume |
| `Writer.ReadFrom` | **Chunking**: each src chunk becomes one message; packet output is whole-packet retry only after zero progress | Efficient bulk encoding; does NOT preserve upstream boundaries |
| `Forwarder.ForwardOnce` | **Message-preserving relay**: decode one, re-encode one | Message-aware proxying with boundary preservation |

### Blocking policy

By default, framer is **non-blocking** (`WithNonblock()`): `ErrWouldBlock` is returned immediately.

- `WithBlock()` or `WithRetryDelay(0)`: yield (`runtime.Gosched`) and retry zero-progress would-block
- `WithRetryDelay(d > 0)`: sleep `d` and retry zero-progress would-block
- Negative `RetryDelay` (default): return zero-progress `ErrWouldBlock` immediately
- If a read or write already transferred bytes, framer returns the positive count with `ErrWouldBlock`; process the progress and retry the same operation as documented above.

No method hides blocking unless explicitly configured.

`framer` uses `code.hybscloud.com/iox` control flow signals. `ErrWouldBlock` and `ErrMore` are aliases from `iox`, enabling direct integration with other `iox`-aware components (`iofd`, `takt`).

## Fast paths

`framer` implements stdlib copy fast paths to interoperate with `io.Copy`-style engines and `iox.CopyPolicy`:

- `(*Reader).WriteTo(io.Writer)`: efficiently transfers framed message payloads to `dst`.
  - Stream (`BinaryStream`): processes one framed message at a time and writes only the payload bytes to `dst`. If `ReadLimit == 0`, an internal default cap (64KiB) is used; messages larger than this cap return `framer.ErrTooLong`.
  - Packet (`SeqPacket`/`Datagram`): pass-through byte transfer; sentinel-cap reads reject oversized packets before forwarding, and `n` counts bytes written to `dst`.
  - Semantic write-side errors `framer.ErrWouldBlock` and `framer.ErrMore` are propagated unchanged with the progress count reflecting bytes written; packet-source errors returned with bytes are reported after the admitted packet is emitted.

- `(*Writer).ReadFrom(io.Reader)`: chunk-to-message; each successful `Read` chunk from `src` is encoded as a single framed message.
  - This is efficient but does not preserve application message boundaries from `src`.
  - On boundary-preserving protocols it effectively behaves like pass-through.
  - Semantic errors `framer.ErrWouldBlock` and `framer.ErrMore` are propagated unchanged; `n` counts bytes read from `src` and admitted to the writer state.

Recommendation: prefer `iox.CopyPolicy` with a retry-aware policy (e.g., `PolicyRetry`) in non-blocking loops so `ErrWouldBlock` / `ErrMore` are handled explicitly.

**Zero-allocation steady state**: After initial buffer allocation, `Forwarder` and `WriteTo` paths reuse internal buffers. No heap allocations occur per message in steady state.

**Note on partial write recovery:** When using `iox.Copy` with non-blocking destinations, partial writes may occur. If the source does not implement `io.Seeker`, `iox.Copy` returns `iox.ErrNoSeeker` to prevent silent data loss. For non-seekable sources (e.g., network sockets), use `iox.CopyPolicy` with `PolicyRetry` for write-side semantic errors to ensure all read bytes are written before returning.

## Forwarding

- Wire proxying (byte engines): use `iox.CopyPolicy` and standard `io` fast paths (`WriterTo`/`ReaderFrom`) when byte-level forwarding is acceptable and higher-level boundaries do not need preservation.
- Message relay (preserve boundaries): use `framer.NewForwarder(dst, src, ...)` and call `ForwardOnce()` in your poll loop. It decodes exactly one framed message from `src` and re-encodes it as exactly one framed message to `dst`.
  - Non-blocking semantics: retry the same `Forwarder` instance after `framer.ErrWouldBlock` or `framer.ErrMore`; packet-source `(n > 0, err)` is emitted before the source signal is reported, and write-side suspension keeps that source signal pending for the later retry on the same `Forwarder`.
  - Limits: `io.ErrShortBuffer` when the internal buffer is too small for a stream message or required packet read capacity; `framer.ErrTooLong` when a packet exceeds `WithReadLimit` or the default packet transfer cap before forwarding.
  - Zero‑alloc steady state after construction; the internal scratch buffer is reused per message.

Message relay example:

```go
fwd := framer.NewForwarder(dst, src, framer.WithReadTCP(), framer.WithWriteTCP())

for {
	_, err := fwd.ForwardOnce()
	if err != nil {
		if err == framer.ErrWouldBlock {
			continue // wait for src readable or dst writable
		}
		if err == io.EOF {
			break
		}
		log.Fatal(err)
	}
}
```

## License

MIT — see [LICENSE](LICENSE).

©2025 [Hayabusa Cloud Co., Ltd.](https://code.hybscloud.com)
