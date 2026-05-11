# framer — ストリーム I/O 上のメッセージ境界

[![Go Reference](https://pkg.go.dev/badge/code.hybscloud.com/framer.svg)](https://pkg.go.dev/code.hybscloud.com/framer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hayabusa-cloud/framer)](https://goreportcard.com/report/github.com/hayabusa-cloud/framer)
[![Coverage Status](https://codecov.io/gh/hayabusa-cloud/framer/graph/badge.svg)](https://codecov.io/gh/hayabusa-cloud/framer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**言語 / Languages:** [English](README.md) | [简体中文](README.zh-CN.md) | 日本語 | [Español](README.es.md) | [Français](README.fr.md)

Go 向けのポータブルなメッセージ・フレーミング。ストリーム系トランスポート上で「1 回の `Read` / `Write` = 1 メッセージ」を保ちます。

対象範囲：ストリーム系トランスポートにおけるメッセージ境界の保持。

## 概要

多くのトランスポートはバイトストリームです（TCP、Unix stream、pipe）。単一の `Read` がアプリケーションメッセージの一部だけを返したり、複数メッセージを結合して返したりします。`framer` は境界を復元します。stream モードでは、1 回の `Read` がちょうど 1 つのペイロードを返し、1 回の `Write` がちょうど 1 つの（長さ前置き付き）メッセージを送ります。

- バイトストリーム（TCP、Unix stream、pipe）のメッセージ境界保持。
- 境界を保持するトランスポート（UDP、Unix datagram、WebSocket、SCTP）ではパススルー。
- ポータブルなワイヤ形式；バイトオーダは設定可能。

## プロトコル適応

- `BinaryStream`（ストリーム系：TCP、TLS-over-TCP、Unix stream、pipe）：長さプレフィックスを付与し、メッセージ単位で読み書きします。
- `SeqPacket`（例：SCTP、WebSocket）：パススルー（底層が境界を保持）。
- `Datagram`（例：UDP、Unix datagram）：パススルー（底層が境界を保持）。
- `Reader.Read` ではパケットモードはパススルーです。`WithReadLimit` は 1 回の受信後に検査されるため、超過パケットでは `n > limit` と `ErrTooLong` を返し得ます；`n` はそのパケットからコピーされたバイト数です。
- パケット出力経路は、ゼロ進捗の `ErrWouldBlock` / `ErrMore` の後だけパケット全体を再試行します。`ErrWouldBlock` または `ErrMore` とともに完全受理されたパケットは再送せず、部分パケット書き込みは `io.ErrShortWrite` として報告されます。

構築時に `WithProtocol(...)`（読み/書き別オプションあり）で選ぶか、トランスポート補助オプション（オプション参照）を使います。

## ワイヤ形式

可変長の長さプレフィックス + ペイロードバイト。拡張長のバイトオーダは `WithByteOrder`、または方向別に `WithReadByteOrder` / `WithWriteByteOrder` で設定します。

## フレームデータ形式

`framer` のフレーミングは意図的にコンパクトです：

- 先頭 1 バイト `H0` + 必要に応じて拡張長バイト。
- `L` をペイロード長（バイト数）とします。
  - `0 ≤ L ≤ 253`（`0x00..0xFD`）：`H0 = L`。追加の長さバイトなし。
  - `254 ≤ L ≤ 65535`（`0x0000..0xFFFF`）：`H0 = 0xFE`。次の 2 バイトで `L` を（設定されたバイトオーダで）符号なし 16-bit としてエンコード。
  - `65536 ≤ L ≤ 2^56-1`：`H0 = 0xFF`。次の 7 バイトに `L` の下位 56-bit を（設定されたバイトオーダで）配置。
    - Big-endian：バイト `[1..7]` は `L` の下位 56-bit の big-endian 表現。
    - Little-endian：バイト `[1..7]` は `L` の下位 56-bit の little-endian 表現。

制限とエラー：
- 最大ペイロード長は `2^56-1`。超える場合は `framer.ErrTooLong`。
- 読み側に `WithReadLimit` を設定した場合、制限を超える長さは `framer.ErrTooLong`。

## インストール

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

## ノンブロッキングの使用例

`framer` の既定はノンブロッキングモードです。イベント駆動ループでの使い方：

```go
for {
	n, err := r.Read(buf)
	if n > 0 {
		process(buf[:n])
	}
	if err != nil {
		if err == framer.ErrWouldBlock {
			// データなし；読み取り可能になるまで待つ（epoll、io_uring 等）
			continue
		}
		if err == io.EOF {
			break
		}
		log.Fatal(err)
	}
}
```

## オプション

- `WithProtocol(proto Protocol)`：`BinaryStream` / `SeqPacket` / `Datagram` を選択（読み/書き方向別もあり）。
- バイトオーダ：`WithByteOrder`、または `WithReadByteOrder` / `WithWriteByteOrder`。
- `WithReadLimit(n int)`：読み取り時の最大ペイロードサイズを制限。`Reader.Read` はパケットモードで読み取り後に判定し、`n > limit` と `ErrTooLong` を返す場合があります。
- `WithRetryDelay(d time.Duration)`：ゼロ進捗の `ErrWouldBlock` ポリシーを設定します。負値は `ErrWouldBlock` を即時返し、ゼロは実行権を譲って再試行し、正値は `d` だけ待機してから再試行します。操作がすでにバイトを転送している場合は、呼び出し側が進捗を処理してから再試行できるように、正のカウントと `ErrWouldBlock` を返します。関連オプション：`WithNonblock()` / `WithBlock()`。

トランスポート補助オプション（プリセット）：
- `WithReadTCP` / `WithWriteTCP`（`BinaryStream`、ネットワークオーダ BigEndian）
- `WithReadUDP` / `WithWriteUDP`（`Datagram`、BigEndian）
- `WithReadWebSocket` / `WithWriteWebSocket`（`SeqPacket`、BigEndian）
- `WithReadSCTP` / `WithWriteSCTP`（`SeqPacket`、BigEndian）
- `WithReadUnix` / `WithWriteUnix`（`BinaryStream`、BigEndian）
- `WithReadUnixPacket` / `WithWriteUnixPacket`（`Datagram`、BigEndian）
- `WithReadLocal` / `WithWriteLocal`（`BinaryStream`、ネイティブバイトオーダ）

詳細：GoDoc https://pkg.go.dev/code.hybscloud.com/framer

## 意味論上の契約

### パケットモードの注記（`SeqPacket` / `Datagram`）

- パケットモードはトランスポート境界を保持し、パケット分割は行いません。
- `Reader.Read` は 1 パケット読み取り後に `WithReadLimit` を適用します。転送補助処理は 1 バイトのセンチネルを使い、超過パケットをバイト転送前に拒否します。
- パケット境界を保持する宛先では、ゼロ進捗の `ErrWouldBlock` / `ErrMore` の後だけパケット全体を再試行します。`ErrWouldBlock` または `ErrMore` とともに完全受理されたパケットは再送せず、部分パケット書き込みは境界失敗として `io.ErrShortWrite` を返します。
- 任意の `io.Writer` への `Reader.WriteTo` は、サフィックス再開を持つバイトコピー転送です。宛先が `framer.Writer` の場合は宛先の代数に従い、パケット出力先はゼロ進捗後にパケット全体を再試行し、ストリーム出力先は進行中の同じフレームを再試行します。
- パケットソースが `(n > 0, err)` を返した場合、`Reader.WriteTo` は受理したパケットを出力してから `err` を返します。書き込み側で停止した場合、そのソース信号は再試行まで保持されます。
- 進捗カウントは操作ごとに決まります。`Reader.Read` は `p` にコピーしたバイト数、`Reader.WriteTo` は `dst` に書いたバイト数、`Writer.ReadFrom` は `src` から読んで書き込み側状態に受理したバイト数、`Forwarder.ForwardOnce` は現在フェーズの進捗を返します。

### 再試行規則

- `ErrWouldBlock` は読み書き可能性の一時停止であり、失敗ではありません。同じ集約呼び出し内で前段のループが進捗していれば、集約処理は正のカウントを返す場合があります。
- `ErrMore` は同じ操作にまだ返す進捗があることを示します。`io.EOF` でも読み書き可能性の一時停止でもありません。返された進捗を処理し、同じ操作をもう一度呼びます。
- ストリームで部分進捗後の `Reader.Read` は、同じ `Reader` と同じバッファで再試行します。
- BinaryStream 一時停止後の `Writer.Write` は、同じ `Writer` と同じメッセージ長で再試行します。BinaryStream のヘッダーバイトは `n` に含めません。パケットモードで `n == len(p)` かつ `ErrWouldBlock` または `ErrMore` の場合、そのパケットは受理済みなので `p` を再送しません。
- `Reader.WriteTo` は同じ `Reader` と同じ宛先で、`Writer.ReadFrom` は同じ `Writer` で、`Forwarder.ForwardOnce` は同じ `Forwarder` で再試行します。

### パフォーマンス契約

- ホットパスは定常スループットのため実行時チェックを最小化します。
- 呼び出し側は有効なオプション/バッファ引数、および `ErrWouldBlock` / `ErrMore` 後の操作ごとの再試行規則に責任を持ちます。

### エラー分類

| Error | 意味 | 呼び出し側のアクション |
|-------|------|------------------------|
| `nil` | 正常に完了 | 続行；`n` は完全な進捗 |
| `io.EOF` | ストリーム終端（これ以上メッセージなし） | 読み取り停止；正常終了 |
| `io.ErrUnexpectedEOF` | メッセージ途中で終端（ヘッダー/ペイロード不完全） | 致命として扱う；破損や切断の可能性 |
| `io.ErrShortBuffer` | 宛先バッファが小さすぎる | より大きいバッファで再試行 |
| `io.ErrShortWrite` | 宛先 Writer が提供バイト数より少なく受理 | 文脈に応じて再試行または致命 |
| `io.ErrNoProgress` | 下層 Reader が非空バッファで進捗なし（`n==0, err==nil`） | 致命として扱う；壊れた `io.Reader` 実装の兆候 |
| `framer.ErrWouldBlock` | 今は待たないと進捗できない | 後で再試行（poll/event 後）；`n` は >0 の場合あり |
| `framer.ErrMore` | 同じ操作にまだ返す進捗があり、EOF や読み書き可能性の一時停止とは別です | 返された進捗を処理し、同じ操作を再度呼び出す |
| `framer.ErrTooLong` | メッセージが設定済み制限、転送上限、またはワイヤ形式上限を超過 | 拒否；状況により致命 |
| `framer.ErrInvalidArgument` | nil reader/writer や不正な設定 | 設定を修正 |

### 結果表

**`Reader.Read(p []byte) (n int, err error)`**（BinaryStream）

| 条件 | n | err |
|------|---|-----|
| 1 メッセージを完全に返す | ペイロード長 | `nil` |
| `len(p) < ペイロード長` | 0 | `io.ErrShortBuffer` |
| ペイロードが ReadLimit 超過 | 0 | `ErrTooLong` |
| 下層が `ErrWouldBlock` を返す | これまでに読んだバイト数 | `ErrWouldBlock` |
| 下層が more | これまでに読んだバイト数 | `ErrMore` |
| メッセージ境界で EOF | 0 | `io.EOF` |
| ヘッダー/ペイロード途中で EOF | 読めたバイト数 | `io.ErrUnexpectedEOF` |

**`Writer.Write(p []byte) (n int, err error)`**（BinaryStream）

| 条件 | n | err |
|------|---|-----|
| 1 フレームを完全に送信 | `len(p)` | `nil` |
| ペイロードが最大（2^56-1）超過 | 0 | `ErrTooLong` |
| 下層が `ErrWouldBlock` を返す | これまでに書いたペイロードバイト数 | `ErrWouldBlock` |
| 下層が more | これまでに書いたペイロードバイト数 | `ErrMore` |

**`Reader.WriteTo(dst io.Writer) (n int64, err error)`**

| 条件 | n | err |
|------|---|-----|
| EOF まで転送完了 | ペイロード合計バイト数 | `nil` |
| 下層 reader が `ErrWouldBlock` を返す | 書けたペイロードバイト数 | `ErrWouldBlock` |
| 下層 reader が more | 書けたペイロードバイト数 | `ErrMore` |
| dst が `ErrWouldBlock` を返す | 書けたペイロードバイト数 | `ErrWouldBlock` |
| パケットソースが転送前に ReadLimit 超過 | そのパケット前に書けたバイト数 | `ErrTooLong` |
| メッセージが内部バッファ（既定 64KiB）超過 | これまでのバイト数 | `ErrTooLong` |
| メッセージ途中でストリーム終端 | これまでのバイト数 | `io.ErrUnexpectedEOF` |

**`Writer.ReadFrom(src io.Reader) (n int64, err error)`**

| 条件 | n | err |
|------|---|-----|
| src EOF までエンコード完了 | `src` から読んだ合計バイト数 | `nil` |
| src が `ErrWouldBlock` を返す | シグナル前に `src` から読んだバイト数 | `ErrWouldBlock` |
| src が more | シグナル前に `src` から読んだバイト数 | `ErrMore` |
| 下層 writer が `ErrWouldBlock` を返す | 停止前に `src` から読んで受理したバイト数；書き込み側だけの再開では 0 | `ErrWouldBlock` |
| 下層 writer が more | 停止前に `src` から読んで受理したバイト数；書き込み側だけの再開では 0 | `ErrMore` |

**`Forwarder.ForwardOnce() (n int, err error)`**

| 条件 | n | err |
|------|---|-----|
| 1 メッセージを完全にフォワード | ペイロードバイト数（書きフェーズ） | `nil` |
| パケットソースが `(n > 0, io.EOF)` を返す | ペイロードバイト数（書きフェーズ） | `nil`（次回 `io.EOF`） |
| メッセージなし | 0 | `io.EOF` |
| source が `ErrWouldBlock` を返す | packet を出していない場合は読んだバイト数；パケットソースの `n > 0` は先に出力され、ペイロードバイト数（書きフェーズ）を返す | `ErrWouldBlock` |
| source が more | packet を出していない場合は読んだバイト数；パケットソースの `n > 0` は先に出力され、ペイロードバイト数（書きフェーズ）を返す | `ErrMore` |
| 書きフェーズが `ErrWouldBlock` を返す | この呼び出しで書いたバイト数 | `ErrWouldBlock` |
| 書きフェーズ more | この呼び出しで書いたバイト数 | `ErrMore` |
| ストリームメッセージまたは必要なパケット読み取り容量が内部バッファ超過 | 0 | `io.ErrShortBuffer` |
| パケットが転送前に ReadLimit/既定パケット転送上限を超過 | パケットから読み取ったが転送していないバイト数 | `ErrTooLong` |
| メッセージ途中で終端 | これまでのバイト数 | `io.ErrUnexpectedEOF` |

### 操作の分類

| 操作 | 境界の扱い | 用途 |
|------|------------|------|
| `Reader.Read` | **境界保持**：1 回 = 1 メッセージ | アプリ側のメッセージ処理 |
| `Writer.Write` | **境界保持**：1 回 = 1 メッセージ | アプリ側のメッセージ送信 |
| `Reader.WriteTo` | 任意の書き込み先には**バイト転送**；既知の `framer` 宛先はパケット/フレーム再試行則を保持 | サフィックス再開付きの高効率転送 |
| `Writer.ReadFrom` | **チャンク**：src の各チャンクを 1 メッセージにする；パケット出力はゼロ進捗後のみ全体再試行 | 高効率エンコード；上流境界は保持しない |
| `Forwarder.ForwardOnce` | **境界保持リレー**：1 つデコードして 1 つ再エンコード | 境界が必要なプロキシ/中継 |

### ブロッキング・ポリシー

既定では **ノンブロッキング**（`WithNonblock()`）で、`ErrWouldBlock` を即時返します。

- `WithBlock()` または `WithRetryDelay(0)`：ゼロ進捗の `ErrWouldBlock` で実行権を譲り（`runtime.Gosched`）再試行
- `WithRetryDelay(d > 0)`：ゼロ進捗の `ErrWouldBlock` で `d` だけ待機して再試行
- `RetryDelay` が負（既定）：ゼロ進捗の `ErrWouldBlock` を即時返す
- 読み取りまたは書き込みがすでにバイトを転送している場合、framer は正のカウントと `ErrWouldBlock` を返します。進捗を処理し、上記の規則どおり同じ操作を再試行してください。

明示的に設定しない限り、隠れたブロッキングは行いません。

`framer` は `code.hybscloud.com/iox` の制御フローシグナルを使用します。`ErrWouldBlock` と `ErrMore` は `iox` のエイリアスであり、他の `iox` 対応コンポーネント（`iofd`、`takt`）との直接統合が可能です。

## 高速パス

`framer` は標準ライブラリのコピー最適化パスを実装し、`io.Copy` 系エンジンや `iox.CopyPolicy` と相互運用します：

- `(*Reader).WriteTo(io.Writer)`：フレーム化されたメッセージのペイロードを `dst` に効率的に転送。
  - Stream（`BinaryStream`）：メッセージ単位で処理し、ペイロードバイトのみを `dst` に書きます。`ReadLimit == 0` の場合、保守的な既定上限（64KiB）を用い、それを超えるメッセージは `framer.ErrTooLong`。
  - Packet（`SeqPacket`/`Datagram`）：パススルーのバイト転送。センチネル容量の読み取りで超過パケットを転送前に拒否し、`n` は `dst` に書いたバイト数を数えます。
  - 書き込み側の `framer.ErrWouldBlock` / `framer.ErrMore` は進捗（書き込めたバイト数）と共にそのまま返します。バイトと一緒に返ったパケットソースエラーは、受理したパケットの出力後に返します。

- `(*Writer).ReadFrom(io.Reader)`：チャンクからメッセージへ変換します。`src` の各 `Read` チャンクを 1 メッセージとしてエンコードして `w.Write` します。
  - 効率的ですが、src のアプリケーション境界は保持しません。
  - 境界保持プロトコルでは実質パススルーです。
  - `framer.ErrWouldBlock` / `framer.ErrMore` はそのまま返します。`n` は `src` から読んで書き込み側状態に受理したバイト数です。

推奨：ノンブロッキングループでは、`ErrWouldBlock` / `ErrMore` を明示的に扱えるリトライポリシー付きの `iox.CopyPolicy`（例：`PolicyRetry`）を使ってください。

**定常経路での割り当て 0**：初回のバッファ確保後、`Forwarder` と `WriteTo` パスは内部バッファを再利用します。定常状態ではメッセージあたりのヒープ割り当ては発生しません。

**部分書き込みの回復に関する注意：** ノンブロッキングな宛先に対して `iox.Copy` を使用すると、部分書き込みが発生する可能性があります。ソースが `io.Seeker` を実装していない場合、`iox.Copy` はデータの暗黙的な損失を防ぐために `iox.ErrNoSeeker` を返します。シーク不可能なソース（例：ネットワークソケット）の場合は、書き込み側のセマンティックエラーに対して `PolicyRetry` を設定した `iox.CopyPolicy` を使用し、読み取ったすべてのバイトが返却前に書き込まれることを保証してください。

## 転送

- ワイヤレベルのプロキシ（バイトエンジン）：バイト単位の転送で足り、上位の境界保持が不要な場合は `iox.CopyPolicy` と標準 `io` の高速経路（`WriterTo`/`ReaderFrom`）を使用します。
- メッセージ単位のリレー（境界保持）：`framer.NewForwarder(dst, src, ...)` を使い、poll ループで `ForwardOnce()` を呼びます。`src` から 1 メッセージをデコードし、`dst` に 1 メッセージとして再エンコードします。
  - ノンブロッキング：`framer.ErrWouldBlock` または `framer.ErrMore` の後は同じ `Forwarder` インスタンスで再試行してください。パケットソースの `(n > 0, err)` はソース信号を返す前に出力され、書き込み側の一時停止はそのソース信号を後続の同一 `Forwarder` 再試行まで保持します。
  - 制限：内部バッファがストリームメッセージまたは必要なパケット読み取り容量に不足する場合は `io.ErrShortBuffer`。パケットが転送前に `WithReadLimit` または既定パケット転送上限を超える場合は `framer.ErrTooLong`。
  - 構築後は定常経路での割り当て 0；内部バッファを再利用します。

メッセージ中継の例：

```go
fwd := framer.NewForwarder(dst, src, framer.WithReadTCP(), framer.WithWriteTCP())

for {
	_, err := fwd.ForwardOnce()
	if err != nil {
		if err == framer.ErrWouldBlock {
			continue // src の読み取り可能または dst の書き込み可能を待つ
		}
		if err == io.EOF {
			break
		}
		log.Fatal(err)
	}
}
```

## ライセンス

MIT — [LICENSE](LICENSE) を参照。

©2025 [Hayabusa Cloud Co., Ltd.](https://code.hybscloud.com)
