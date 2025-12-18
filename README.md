# framer — message boundaries over stream I/O

[![Go Reference](https://pkg.go.dev/badge/code.hybscloud.com/framer.svg)](https://pkg.go.dev/code.hybscloud.com/framer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hayabusa-cloud/framer)](https://goreportcard.com/report/github.com/hayabusa-cloud/framer)
[![Coverage Status](https://codecov.io/gh/hayabusa-cloud/framer/graph/badge.svg)](https://codecov.io/gh/hayabusa-cloud/framer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Languages:** English | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md) | [Français](README.fr.md)

Portable message framing for Go. Preserve one-message-per-Read/Write over stream transports.

Scope: `framer` solves message boundary preservation across stream transports.

## At a glance

- Solve message boundary problems for byte streams (TCP, Unix stream, pipes).
- Pass-through on boundary-preserving transports (UDP, Unix datagram, WebSocket, SCTP).
- Portable wire format; configurable byte order.

## Why

Many transports are byte streams (TCP, Unix stream, pipes). A single `Read` may return a partial application message, or several messages concatenated. `framer` restores message boundaries: in stream mode, one `Read` returns exactly one message payload, and one `Write` emits exactly one framed message.

## Protocol adaptation

- `BinaryStream` (stream transports: TCP, TLS-over-TCP, Unix stream, pipes): adds a length prefix; reads/writes whole messages.
- `SeqPacket` (e.g., SCTP, WebSocket): pass-through; the transport already preserves boundaries.
- `Datagram` (e.g., UDP, Unix datagram): pass-through; boundary already preserved.

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

## Quick start

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

## Options

- `WithProtocol(proto Protocol)` — choose `BinaryStream`, `SeqPacket`, or `Datagram` (read/write variants available).
- Byte order: `WithByteOrder`, or `WithReadByteOrder` / `WithWriteByteOrder`.
- `WithReadLimit(n int)` — cap maximum message payload size when reading.
- `WithRetryDelay(d time.Duration)` — configure would-block policy; helpers: `WithNonblock()` / `WithBlock()`.

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
| `framer.ErrMore` | Progress made; more completions will follow | Process result, then call again |
| `framer.ErrTooLong` | Message exceeds limit or max wire format | Reject message; possibly fatal |
| `framer.ErrInvalidArgument` | Nil reader/writer or invalid config | Fix configuration |

### Outcome tables

**`Reader.Read(p []byte) (n int, err error)`** — BinaryStream mode

| Condition | n | err |
|-----------|---|-----|
| Complete message delivered | payload length | `nil` |
| `len(p) < payload length` | 0 | `io.ErrShortBuffer` |
| Payload exceeds ReadLimit | 0 | `ErrTooLong` |
| Underlying returns would-block | bytes read so far | `ErrWouldBlock` |
| Underlying returns more | bytes read so far | `ErrMore` |
| EOF at message boundary | 0 | `io.EOF` |
| EOF mid-header or mid-payload | bytes read | `io.ErrUnexpectedEOF` |

**`Writer.Write(p []byte) (n int, err error)`** — BinaryStream mode

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
| Message exceeds internal buffer (64KiB default) | bytes so far | `ErrTooLong` |
| Stream ended mid-message | bytes so far | `io.ErrUnexpectedEOF` |

**`Writer.ReadFrom(src io.Reader) (n int64, err error)`**

| Condition | n | err |
|-----------|---|-----|
| All chunks encoded until src EOF | total payload bytes | `nil` |
| src returns would-block | payload bytes written | `ErrWouldBlock` |
| src returns more | payload bytes written | `ErrMore` |
| Underlying writer returns would-block | payload bytes written | `ErrWouldBlock` |

**`Forwarder.ForwardOnce() (n int, err error)`**

| Condition | n | err |
|-----------|---|-----|
| One message fully forwarded | payload bytes (write phase) | `nil` |
| Packet source returns `(n>0, io.EOF)` | payload bytes (write phase) | `nil` (next call returns `io.EOF`) |
| No more messages | 0 | `io.EOF` |
| Read phase would-block | bytes read this call | `ErrWouldBlock` |
| Write phase would-block | bytes written this call | `ErrWouldBlock` |
| Message exceeds internal buffer | 0 | `io.ErrShortBuffer` |
| Message exceeds ReadLimit | 0 | `ErrTooLong` |
| Stream ended mid-message | bytes so far | `io.ErrUnexpectedEOF` |

### Operation classification

| Operation | Boundary behavior | Use case |
|-----------|-------------------|----------|
| `Reader.Read` | **Message-preserving**: one call = one message | Application-level message processing |
| `Writer.Write` | **Message-preserving**: one call = one framed message | Application-level message sending |
| `Reader.WriteTo` | **Chunking**: streams payload bytes (not wire format) | Efficient bulk transfer; does NOT preserve boundaries |
| `Writer.ReadFrom` | **Chunking**: each src chunk becomes one message | Efficient bulk encoding; does NOT preserve upstream boundaries |
| `Forwarder.ForwardOnce` | **Message-preserving relay**: decode one, re-encode one | Message-aware proxying with boundary preservation |

### Blocking policy

By default, framer is **non-blocking** (`WithNonblock()`): `ErrWouldBlock` is returned immediately.

- `WithBlock()` — yield (`runtime.Gosched`) and retry on would-block
- `WithRetryDelay(d)` — sleep `d` and retry on would-block
- Negative `RetryDelay` (default) — return `ErrWouldBlock` immediately

No method hides blocking unless explicitly configured.

## Fast paths

`framer` implements stdlib copy fast paths to interoperate with `io.Copy`-style engines and `iox.CopyPolicy`:

- `(*Reader).WriteTo(io.Writer)` — efficiently transfers framed message payloads to `dst`.
  - Stream (`BinaryStream`): processes one framed message at a time and writes only the payload bytes to `dst`. If `ReadLimit == 0`, an internal default cap (64KiB) is used; messages larger than this cap return `framer.ErrTooLong`.
  - Packet (`SeqPacket`/`Datagram`): pass-through (reads bytes, writes bytes).
  - Semantic errors `framer.ErrWouldBlock` and `framer.ErrMore` are propagated unchanged with the progress count reflecting bytes written.

- `(*Writer).ReadFrom(io.Reader)` — chunk-to-message: each successful `Read` chunk from `src` is encoded as a single framed message.
  - This is efficient but does not preserve application message boundaries from `src`.
  - On boundary-preserving protocols it effectively behaves like pass-through.
  - Semantic errors `framer.ErrWouldBlock` and `framer.ErrMore` are propagated unchanged with progress counts.

Recommendation: prefer `iox.CopyPolicy` with a retry-aware policy (e.g., `PolicyRetry`) in non-blocking loops so `ErrWouldBlock` / `ErrMore` are handled explicitly.

**Note on partial write recovery:** When using `iox.Copy` with non-blocking destinations, partial writes may occur. If the source does not implement `io.Seeker`, `iox.Copy` returns `iox.ErrNoSeeker` to prevent silent data loss. For non-seekable sources (e.g., network sockets), use `iox.CopyPolicy` with `PolicyRetry` for write-side semantic errors to ensure all read bytes are written before returning.

## Forwarding

- Wire proxying (byte engines): use `iox.CopyPolicy` and standard `io` fast paths (`WriterTo`/`ReaderFrom`). This maximizes throughput when you don't need to preserve higher-level boundaries.
- Message relay (preserve boundaries): use `framer.NewForwarder(dst, src, ...)` and call `ForwardOnce()` in your poll loop. It decodes exactly one framed message from `src` and re-encodes it as exactly one framed message to `dst`.
  - Non-blocking semantics: `ForwardOnce` returns `(n>0, framer.ErrWouldBlock|framer.ErrMore)` when partial progress happened; retry the same `Forwarder` instance later to complete.
  - Limits: `io.ErrShortBuffer` when the internal buffer is too small for the message; `framer.ErrTooLong` when a message exceeds the configured `WithReadLimit`.
  - Zero‑alloc steady state after construction; the internal scratch buffer is reused per message.

## License

MIT — see `LICENSE`.
