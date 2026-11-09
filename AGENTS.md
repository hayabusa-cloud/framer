# framer

Portable message framing over Go I/O, with a borrowed-input decoder and caller-owned header encoding. Go 1.26+.

- Preserve the 1/3/8-byte BinaryStream wire format; SeqPacket and Datagram preserve transport boundaries without prefixes.
- Callers serialize mutable instances and retain buffers through the documented lifetime. Keep progress, completion and buffer ownership distinct.
- Process counts before errors. Resume the same operation after ErrWouldBlock or ErrMore using its documented buffer/message rules; neither signal is EOF. See [public contracts](README.md).
- Keep prepared steady-state paths allocation-free. Separate setup, copying, borrowed decoding and transport scheduling in measurements.
- Use black-box tests, benchmark completed work, and preserve all README translations when public contracts change.
- Document how users measure performance in their own environments; omit benchmark result reports.

Verify with `go test -timeout=120s -race -tags examples ./...`, `go vet ./...`, and `make bench`. The [benchmark guide](BENCHMARKS.md) defines workloads, comparison limits and repeatable commands.
