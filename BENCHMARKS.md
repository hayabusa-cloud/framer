# Benchmarks

Use Go 1.26+ and record the compiler and platform:

```sh
go version
go env GOOS GOARCH GOWORK
make bench > before.txt
```

The default is ten 100-ms samples per case with GOMAXPROCS=1. The Go test timeout does not bound benchmark runs. Select cases and increase sample duration when needed:

```sh
make bench BENCH='BenchmarkEncodeHeader' BENCHTIME=1s BENCHCOUNT=10
```

Record the revision, dependency versions, OS/kernel, CPU model, affinity, compiler flags and concurrency. On Linux, `uname -srmo` records kernel and platform information. Choose workloads and system settings representative of your deployment; keep them fixed across comparisons. Run race and coverage checks separately from timing.

After applying the change being measured, repeat the same command. If benchstat is installed, compare the captured samples:

```sh
make bench > after.txt
benchstat before.txt after.txt
```

| Benchmark | Work per iteration |
|-----------|--------------------|
| EncodeHeader | One 1/3/8-byte header, in each byte order; compare Framer with direct encoding/binary on the same valid input. The Binary baseline uses a prepared byte-order flag and omits public argument validation. |
| Decoder | Complete one frame, or eight coalesced frames; include empty, 253-byte, 4-KiB, 64-KiB, little-endian and fragmented input. Payload is borrowed, not copied. |
| Read / Write | Complete a 4-KiB or 64-KiB stream frame, or one 4-KiB packet, using reusable bytes.Reader/bytes.Buffer storage. |
| Transfer | ReadFrom, WriteTo or ForwardOnce with reusable buffers; initialize lazy scratch storage before timing. |
| WriteResume | Complete one 4-KiB frame across a partial write and same-message retry after ErrWouldBlock. No waiting or polling. |

Setup is outside B.Loop. Counts and control outcomes are checked during timing; byte comparisons run outside timing. Buffer resets and decoder accounting are included. Header throughput counts header bytes; other cases count logical payload bytes, which do not imply copied or transmitted bandwidth.

Interpret ns/op as average service cost and inspect B/op and allocs/op separately. These single-caller in-memory workloads do not measure socket, queueing, contention or p99.9 latency; measure those boundaries in your application environment. Constructor and first-use costs are excluded; use the allocation regression tests for their separate contracts. The direct-header comparison covers valid prepared inputs and does not establish equal validation work or zero additional abstraction cost.
