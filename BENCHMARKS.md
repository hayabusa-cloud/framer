# Benchmarks

Use Go 1.27 or later. `make bench` runs ten 200-ms samples per case with `GOMAXPROCS=1` and reports heap bytes and allocations per operation. Set `GOWORK=off` to measure the dependencies declared in `go.mod`.

```sh
go version
go env GOOS GOARCH GOWORK
make bench > before.txt
```

## Small and large messages

`WriteEqualPayload` reads the same 64 KiB source block in two ways. One operation writes either 2,048 consecutive 32-byte frames or one 65,536-byte frame. The small frames add 2,048 header bytes in total. The large frame adds eight. Compare `frames/s` and `ns/frame` for per-message cost, and `MB/s` for logical payload throughput.

```sh
make bench BENCH='^BenchmarkWriteEqualPayload$/^size=(32|65536)$/^peers=(1|64)$/^Framer$'
```

Each frame goes to the next peer in turn, continuing across operations. Each peer has its own writer and pre-touched `bytes.Buffer`. After timing, the benchmark checks each peer's bytes in send order. The `Direct` cases use `encoding/binary` with the same valid payloads, header widths, sink copies and completion checks. The public writer also supports validation and resumable writes. A full rotation of 1,024 large frames writes 64 MiB of payload across the destination buffers. Compare that working set with the last-level cache on your machine.

| Benchmark | Work measured |
|---|---|
| `Read`, `Write` | A single 32-byte, 4-KiB or 64-KiB stream message, or a 4-KiB packet on a boundary-preserving transport. `Read/Stream64KIn4KReads` caps each underlying read at 4 KiB while copying into the caller's buffer. |
| `Decoder` | Borrow payload from complete or fragmented input. Compare eight 32-byte frames supplied separately or together, and a 64-KiB frame whole or in 4-KiB chunks. Cases also cover empty messages and the 253/254-byte and 65,535/65,536-byte header transitions. |
| `DecoderPeers` | Complete 1,024 split frames per operation with one owner and 1, 64 or 1,024 active peers. Begin a frame on each peer before revisiting it for completion. |
| `DecoderParallel32` | Each Go benchmark worker owns a decoder and finishes a 32-byte frame in two calls. Vary `BENCHCPU` to measure aggregate throughput as workers increase. |
| `Transfer`, `WriteResume` | Complete a `ReadFrom`, `WriteTo` or `ForwardOnce` transfer, or finish a partial stream write by retrying the same message after `ErrWouldBlock`. |
| `EncodeHeader` | Encode one 1-, 3- or 8-byte header in either byte order. `MB/s` counts header bytes. The direct Go comparison uses prepared valid inputs. |

For `Decoder`, `MB/s` counts payload covered by borrowed slices. `Read` and `WriteEqualPayload` copy payload. `frames/op` counts completed messages, including empty frames. For a batch, divide `B/op` and `allocs/op` by `frames/op` to obtain averages per frame. The parallel benchmark includes worker setup and finalization. Its `ns/frame` is wall time divided by aggregate completions. Receiver delivery and queueing latency require a transport workload with a receiver and an offered load.

## Setup and memory

`Transfer/FirstReadFrom32K` constructs a writer and allocates its scratch buffer during each operation. `Transfer/ReadFrom32K` reuses both. `ForwarderSetup` reports construction cost per peer and estimates `live-B/peer` from the change in process-wide live heap across 64 reachable default stream forwarders after GC. Run this case in isolation for memory comparisons. The allocation tests also check fresh and warmed operations.

```sh
make bench BENCH='^BenchmarkTransfer$/^(FirstReadFrom32K|ReadFrom32K)$'
make bench BENCH='^BenchmarkForwarderSetup$'
make bench BENCH='^BenchmarkDecoderParallel32$' BENCHCPU=1,2,4
```

## CPU and comparisons

For process CPU on Linux, time a compiled binary over a fixed number of batches. Add user and system seconds, then divide by completed frames or payload GiB. Process time includes startup, setup, GC and final checks. Choose a run long enough to amortize them.

```sh
out=$(mktemp -d)
go test -c -o "$out/framer.test" .
/usr/bin/time -f 'user=%U s system=%S s' "$out/framer.test" \
  -test.run='^$' -test.bench='^BenchmarkWriteEqualPayload$/^size=65536$/^peers=1$/^Framer$' \
  -test.benchtime=100000x -test.cpu=1
```

Record the source and dependency revisions. Keep the compiler, CPU affinity, kernel, power settings and workloads fixed between runs. Run race and coverage checks separately. Increase `BENCHTIME` for longer samples, then compare repeated results with [benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat).

```sh
make bench > after.txt
benchstat before.txt after.txt
```
