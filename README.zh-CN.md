# framer — 在流式 I/O 上保留消息边界

[![Go Reference](https://pkg.go.dev/badge/code.hybscloud.com/framer.svg)](https://pkg.go.dev/code.hybscloud.com/framer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hayabusa-cloud/framer)](https://goreportcard.com/report/github.com/hayabusa-cloud/framer)
[![Coverage Status](https://codecov.io/gh/hayabusa-cloud/framer/graph/badge.svg)](https://codecov.io/gh/hayabusa-cloud/framer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**语言 / Languages:** [English](README.md) | 简体中文 | [日本語](README.ja.md) | [Español](README.es.md) | [Français](README.fr.md)

用于 Go 的可移植消息分帧库。在流式传输上实现“一次 `Read` / `Write` 对应一条消息”。

范围：流式传输的消息边界保持。

## 概述

许多传输是字节流（TCP、Unix stream、pipe）。一次 `Read` 可能只返回应用消息的一部分，也可能把多个消息拼在一起返回。`framer` 恢复消息边界：在 stream 模式下，一次 `Read` 只返回一条消息的载荷；一次 `Write` 只发送一条（带长度前缀的）消息。

- 字节流（TCP、Unix stream、pipe）的消息边界保持。
- 对天然保留边界的传输（UDP、Unix datagram、WebSocket、SCTP）做透传。
- 可移植线格式；可配置字节序。

## 协议适配

- `BinaryStream`（流式传输：TCP、TLS-over-TCP、Unix stream、pipe）：添加长度前缀；读/写整条消息。
- `SeqPacket`（例如 SCTP、WebSocket）：透传；底层已保留边界。
- `Datagram`（例如 UDP、Unix datagram）：透传；底层已保留边界。
- 对于 `Reader.Read`，分组模式按设计透传：`WithReadLimit` 在一次接收后检查，超大分组可能返回 `n > limit` 与 `ErrTooLong`；`n` 表示从该分组复制出的字节数。
- 分组输出路径只会在零进度 `ErrWouldBlock` / `ErrMore` 后重试整个分组；带 `ErrWouldBlock` 或 `ErrMore` 返回的完整分组接纳不会重放，部分分组写入会报告为 `io.ErrShortWrite`。

可在构造时通过 `WithProtocol(...)` 选择（读/写方向也有独立选项），或使用传输辅助选项（见“选项”）。

## 线格式

紧凑的可变长度前缀 + 载荷字节。扩展长度的字节序可配置：`WithByteOrder`，或按方向 `WithReadByteOrder` / `WithWriteByteOrder`。

## 帧数据格式

`framer` 的分帧格式刻意保持紧凑：

- 首字节 `H0` + 可选的扩展长度字节。
- 设 `L` 为载荷长度（字节数）。
  - 若 `0 ≤ L ≤ 253`（`0x00..0xFD`）：`H0 = L`。无额外长度字节。
  - 若 `254 ≤ L ≤ 65535`（`0x0000..0xFFFF`）：`H0 = 0xFE`，后续 2 字节以配置的字节序编码无符号 16 位整数 `L`。
  - 若 `65536 ≤ L ≤ 2^56-1`：`H0 = 0xFF`，后续 7 字节以配置的字节序承载 `L` 的低 56 位。
    - Big-endian：字节 `[1..7]` 为 `L` 的低 56 位的大端表示。
    - Little-endian：字节 `[1..7]` 为 `L` 的低 56 位的小端表示。

限制与错误：
- 最大支持的载荷长度为 `2^56-1`；更大的值返回 `framer.ErrTooLong`。
- 配置读侧限制（`WithReadLimit`）时，超过该限制的长度返回 `framer.ErrTooLong`。

## 安装

使用 `go get` 安装：
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

## 非阻塞用法

`framer` 默认为非阻塞模式。在事件驱动循环中：

```go
for {
	n, err := r.Read(buf)
	if n > 0 {
		process(buf[:n])
	}
	if err != nil {
		if err == framer.ErrWouldBlock {
			// 当前无数据；等待可读（epoll、io_uring 等）
			continue
		}
		if err == io.EOF {
			break
		}
		log.Fatal(err)
	}
}
```

## 选项

- `WithProtocol(proto Protocol)`：选择 `BinaryStream`、`SeqPacket` 或 `Datagram`（读/写方向也有独立选项）。
- 字节序：`WithByteOrder`，或 `WithReadByteOrder` / `WithWriteByteOrder`。
- `WithReadLimit(n int)`：限制读取时允许的最大消息载荷；`Reader.Read` 在分组模式中读后检查，可能返回 `n > limit` 与 `ErrTooLong`。
- `WithRetryDelay(d time.Duration)`：设置零进度 `ErrWouldBlock` 策略；负值立即返回 `ErrWouldBlock`，零值让出执行权并重试，正值等待 `d` 后重试。如果操作已经传输了字节，则返回正计数和 `ErrWouldBlock`，让调用方先处理进度再重试；相关选项：`WithNonblock()` / `WithBlock()`。

传输辅助选项（便捷预设）：
- `WithReadTCP` / `WithWriteTCP`（`BinaryStream`，网络序 BigEndian）
- `WithReadUDP` / `WithWriteUDP`（`Datagram`，BigEndian）
- `WithReadWebSocket` / `WithWriteWebSocket`（`SeqPacket`，BigEndian）
- `WithReadSCTP` / `WithWriteSCTP`（`SeqPacket`，BigEndian）
- `WithReadUnix` / `WithWriteUnix`（`BinaryStream`，BigEndian）
- `WithReadUnixPacket` / `WithWriteUnixPacket`（`Datagram`，BigEndian）
- `WithReadLocal` / `WithWriteLocal`（`BinaryStream`，本机字节序）

更多内容：GoDoc https://pkg.go.dev/code.hybscloud.com/framer

## 语义契约

### 分组模式说明（`SeqPacket` / `Datagram`）

- 分组模式保留底层边界，不做分片。
- `Reader.Read` 在单次分组读取后执行 `WithReadLimit`；传输辅助路径使用一个哨兵字节，在转发字节前拒绝超限分组。
- 保留分组边界的目标只会在零进度 `ErrWouldBlock` / `ErrMore` 后重试整个分组；带 `ErrWouldBlock` 或 `ErrMore` 返回的完整分组接纳不会重放，部分分组写入是边界失败，并报告为 `io.ErrShortWrite`。
- `Reader.WriteTo` 面向任意 `io.Writer` 时是带后缀恢复的字节复制传输。目标是 `framer.Writer` 时使用目标代数：分组写入器在零进度后重试整个分组，流式写入器重试同一个进行中的帧。
- 如果分组源返回 `(n > 0, err)`，`Reader.WriteTo` 会先输出已接纳的分组，再报告 `err`；写侧挂起时会把该源信号保留到重试。
- 进度计数按操作定义：`Reader.Read` 报告复制到 `p` 的字节数，`Reader.WriteTo` 报告写入 `dst` 的字节数，`Writer.ReadFrom` 报告从 `src` 读取并纳入写入器状态的字节数，`Forwarder.ForwardOnce` 报告当前阶段的进度。

### 重试规则

- `ErrWouldBlock` 是就绪挂起，不是失败；如果同一次聚合调用中的先前循环步骤已经推进，聚合路径可能返回正计数。
- `ErrMore` 表示同一操作还有后续进度；它不是 `io.EOF`，也不是就绪挂起。先处理返回的进度，再次调用同一操作。
- `Reader.Read` 在流式模式部分进度后需要使用同一个 `Reader` 和同一个缓冲区重试。
- `Writer.Write` 在 BinaryStream 挂起后需要使用同一个 `Writer` 和相同消息长度重试；BinaryStream 头部字节不计入 `n`。在分组模式下，`n == len(p)` 且返回 `ErrWouldBlock` 或 `ErrMore` 表示该分组已接纳，不要重放 `p`。
- `Reader.WriteTo` 需要在同一个 `Reader` 和同一个目标上重试，`Writer.ReadFrom` 需要在同一个 `Writer` 上重试，`Forwarder.ForwardOnce` 需要在同一个 `Forwarder` 上重试。

### 性能约定

- 热路径为了稳态吞吐保持最少运行时检查。
- 调用方负责保证选项/缓冲参数有效，并在 `ErrWouldBlock` / `ErrMore` 后按操作规则重试。

### 错误分类

| Error | 含义 | 调用方动作 |
|-------|------|-----------|
| `nil` | 操作成功完成 | 继续；`n` 反映全部进度 |
| `io.EOF` | 流结束（不再有消息） | 停止读取；正常终止 |
| `io.ErrUnexpectedEOF` | 在一条消息中途结束（头部或载荷不完整） | 视为致命错误；可能是损坏或断连 |
| `io.ErrShortBuffer` | 目标缓冲区不足以容纳载荷 | 使用更大的缓冲区重试 |
| `io.ErrShortWrite` | 目标 Writer 接受的字节数小于提供值 | 视场景重试或视为致命 |
| `io.ErrNoProgress` | 底层 Reader 在非空缓冲区上返回了无进度（`n==0, err==nil`） | 视为致命；表示底层 `io.Reader` 实现有问题 |
| `framer.ErrWouldBlock` | 当前无法继续推进而不等待 | 稍后重试（在 poll/event 之后）；`n` 可能 >0 |
| `framer.ErrMore` | 同一操作还有后续进度，区别于 EOF 和就绪挂起 | 处理返回的进度，然后再次调用同一操作 |
| `framer.ErrTooLong` | 消息超过配置限制、传输上限或线格式上限 | 拒绝消息；可能需要终止连接 |
| `framer.ErrInvalidArgument` | reader/writer 为 nil 或配置非法 | 修正配置 |

### 结果表

**`Reader.Read(p []byte) (n int, err error)`**（BinaryStream 模式）

| 条件 | n | err |
|------|---|-----|
| 完整交付一条消息 | 载荷长度 | `nil` |
| `len(p) < 载荷长度` | 0 | `io.ErrShortBuffer` |
| 载荷超过 ReadLimit | 0 | `ErrTooLong` |
| 底层返回 `ErrWouldBlock` | 已读取字节数 | `ErrWouldBlock` |
| 底层返回 more | 已读取字节数 | `ErrMore` |
| 在消息边界处 EOF | 0 | `io.EOF` |
| 头部/载荷中途 EOF | 已读取字节数 | `io.ErrUnexpectedEOF` |

**`Writer.Write(p []byte) (n int, err error)`**（BinaryStream 模式）

| 条件 | n | err |
|------|---|-----|
| 完整发送一条带前缀的消息 | `len(p)` | `nil` |
| 载荷超过最大值（2^56-1） | 0 | `ErrTooLong` |
| 底层返回 `ErrWouldBlock` | 已写入的载荷字节数 | `ErrWouldBlock` |
| 底层返回 more | 已写入的载荷字节数 | `ErrMore` |

**`Reader.WriteTo(dst io.Writer) (n int64, err error)`**

| 条件 | n | err |
|------|---|-----|
| 直到 EOF 传输完成 | 总载荷字节数 | `nil` |
| 底层 reader 返回 `ErrWouldBlock` | 已写入的载荷字节数 | `ErrWouldBlock` |
| 底层 reader 返回 more | 已写入的载荷字节数 | `ErrMore` |
| dst 返回 `ErrWouldBlock` | 已写入的载荷字节数 | `ErrWouldBlock` |
| 分组源在转发前超过 ReadLimit | 该分组之前已写出的字节数 | `ErrTooLong` |
| 消息超过内部缓冲（默认 64KiB） | 当前累计字节数 | `ErrTooLong` |
| 流在消息中途结束 | 当前累计字节数 | `io.ErrUnexpectedEOF` |

**`Writer.ReadFrom(src io.Reader) (n int64, err error)`**

| 条件 | n | err |
|------|---|-----|
| 直到 src EOF 编码完成 | 从 `src` 读取的总字节数 | `nil` |
| src 返回 `ErrWouldBlock` | 信号前从 `src` 读取的字节数 | `ErrWouldBlock` |
| src 返回 more | 信号前从 `src` 读取的字节数 | `ErrMore` |
| 底层 writer 返回 `ErrWouldBlock` | 暂停前从 `src` 读取并纳入的字节数；纯写侧恢复时为 0 | `ErrWouldBlock` |
| 底层 writer 返回 more | 暂停前从 `src` 读取并纳入的字节数；纯写侧恢复时为 0 | `ErrMore` |

**`Forwarder.ForwardOnce() (n int, err error)`**

| 条件 | n | err |
|------|---|-----|
| 完整转发一条消息 | 载荷字节数（写阶段） | `nil` |
| 分组源返回 `(n > 0, io.EOF)` | 载荷字节数（写阶段） | `nil`（下一次调用返回 `io.EOF`） |
| 不再有消息 | 0 | `io.EOF` |
| source 返回 `ErrWouldBlock` | 如果尚未输出分组，则为已读字节数；分组源的 `n > 0` 会先输出并返回载荷字节数（写阶段） | `ErrWouldBlock` |
| source 返回 more | 如果尚未输出分组，则为已读字节数；分组源的 `n > 0` 会先输出并返回载荷字节数（写阶段） | `ErrMore` |
| 写阶段返回 `ErrWouldBlock` | 本次写出的字节数 | `ErrWouldBlock` |
| 写阶段 more | 本次写出的字节数 | `ErrMore` |
| 流式消息或必需的分组读取容量超过内部缓冲 | 0 | `io.ErrShortBuffer` |
| 分组在转发前超过 ReadLimit/默认分组传输上限 | 从该分组读取但未转发的字节数 | `ErrTooLong` |
| 流在消息中途结束 | 当前累计字节数 | `io.ErrUnexpectedEOF` |

### 操作分类

| 操作 | 边界行为 | 适用场景 |
|------|----------|----------|
| `Reader.Read` | **保留消息边界**：一次调用 = 一条消息 | 应用级按消息处理 |
| `Writer.Write` | **保留消息边界**：一次调用 = 一条消息 | 应用级按消息发送 |
| `Reader.WriteTo` | 面向任意写入器是**字节传输**；已知 `framer` 目标保持分组/帧重试律 | 带后缀恢复的高效批量传输 |
| `Writer.ReadFrom` | **分块**：`src` 的每个分块编码为一条消息；分组输出只在零进度后重试整个分组 | 高效编码；不保留上游边界 |
| `Forwarder.ForwardOnce` | **保留消息边界的中继**：解一条、再编码一条 | 需要边界语义的代理/转发 |

### 阻塞策略

默认情况下，framer 是 **非阻塞** 的（`WithNonblock()`）：立即返回 `ErrWouldBlock`。

- `WithBlock()` 或 `WithRetryDelay(0)`：在零进度 `ErrWouldBlock` 上让出执行权（`runtime.Gosched`）并重试
- `WithRetryDelay(d > 0)`：在零进度 `ErrWouldBlock` 上等待 `d` 并重试
- `RetryDelay` 为负（默认）：立即返回零进度 `ErrWouldBlock`
- 如果读取或写入已经传输了字节，framer 会返回正计数和 `ErrWouldBlock`；先处理该进度，再按上文规则重试同一个操作。

除非显式配置，否则任何方法都不会隐藏阻塞。

`framer` 使用 `code.hybscloud.com/iox` 的控制流信号。`ErrWouldBlock` 和 `ErrMore` 是 `iox` 的别名，可与其他 `iox` 感知组件（`iofd`、`takt`）直接集成。

## 快速路径

`framer` 实现了标准库的复制快路径，以便与 `io.Copy` 风格的引擎以及 `iox.CopyPolicy` 互操作：

- `(*Reader).WriteTo(io.Writer)`：高效地将分帧消息的载荷传到 `dst`。
  - Stream（`BinaryStream`）：逐条消息处理，只把载荷字节写到 `dst`。若 `ReadLimit == 0`，会使用保守的默认上限（64KiB）；超过该上限的消息返回 `framer.ErrTooLong`。
  - Packet（`SeqPacket`/`Datagram`）：透传字节传输；哨兵容量读取会在转发前拒绝超限分组，`n` 计为写入 `dst` 的字节数。
  - 写侧语义错误 `framer.ErrWouldBlock` / `framer.ErrMore` 会原样传播，并且进度计数反映已写入的字节数；与字节一起返回的分组源错误会在已接纳分组输出后报告。

- `(*Writer).ReadFrom(io.Reader)`：分块到消息；`src` 每次成功 `Read` 的分块会被编码为一条消息并通过 `w.Write` 写出。
  - 这很高效，但不会保留 src 的应用层消息边界。
  - 在边界保留协议上，它等价于透传。
  - 语义错误 `framer.ErrWouldBlock` / `framer.ErrMore` 会原样传播；`n` 计为从 `src` 读取并纳入写入器状态的字节数。

建议：在非阻塞循环中，优先使用带重试策略的 `iox.CopyPolicy`（例如 `PolicyRetry`），以显式处理 `ErrWouldBlock` / `ErrMore`。

**稳态零分配**：在初始缓冲区分配之后，`Forwarder` 和 `WriteTo` 路径复用内部缓冲区。稳态下每条消息不产生堆分配。

**关于部分写入恢复的说明：** 当使用 `iox.Copy` 向非阻塞目标复制时，可能会发生部分写入。如果源不实现 `io.Seeker`，`iox.Copy` 会返回 `iox.ErrNoSeeker` 以防止静默数据丢失。对于不可寻址的源（如网络套接字），请使用 `iox.CopyPolicy` 并为写入端语义错误配置 `PolicyRetry`，以确保所有已读字节在返回前被写入。

## 转发

- 线级代理（字节引擎）：当可以接受字节级转发且不需要保留更高层边界时，使用 `iox.CopyPolicy` 以及标准 `io` 快路径（`WriterTo`/`ReaderFrom`）。
- 消息级中继（保留边界）：使用 `framer.NewForwarder(dst, src, ...)` 并在轮询循环中调用 `ForwardOnce()`。它从 `src` 解出恰好一条消息，并向 `dst` 编码写出恰好一条消息。
  - 非阻塞语义：遇到 `framer.ErrWouldBlock` 或 `framer.ErrMore` 后使用同一个 `Forwarder` 实例重试；分组源的 `(n > 0, err)` 会先输出，再报告源信号，而写侧挂起会把该源信号保留到后续同一 `Forwarder` 重试。
  - 限制：当内部缓冲不足以容纳流式消息或必需的分组读取容量时返回 `io.ErrShortBuffer`；当分组在转发前超过 `WithReadLimit` 或默认分组传输上限时返回 `framer.ErrTooLong`。
  - 构造后稳态零分配；内部暂存缓冲区会被复用。

消息级中继示例：

```go
fwd := framer.NewForwarder(dst, src, framer.WithReadTCP(), framer.WithWriteTCP())

for {
	_, err := fwd.ForwardOnce()
	if err != nil {
		if err == framer.ErrWouldBlock {
			continue // 等待 src 可读或 dst 可写
		}
		if err == io.EOF {
			break
		}
		log.Fatal(err)
	}
}
```

## 许可证

MIT — 参见 [LICENSE](LICENSE)。

©2025 [Hayabusa Cloud Co., Ltd.](https://code.hybscloud.com)
