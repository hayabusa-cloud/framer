# framer — 在流式 I/O 上保留消息边界

[![Go Reference](https://pkg.go.dev/badge/code.hybscloud.com/framer.svg)](https://pkg.go.dev/code.hybscloud.com/framer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hayabusa-cloud/framer)](https://goreportcard.com/report/github.com/hayabusa-cloud/framer)
[![Coverage Status](https://codecov.io/gh/hayabusa-cloud/framer/graph/badge.svg)](https://codecov.io/gh/hayabusa-cloud/framer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**语言 / Languages:** [English](README.md) | 简体中文 | [日本語](README.ja.md) | [Español](README.es.md) | [Français](README.fr.md)

用于 Go 的可移植消息分帧库。在流式传输上实现“一次 `Read` / `Write` 对应一条消息”。

范围：流式传输的消息边界保持。

## 概述

许多传输是字节流（TCP、Unix stream、pipe）。一次 `Read` 可能只返回应用消息的一部分，也可能把多个消息拼在一起返回。`framer` 恢复消息边界：在 stream 模式下，一次 `Read` 只返回一条消息的 payload；一次 `Write` 只发送一条（带长度前缀的）消息。

- 字节流（TCP、Unix stream、pipe）的消息边界保持。
- 对天然保留边界的传输（UDP、Unix datagram、WebSocket、SCTP）做透传。
- 可移植线格式；可配置字节序。

## 协议适配

- `BinaryStream`（流式传输：TCP、TLS-over-TCP、Unix stream、pipe）：添加长度前缀；读/写整条消息。
- `SeqPacket`（例如 SCTP、WebSocket）：透传；底层已保留边界。
- `Datagram`（例如 UDP、Unix datagram）：透传；底层已保留边界。

可在构造时通过 `WithProtocol(...)` 选择（读/写方向也有独立选项），或使用传输助手（见 Options）。

## 线格式（Wire format）

紧凑的可变长度前缀 + payload 字节。扩展长度的字节序可配置：`WithByteOrder`，或按方向 `WithReadByteOrder` / `WithWriteByteOrder`。

## 帧数据格式

`framer` 的分帧格式刻意保持紧凑：

- 首字节 `H0` + 可选的扩展长度字节。
- 设 `L` 为 payload 长度（字节数）。
  - 若 `0 ≤ L ≤ 253`（`0x00..0xFD`）：`H0 = L`。无额外长度字节。
  - 若 `254 ≤ L ≤ 65535`（`0x0000..0xFFFF`）：`H0 = 0xFE`，后续 2 字节以配置的字节序编码无符号 16 位整数 `L`。
  - 若 `65536 ≤ L ≤ 2^56-1`：`H0 = 0xFF`，后续 7 字节以配置的字节序承载 `L` 的低 56 位。
    - Big-endian：字节 `[1..7]` 为 `L` 的低 56 位的大端表示。
    - Little-endian：字节 `[1..7]` 为 `L` 的低 56 位的小端表示。

限制与错误：
- 最大支持的 payload 长度为 `2^56-1`；更大的值返回 `framer.ErrTooLong`。
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

- `WithProtocol(proto Protocol)` — 选择 `BinaryStream`、`SeqPacket` 或 `Datagram`（读/写方向也有独立选项）。
- 字节序：`WithByteOrder`，或 `WithReadByteOrder` / `WithWriteByteOrder`。
- `WithReadLimit(n int)` — 限制读取时允许的最大消息 payload。
- `WithRetryDelay(d time.Duration)` — 配置 would-block 策略；快捷：`WithNonblock()` / `WithBlock()`。

传输助手（便捷预设）：
- `WithReadTCP` / `WithWriteTCP`（`BinaryStream`，网络序 BigEndian）
- `WithReadUDP` / `WithWriteUDP`（`Datagram`，BigEndian）
- `WithReadWebSocket` / `WithWriteWebSocket`（`SeqPacket`，BigEndian）
- `WithReadSCTP` / `WithWriteSCTP`（`SeqPacket`，BigEndian）
- `WithReadUnix` / `WithWriteUnix`（`BinaryStream`，BigEndian）
- `WithReadUnixPacket` / `WithWriteUnixPacket`（`Datagram`，BigEndian）
- `WithReadLocal` / `WithWriteLocal`（`BinaryStream`，本机字节序）

更多内容：GoDoc https://pkg.go.dev/code.hybscloud.com/framer

## 语义契约（Semantics Contract）

### 错误分类

| Error | 含义 | 调用方动作 |
|-------|------|-----------|
| `nil` | 操作成功完成 | 继续；`n` 反映全部进度 |
| `io.EOF` | 流结束（不再有消息） | 停止读取；正常终止 |
| `io.ErrUnexpectedEOF` | 在一条消息中途结束（header 或 payload 不完整） | 视为致命错误；可能是损坏或断连 |
| `io.ErrShortBuffer` | 目标缓冲区不足以容纳 payload | 使用更大的缓冲区重试 |
| `io.ErrShortWrite` | 目标 Writer 接受的字节数小于提供值 | 视场景重试或视为致命 |
| `io.ErrNoProgress` | 底层 Reader 在非空缓冲区上返回了无进度（`n==0, err==nil`） | 视为致命；表示底层 `io.Reader` 实现有问题 |
| `framer.ErrWouldBlock` | 当前无法继续推进而不等待 | 稍后重试（在 poll/event 之后）；`n` 可能 >0 |
| `framer.ErrMore` | 已有进度；同一操作还会产生更多完成 | 处理结果，然后再次调用 |
| `framer.ErrTooLong` | 消息超过限制或线格式上限 | 拒绝消息；可能需要终止连接 |
| `framer.ErrInvalidArgument` | reader/writer 为 nil 或配置非法 | 修正配置 |

### 结果表

**`Reader.Read(p []byte) (n int, err error)`** — BinaryStream 模式

| 条件 | n | err |
|------|---|-----|
| 完整交付一条消息 | payload length | `nil` |
| `len(p) < payload length` | 0 | `io.ErrShortBuffer` |
| payload 超过 ReadLimit | 0 | `ErrTooLong` |
| 底层返回 would-block | 已读取字节数 | `ErrWouldBlock` |
| 底层返回 more | 已读取字节数 | `ErrMore` |
| 在消息边界处 EOF | 0 | `io.EOF` |
| header/payload 中途 EOF | 已读取字节数 | `io.ErrUnexpectedEOF` |

**`Writer.Write(p []byte) (n int, err error)`** — BinaryStream 模式

| 条件 | n | err |
|------|---|-----|
| 完整发送一条带前缀的消息 | `len(p)` | `nil` |
| payload 超过最大值（2^56-1） | 0 | `ErrTooLong` |
| 底层返回 would-block | 已写入的 payload 字节数 | `ErrWouldBlock` |
| 底层返回 more | 已写入的 payload 字节数 | `ErrMore` |

**`Reader.WriteTo(dst io.Writer) (n int64, err error)`**

| 条件 | n | err |
|------|---|-----|
| 直到 EOF 传输完成 | 总 payload 字节数 | `nil` |
| 底层 reader 返回 would-block | 已写入的 payload 字节数 | `ErrWouldBlock` |
| 底层 reader 返回 more | 已写入的 payload 字节数 | `ErrMore` |
| dst 返回 would-block | 已写入的 payload 字节数 | `ErrWouldBlock` |
| 消息超过内部缓冲（默认 64KiB） | 当前累计字节数 | `ErrTooLong` |
| 流在消息中途结束 | 当前累计字节数 | `io.ErrUnexpectedEOF` |

**`Writer.ReadFrom(src io.Reader) (n int64, err error)`**

| 条件 | n | err |
|------|---|-----|
| 直到 src EOF 编码完成 | 总 payload 字节数 | `nil` |
| src 返回 would-block | 已写入的 payload 字节数 | `ErrWouldBlock` |
| src 返回 more | 已写入的 payload 字节数 | `ErrMore` |
| 底层 writer 返回 would-block | 已写入的 payload 字节数 | `ErrWouldBlock` |

**`Forwarder.ForwardOnce() (n int, err error)`**

| 条件 | n | err |
|------|---|-----|
| 完整转发一条消息 | payload 字节数（写阶段） | `nil` |
| packet 源返回 `(n>0, io.EOF)` | payload 字节数（写阶段） | `nil`（下一次调用返回 `io.EOF`） |
| 不再有消息 | 0 | `io.EOF` |
| 读阶段 would-block | 本次读到的字节数 | `ErrWouldBlock` |
| 写阶段 would-block | 本次写出的字节数 | `ErrWouldBlock` |
| 消息超过内部缓冲 | 0 | `io.ErrShortBuffer` |
| 消息超过 ReadLimit | 0 | `ErrTooLong` |
| 流在消息中途结束 | 当前累计字节数 | `io.ErrUnexpectedEOF` |

### 操作分类

| 操作 | 边界行为 | 适用场景 |
|------|----------|----------|
| `Reader.Read` | **保留消息边界**：一次调用 = 一条消息 | 应用级按消息处理 |
| `Writer.Write` | **保留消息边界**：一次调用 = 一条消息 | 应用级按消息发送 |
| `Reader.WriteTo` | **分块**：输出 payload 字节流（非线格式） | 高效批量传输；不保留边界 |
| `Writer.ReadFrom` | **分块**：每个 src chunk 编码为一条消息 | 高效编码；不保留上游边界 |
| `Forwarder.ForwardOnce` | **保留消息边界的中继**：解一条、再编码一条 | 需要边界语义的代理/转发 |

### 阻塞策略

默认情况下，framer 是 **非阻塞** 的（`WithNonblock()`）：立即返回 `ErrWouldBlock`。

- `WithBlock()` — 在 would-block 上进行 yield（`runtime.Gosched`）并重试
- `WithRetryDelay(d)` — 在 would-block 上 sleep `d` 并重试
- `RetryDelay` 为负（默认）— 立即返回 `ErrWouldBlock`

除非显式配置，否则任何方法都不会隐藏阻塞。

`framer` 使用 `code.hybscloud.com/iox` 的控制流信号。`ErrWouldBlock` 和 `ErrMore` 是 `iox` 的别名，可与其他 `iox` 感知组件（`iofd`、`takt`）直接集成。

## 快路径（Fast paths）

`framer` 实现了标准库的复制快路径，以便与 `io.Copy` 风格的引擎以及 `iox.CopyPolicy` 互操作：

- `(*Reader).WriteTo(io.Writer)` — 高效地将分帧消息的 payload 传到 `dst`。
  - Stream（`BinaryStream`）：逐条消息处理，只把 payload 字节写到 `dst`。若 `ReadLimit == 0`，会使用保守的默认上限（64KiB）；超过该上限的消息返回 `framer.ErrTooLong`。
  - Packet（`SeqPacket`/`Datagram`）：透传（读字节、写字节）。
  - 语义错误 `framer.ErrWouldBlock` / `framer.ErrMore` 会原样传播，并且进度计数反映已写入的字节数。

- `(*Writer).ReadFrom(io.Reader)` — chunk-to-message：src 每次成功 `Read` 的 chunk 会被编码为一条消息并通过 `w.Write` 写出。
  - 这很高效，但不会保留 src 的应用层消息边界。
  - 在边界保留协议上，它等价于透传。
  - 语义错误 `framer.ErrWouldBlock` / `framer.ErrMore` 会原样传播，并且进度计数反映 payload 进度。

建议：在非阻塞循环中，优先使用带重试策略的 `iox.CopyPolicy`（例如 `PolicyRetry`），以显式处理 `ErrWouldBlock` / `ErrMore`。

**稳态零分配**：在初始缓冲区分配之后，`Forwarder` 和 `WriteTo` 路径复用内部缓冲区。稳态下每条消息不产生堆分配。

**关于部分写入恢复的说明：** 当使用 `iox.Copy` 向非阻塞目标复制时，可能会发生部分写入。如果源不实现 `io.Seeker`，`iox.Copy` 会返回 `iox.ErrNoSeeker` 以防止静默数据丢失。对于不可寻址的源（如网络套接字），请使用 `iox.CopyPolicy` 并为写入端语义错误配置 `PolicyRetry`，以确保所有已读字节在返回前被写入。

## 转发

- 线级代理（byte engines）：使用 `iox.CopyPolicy` 以及标准 `io` 快路径（`WriterTo`/`ReaderFrom`）。当你不需要保留更高层的边界语义时，这通常能获得更高吞吐。
- 消息级中继（保留边界）：使用 `framer.NewForwarder(dst, src, ...)` 并在 poll loop 中调用 `ForwardOnce()`。它从 `src` 解出恰好一条消息，并向 `dst` 编码写出恰好一条消息。
  - 非阻塞语义：当发生部分进度时，`ForwardOnce` 返回 `(n>0, framer.ErrWouldBlock|framer.ErrMore)`；应在稍后使用同一个 `Forwarder` 实例重试以完成当前消息。
  - 限制：当内部缓冲不足时返回 `io.ErrShortBuffer`；当消息超过 `WithReadLimit` 配置的限制时返回 `framer.ErrTooLong`。
  - 构造后稳态零分配；内部 scratch buffer 会被复用。

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
