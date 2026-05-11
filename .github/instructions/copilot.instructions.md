# framer Review Instructions

## Package

`code.hybscloud.com/framer` provides portable message-boundary preservation for
Go `io.Reader`, `io.Writer`, and connection-like transports.

The package has three protocol modes:

- `BinaryStream`: adds a compact length prefix to byte-stream transports such
  as TCP and Unix stream sockets.
- `SeqPacket`: passes bytes through because the transport already preserves
  packet boundaries.
- `Datagram`: passes bytes through because the transport already preserves
  datagram boundaries.

The stream wire format is:

- payload length `0..253`: one header byte containing the length.
- payload length `254..65535`: header byte `0xFE` plus a 2-byte length.
- payload length `65536..2^56-1`: header byte `0xFF` plus a 7-byte length.

One successful `Reader.Read` returns at most one message. One successful
`Writer.Write` emits exactly one message.

## Review Threshold

Prefer no comment over a weak comment. Report only confirmed, reachable P1 or
P2 issues that break buildability, memory safety, message-boundary
preservation, retry-state soundness, nonblocking control semantics, or the
exported public contract.

Do not report P3, style, naming, formatting, translation, optional coverage, or
speculative design comments.

## Sources Of Truth

Use this file as the complete review baseline. If this file and the current
implementation or exported Go documentation disagree, treat that disagreement
as review evidence and report it only when it creates a concrete P1 or P2
consumer-facing failure.

Do not require any private repository notes, prior review discussions, issue
history, or external instructions to apply this file.

## Output

For each confirmed issue, write one short comment with:

- the exact public operation or file path;
- the reachable failure condition;
- the violated contract from this file;
- the smallest concrete fix.

Do not write overviews, compliments, broad suggestions, or speculative
comments.

## Report Only

Report only these issue classes:

- Build or test failure on a supported target.
- Message-boundary loss, message duplication, or packet replay in a reachable
  public operation.
- Incorrect `ErrWouldBlock`, `ErrMore`, `io.EOF`, `io.ErrUnexpectedEOF`,
  `io.ErrShortWrite`, or `ErrTooLong` behavior under the contracts below.
- Loss of pending retry state after write-side or read-side `ErrWouldBlock` or
  `ErrMore`.
- Partial packet writes that are retried as packet suffixes instead of being
  reported as `io.ErrShortWrite`.
- Transfer-helper read-limit behavior that forwards an oversized packet before
  rejecting it.
- Public documentation that contradicts exported behavior in a way that would
  cause incorrect retry, count, or message-boundary handling.
- A hot-path change that measurably adds heap allocation to an established
  zero-allocation path.

## Do Not Report

- Style, naming, formatting, translation wording, or comment polish.
- Broad requests for more tests unless a confirmed reachable bug lacks a
  regression witness.
- New dependencies, socket creation helpers, runtime integration layers, or API
  expansion.
- Extra defensive validation for invalid inputs when the exported contract
  states caller obligations.
- Packet modes not adding length prefixes; `SeqPacket` and `Datagram` are
  pass-through by contract.
- `ErrWouldBlock` or `ErrMore` being returned as control signals instead of
  ordinary failures.

## Semantic Error Contracts

- `nil`: the operation completed.
- `ErrWouldBlock`: readiness suspension. It is not failure and does not imply
  EOF.
- `ErrMore`: live same-operation continuation. It is not EOF, not readiness
  suspension, and not terminal success.
- `ErrTooLong`: message-size rejection.
- `io.ErrShortWrite`: packet/frame boundary failure after partial destination
  write where suffix retry would violate packet or frame atomicity.

## Retry Contracts

- Arbitrary `io.Writer` destinations use byte-suffix retry.
- Packet-preserving `framer.Writer` destinations retry whole packets only after
  zero-progress `ErrWouldBlock` or `ErrMore`.
- Stream `framer.Writer` destinations retry the same in-flight frame.
- Source errors returned with admitted packet bytes are reported after those
  bytes are emitted.
- Write-side suspension may keep a source signal pending for retry on the same
  operation instance.
- Retry must preserve the active constructor: byte suffix, packet-atomic,
  stream-frame, or control-only source frontier. Do not coerce one constructor
  into another.

## Count Contracts

Progress counts are operation-indexed:

- `Reader.WriteTo` counts bytes written to `dst`.
- `Writer.ReadFrom` counts bytes read from `src` and admitted to the writer
  state.
- `Forwarder.ForwardOnce` counts the active phase. In packet mode, a rejected
  oversized packet may count bytes read from the source, but those bytes must
  not be counted as forwarded bytes.

## Read-Limit Contracts

- `Reader.Read` checks packet `WithReadLimit` after one transport read, so it
  may return `n > limit` with `ErrTooLong`.
- `Reader.WriteTo` and `Forwarder.ForwardOnce` use sentinel capacity to reject
  oversized packets before forwarding bytes.
- Rejected oversized packets must not be forwarded.

## Accepted Low-Level Patterns

- Reusable per-instance scratch buffers for steady-state zero-allocation paths.
- Explicit pending-state fields such as `wtMode`, `wtOff`, `wtLen`, `wtAfter`,
  `rfLen`, `rfAfter`, `sourceAfter`, and `eofPending`.
- Minimal runtime validation on hot paths when the public contract states
  caller obligations.
- Type assertions to known `framer.Writer` destinations for packet/frame retry
  algebra.
