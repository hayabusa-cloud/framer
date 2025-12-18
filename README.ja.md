# framer — ストリーム I/O 上のメッセージ境界

[![Go Reference](https://pkg.go.dev/badge/code.hybscloud.com/framer.svg)](https://pkg.go.dev/code.hybscloud.com/framer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hayabusa-cloud/framer)](https://goreportcard.com/report/github.com/hayabusa-cloud/framer)
[![Coverage Status](https://codecov.io/gh/hayabusa-cloud/framer/graph/badge.svg)](https://codecov.io/gh/hayabusa-cloud/framer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**言語 / Languages:** [English](README.md) | [简体中文](README.zh-CN.md) | 日本語 | [Español](README.es.md) | [Français](README.fr.md)

Go 向けのポータブルなメッセージ・フレーミング。ストリーム系トランスポート上で「1 回の `Read` / `Write` = 1 メッセージ」を保ちます。

スコープ：`framer` はストリーム系トランスポートにおけるメッセージ境界の問題を解決します。

## 概要

- バイトストリーム（TCP、Unix stream、pipe）のメッセージ境界問題を解決。
- 境界を保持するトランスポート（UDP、Unix datagram、WebSocket、SCTP）ではパススルー。
- ポータブルなワイヤ形式；バイトオーダは設定可能。

## なぜ

多くのトランスポートはバイトストリームです（TCP、Unix stream、pipe）。単一の `Read` がアプリケーションメッセージの一部だけを返したり、複数メッセージを結合して返したりします。`framer` は境界を復元します。stream モードでは、1 回の `Read` がちょうど 1 つの payload を返し、1 回の `Write` がちょうど 1 つの（長さ前置き付き）メッセージを送ります。

## プロトコル適応

- `BinaryStream`（ストリーム系：TCP、TLS-over-TCP、Unix stream、pipe）：長さプレフィックスを付与し、メッセージ単位で読み書きします。
- `SeqPacket`（例：SCTP、WebSocket）：パススルー（底層が境界を保持）。
- `Datagram`（例：UDP、Unix datagram）：パススルー（底層が境界を保持）。

構築時に `WithProtocol(...)`（読み/書き別オプションあり）で選ぶか、トランスポート・ヘルパ（Options 参照）を使います。

## ワイヤ形式

可変長の長さプレフィックス + payload バイト。拡張長のバイトオーダは `WithByteOrder`、または方向別に `WithReadByteOrder` / `WithWriteByteOrder` で設定します。

## フレームデータ形式

`framer` のフレーミングは意図的にコンパクトです：

- 先頭 1 バイト `H0` + 必要に応じて拡張長バイト。
- `L` を payload 長（バイト数）とします。
  - `0 ≤ L ≤ 253`（`0x00..0xFD`）：`H0 = L`。追加の長さバイトなし。
  - `254 ≤ L ≤ 65535`（`0x0000..0xFFFF`）：`H0 = 0xFE`。次の 2 バイトで `L` を（設定されたバイトオーダで）符号なし 16-bit としてエンコード。
  - `65536 ≤ L ≤ 2^56-1`：`H0 = 0xFF`。次の 7 バイトに `L` の下位 56-bit を（設定されたバイトオーダで）配置。
    - Big-endian：バイト `[1..7]` は `L` の下位 56-bit の big-endian 表現。
    - Little-endian：バイト `[1..7]` は `L` の下位 56-bit の little-endian 表現。

制限とエラー：
- 最大 payload 長は `2^56-1`。超える場合は `framer.ErrTooLong`。
- 読み側に `WithReadLimit` を設定した場合、制限を超える長さは `framer.ErrTooLong`。

## クイックスタート

`go get` でインストール：
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

- `WithProtocol(proto Protocol)` — `BinaryStream` / `SeqPacket` / `Datagram` を選択（読み/書き方向別もあり）。
- バイトオーダ：`WithByteOrder`、または `WithReadByteOrder` / `WithWriteByteOrder`。
- `WithReadLimit(n int)` — 読み取り時の最大 payload サイズを制限。
- `WithRetryDelay(d time.Duration)` — would-block ポリシー；ヘルパ：`WithNonblock()` / `WithBlock()`。

トランスポート・ヘルパ（プリセット）：
- `WithReadTCP` / `WithWriteTCP`（`BinaryStream`、ネットワークオーダ BigEndian）
- `WithReadUDP` / `WithWriteUDP`（`Datagram`、BigEndian）
- `WithReadWebSocket` / `WithWriteWebSocket`（`SeqPacket`、BigEndian）
- `WithReadSCTP` / `WithWriteSCTP`（`SeqPacket`、BigEndian）
- `WithReadUnix` / `WithWriteUnix`（`BinaryStream`、BigEndian）
- `WithReadUnixPacket` / `WithWriteUnixPacket`（`Datagram`、BigEndian）
- `WithReadLocal` / `WithWriteLocal`（`BinaryStream`、ネイティブバイトオーダ）

その他：GoDoc https://pkg.go.dev/code.hybscloud.com/framer

## Semantics Contract（セマンティクス契約）

### エラー分類

| Error | 意味 | 呼び出し側のアクション |
|-------|------|------------------------|
| `nil` | 正常に完了 | 続行；`n` は完全な進捗 |
| `io.EOF` | ストリーム終端（これ以上メッセージなし） | 読み取り停止；正常終了 |
| `io.ErrUnexpectedEOF` | メッセージ途中で終端（header/payload 不完全） | 致命として扱う；破損や切断の可能性 |
| `io.ErrShortBuffer` | 宛先バッファが小さすぎる | より大きいバッファで再試行 |
| `io.ErrShortWrite` | 宛先 Writer が提供バイト数より少なく受理 | 文脈に応じて再試行または致命 |
| `io.ErrNoProgress` | 下層 Reader が非空バッファで進捗なし（`n==0, err==nil`） | 致命として扱う；壊れた `io.Reader` 実装の兆候 |
| `framer.ErrWouldBlock` | 今は待たないと進捗できない | 後で再試行（poll/event 後）；`n` は >0 の場合あり |
| `framer.ErrMore` | 進捗はあるが、同一操作から追加完了が続く | 結果を処理して再度呼び出す |
| `framer.ErrTooLong` | メッセージが制限/ワイヤ上限を超過 | 拒否；状況により致命 |
| `framer.ErrInvalidArgument` | nil reader/writer や不正な設定 | 設定を修正 |

### Outcome tables

**`Reader.Read(p []byte) (n int, err error)`** — BinaryStream

| 条件 | n | err |
|------|---|-----|
| 1 メッセージを完全に返す | payload length | `nil` |
| `len(p) < payload length` | 0 | `io.ErrShortBuffer` |
| payload が ReadLimit 超過 | 0 | `ErrTooLong` |
| 下層が would-block | これまでに読んだバイト数 | `ErrWouldBlock` |
| 下層が more | これまでに読んだバイト数 | `ErrMore` |
| メッセージ境界で EOF | 0 | `io.EOF` |
| header/payload 途中で EOF | 読めたバイト数 | `io.ErrUnexpectedEOF` |

**`Writer.Write(p []byte) (n int, err error)`** — BinaryStream

| 条件 | n | err |
|------|---|-----|
| 1 フレームを完全に送信 | `len(p)` | `nil` |
| payload が最大（2^56-1）超過 | 0 | `ErrTooLong` |
| 下層が would-block | これまでに書いた payload バイト数 | `ErrWouldBlock` |
| 下層が more | これまでに書いた payload バイト数 | `ErrMore` |

**`Reader.WriteTo(dst io.Writer) (n int64, err error)`**

| 条件 | n | err |
|------|---|-----|
| EOF まで転送完了 | payload 合計バイト数 | `nil` |
| 下層 reader が would-block | 書けた payload バイト数 | `ErrWouldBlock` |
| 下層 reader が more | 書けた payload バイト数 | `ErrMore` |
| dst が would-block | 書けた payload バイト数 | `ErrWouldBlock` |
| メッセージが内部バッファ（既定 64KiB）超過 | これまでのバイト数 | `ErrTooLong` |
| メッセージ途中でストリーム終端 | これまでのバイト数 | `io.ErrUnexpectedEOF` |

**`Writer.ReadFrom(src io.Reader) (n int64, err error)`**

| 条件 | n | err |
|------|---|-----|
| src EOF までエンコード完了 | payload 合計バイト数 | `nil` |
| src が would-block | 書けた payload バイト数 | `ErrWouldBlock` |
| src が more | 書けた payload バイト数 | `ErrMore` |
| 下層 writer が would-block | 書けた payload バイト数 | `ErrWouldBlock` |

**`Forwarder.ForwardOnce() (n int, err error)`**

| 条件 | n | err |
|------|---|-----|
| 1 メッセージを完全にフォワード | payload バイト数（書きフェーズ） | `nil` |
| packet source が `(n>0, io.EOF)` を返す | payload バイト数（書きフェーズ） | `nil`（次回 `io.EOF`） |
| メッセージなし | 0 | `io.EOF` |
| 読みフェーズ would-block | この呼び出しで読んだバイト数 | `ErrWouldBlock` |
| 書きフェーズ would-block | この呼び出しで書いたバイト数 | `ErrWouldBlock` |
| メッセージが内部バッファ超過 | 0 | `io.ErrShortBuffer` |
| メッセージが ReadLimit 超過 | 0 | `ErrTooLong` |
| メッセージ途中で終端 | これまでのバイト数 | `io.ErrUnexpectedEOF` |

### 操作の分類

| 操作 | 境界の扱い | 用途 |
|------|------------|------|
| `Reader.Read` | **境界保持**：1 回 = 1 メッセージ | アプリ側のメッセージ処理 |
| `Writer.Write` | **境界保持**：1 回 = 1 メッセージ | アプリ側のメッセージ送信 |
| `Reader.WriteTo` | **チャンク**：payload バイト列（ワイヤ形式ではない） | 高効率転送；境界は保持しない |
| `Writer.ReadFrom` | **チャンク**：src の各チャンクを 1 メッセージにする | 高効率エンコード；上流境界は保持しない |
| `Forwarder.ForwardOnce` | **境界保持リレー**：1 つデコードして 1 つ再エンコード | 境界が必要なプロキシ/中継 |

### ブロッキング・ポリシー

既定では **ノンブロッキング**（`WithNonblock()`）で、`ErrWouldBlock` を即時返します。

- `WithBlock()` — would-block で yield（`runtime.Gosched`）して再試行
- `WithRetryDelay(d)` — would-block で `d` だけ sleep して再試行
- `RetryDelay` が負（既定）— `ErrWouldBlock` を即時返す

明示的に設定しない限り、隠れたブロッキングは行いません。

## Fast paths

`framer` は標準ライブラリのコピー最適化パスを実装し、`io.Copy` 系エンジンや `iox.CopyPolicy` と相互運用します：

- `(*Reader).WriteTo(io.Writer)` — フレーム化されたメッセージの payload を `dst` に効率的に転送。
  - Stream（`BinaryStream`）：メッセージ単位で処理し、payload バイトのみを `dst` に書きます。`ReadLimit == 0` の場合、保守的な既定上限（64KiB）を用い、それを超えるメッセージは `framer.ErrTooLong`。
  - Packet（`SeqPacket`/`Datagram`）：パススルー。
  - `framer.ErrWouldBlock` / `framer.ErrMore` は進捗（書き込めたバイト数）と共にそのまま返します。

- `(*Writer).ReadFrom(io.Reader)` — chunk-to-message：src の各 `Read` チャンクを 1 メッセージとしてエンコードして `w.Write` します。
  - 効率的ですが、src のアプリケーション境界は保持しません。
  - 境界保持プロトコルでは実質パススルーです。
  - `framer.ErrWouldBlock` / `framer.ErrMore` は進捗と共にそのまま返します。

推奨：ノンブロッキングループでは、`ErrWouldBlock` / `ErrMore` を明示的に扱えるリトライポリシー付きの `iox.CopyPolicy`（例：`PolicyRetry`）を使ってください。

**部分書き込みの回復に関する注意：** ノンブロッキングな宛先に対して `iox.Copy` を使用すると、部分書き込みが発生する可能性があります。ソースが `io.Seeker` を実装していない場合、`iox.Copy` はデータの暗黙的な損失を防ぐために `iox.ErrNoSeeker` を返します。シーク不可能なソース（例：ネットワークソケット）の場合は、書き込み側のセマンティックエラーに対して `PolicyRetry` を設定した `iox.CopyPolicy` を使用し、読み取ったすべてのバイトが返却前に書き込まれることを保証してください。

## Forwarding

- ワイヤレベルのプロキシ（byte engines）：`iox.CopyPolicy` と標準 `io` の fast path（`WriterTo`/`ReaderFrom`）を使用。高いスループットが必要で境界保持が不要な場合に適します。
- メッセージ単位のリレー（境界保持）：`framer.NewForwarder(dst, src, ...)` を使い、poll ループで `ForwardOnce()` を呼びます。`src` から 1 メッセージをデコードし、`dst` に 1 メッセージとして再エンコードします。
  - ノンブロッキング：部分的に進捗した場合、`ForwardOnce` は `(n>0, framer.ErrWouldBlock|framer.ErrMore)` を返します。同じ `Forwarder` インスタンスで後で再試行してください。
  - 制限：内部バッファが不足なら `io.ErrShortBuffer`。`WithReadLimit` を超えるなら `framer.ErrTooLong`。
  - 構築後は定常経路での割り当て 0；内部バッファを再利用します。

## ライセンス

MIT — `LICENSE` を参照。
