# framer — límites de mensaje sobre E/S de flujo

[![Go Reference](https://pkg.go.dev/badge/code.hybscloud.com/framer.svg)](https://pkg.go.dev/code.hybscloud.com/framer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hayabusa-cloud/framer)](https://goreportcard.com/report/github.com/hayabusa-cloud/framer)
[![Coverage Status](https://codecov.io/gh/hayabusa-cloud/framer/graph/badge.svg)](https://codecov.io/gh/hayabusa-cloud/framer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Idiomas / Languages:** [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | Español | [Français](README.fr.md)

Framing de mensajes portable para Go. Conserva “un mensaje por `Read`/`Write`” sobre transportes tipo stream.

Alcance: preservación de límites de mensaje en transportes de flujo.

## Descripción general

Muchos transportes son flujos de bytes (TCP, Unix stream, pipes). Un solo `Read` puede devolver una fracción de un mensaje de aplicación, o varios mensajes concatenados. `framer` restaura los límites: en modo stream, un `Read` devuelve exactamente un payload de mensaje y un `Write` emite exactamente un mensaje enmarcado.

- Preservación de límites de mensaje en flujos de bytes (TCP, Unix stream, pipes).
- Pass-through en transportes que ya preservan límites (UDP, Unix datagram, WebSocket, SCTP).
- Formato wire portable; orden de bytes configurable.

## Adaptación de protocolo

- `BinaryStream` (transportes stream: TCP, TLS-over-TCP, Unix stream, pipes): agrega un prefijo de longitud; lee/escribe mensajes completos.
- `SeqPacket` (p. ej., SCTP, WebSocket): pass-through; el transporte ya preserva límites.
- `Datagram` (p. ej., UDP, Unix datagram): pass-through; el transporte ya preserva límites.

Selecciona al construir vía `WithProtocol(...)` (hay variantes de lectura/escritura) o usa los helpers de transporte (ver Options).

## Wire format

Prefijo de longitud compacto de tamaño variable, seguido por bytes de payload. El orden de bytes para la longitud extendida es configurable: `WithByteOrder`, o por dirección `WithReadByteOrder` / `WithWriteByteOrder`.

## Formato de datos del frame

El esquema de framing de `framer` es intencionalmente compacto:

- Byte de cabecera `H0` + bytes opcionales de longitud extendida.
- Sea `L` la longitud del payload en bytes.
  - Si `0 ≤ L ≤ 253` (`0x00..0xFD`): `H0 = L`. Sin bytes extra.
  - Si `254 ≤ L ≤ 65535` (`0x0000..0xFFFF`): `H0 = 0xFE` y los siguientes 2 bytes codifican `L` como entero sin signo de 16 bits en el orden configurado.
  - Si `65536 ≤ L ≤ 2^56-1`: `H0 = 0xFF` y los siguientes 7 bytes llevan los 56 bits bajos de `L` en el orden configurado.
    - Big-endian: bytes `[1..7]` son los 56 bits bajos de `L` en big-endian.
    - Little-endian: bytes `[1..7]` son los 56 bits bajos de `L` en little-endian.

Límites y errores:
- La longitud máxima de payload soportada es `2^56-1`; valores mayores producen `framer.ErrTooLong`.
- Con un límite de lectura (`WithReadLimit`), longitudes mayores fallan con `framer.ErrTooLong`.

## Instalación

Instala con `go get`:
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

## Uso no bloqueante

`framer` opera en modo no bloqueante por defecto. En un bucle orientado a eventos:

```go
for {
    n, err := r.Read(buf)
    if n > 0 {
        process(buf[:n])
    }
    if err != nil {
        if err == framer.ErrWouldBlock {
            // Sin datos ahora; esperar disponibilidad de lectura (epoll, io_uring, etc.)
            continue
        }
        if err == io.EOF {
            break
        }
        log.Fatal(err)
    }
}
```

- `WithProtocol(proto Protocol)` — elige `BinaryStream`, `SeqPacket` o `Datagram` (hay variantes de lectura/escritura).
- Orden de bytes: `WithByteOrder`, o `WithReadByteOrder` / `WithWriteByteOrder`.
- `WithReadLimit(n int)` — limita el tamaño máximo del payload al leer.
- `WithRetryDelay(d time.Duration)` — política de would-block; helpers: `WithNonblock()` / `WithBlock()`.

Helpers de transporte (presets):
- `WithReadTCP` / `WithWriteTCP` (`BinaryStream`, BigEndian en orden de red)
- `WithReadUDP` / `WithWriteUDP` (`Datagram`, BigEndian)
- `WithReadWebSocket` / `WithWriteWebSocket` (`SeqPacket`, BigEndian)
- `WithReadSCTP` / `WithWriteSCTP` (`SeqPacket`, BigEndian)
- `WithReadUnix` / `WithWriteUnix` (`BinaryStream`, BigEndian)
- `WithReadUnixPacket` / `WithWriteUnixPacket` (`Datagram`, BigEndian)
- `WithReadLocal` / `WithWriteLocal` (`BinaryStream`, orden nativo)

Más: GoDoc https://pkg.go.dev/code.hybscloud.com/framer

## Contrato de semántica (Semantics Contract)

### Taxonomía de errores

| Error | Significado | Acción del llamador |
|-------|-------------|---------------------|
| `nil` | Operación completada con éxito | Continúa; `n` refleja el progreso total |
| `io.EOF` | Fin de stream (no hay más mensajes) | Deja de leer; terminación normal |
| `io.ErrUnexpectedEOF` | El stream terminó a mitad de mensaje (header o payload incompleto) | Trátalo como fatal; posible corrupción o desconexión |
| `io.ErrShortBuffer` | Buffer destino demasiado pequeño para el payload | Reintenta con un buffer más grande |
| `io.ErrShortWrite` | El destino aceptó menos bytes que los provistos | Reintenta o trátalo como fatal según el contexto |
| `io.ErrNoProgress` | El Reader subyacente no avanzó (`n==0, err==nil`) con buffer no vacío | Trátalo como fatal; indica un `io.Reader` roto |
| `framer.ErrWouldBlock` | No es posible avanzar ahora sin esperar | Reintenta más tarde (tras poll/event); `n` puede ser >0 |
| `framer.ErrMore` | Hubo progreso; seguirán más completions del mismo op | Procesa y vuelve a llamar |
| `framer.ErrTooLong` | El mensaje excede límites o el máximo del wire format | Rechaza; posiblemente fatal |
| `framer.ErrInvalidArgument` | Reader/Writer nil o configuración inválida | Corrige la configuración |

### Tablas de resultados

**`Reader.Read(p []byte) (n int, err error)`** — modo BinaryStream

| Condición | n | err |
|----------|---|-----|
| Mensaje completo entregado | payload length | `nil` |
| `len(p) < payload length` | 0 | `io.ErrShortBuffer` |
| Payload excede ReadLimit | 0 | `ErrTooLong` |
| El subyacente devuelve would-block | bytes leídos hasta ahora | `ErrWouldBlock` |
| El subyacente devuelve more | bytes leídos hasta ahora | `ErrMore` |
| EOF en el límite de mensaje | 0 | `io.EOF` |
| EOF a mitad de header o payload | bytes leídos | `io.ErrUnexpectedEOF` |

**`Writer.Write(p []byte) (n int, err error)`** — modo BinaryStream

| Condición | n | err |
|----------|---|-----|
| Mensaje enmarcado completo emitido | `len(p)` | `nil` |
| Payload excede el máximo (2^56-1) | 0 | `ErrTooLong` |
| El subyacente devuelve would-block | bytes de payload escritos | `ErrWouldBlock` |
| El subyacente devuelve more | bytes de payload escritos | `ErrMore` |

**`Reader.WriteTo(dst io.Writer) (n int64, err error)`**

| Condición | n | err |
|----------|---|-----|
| Transferencia hasta EOF | bytes totales de payload | `nil` |
| Reader subyacente devuelve would-block | bytes de payload escritos | `ErrWouldBlock` |
| Reader subyacente devuelve more | bytes de payload escritos | `ErrMore` |
| `dst` devuelve would-block | bytes de payload escritos | `ErrWouldBlock` |
| Mensaje excede el buffer interno (64KiB por defecto) | bytes hasta ahora | `ErrTooLong` |
| Stream terminó a mitad de mensaje | bytes hasta ahora | `io.ErrUnexpectedEOF` |

**`Writer.ReadFrom(src io.Reader) (n int64, err error)`**

| Condición | n | err |
|----------|---|-----|
| Chunks codificados hasta src EOF | bytes totales de payload | `nil` |
| `src` devuelve would-block | bytes de payload escritos | `ErrWouldBlock` |
| `src` devuelve more | bytes de payload escritos | `ErrMore` |
| Writer subyacente devuelve would-block | bytes de payload escritos | `ErrWouldBlock` |

**`Forwarder.ForwardOnce() (n int, err error)`**

| Condición | n | err |
|----------|---|-----|
| Un mensaje reenviado completamente | bytes de payload (fase de escritura) | `nil` |
| Fuente packet devuelve `(n>0, io.EOF)` | bytes de payload (fase de escritura) | `nil` (la próxima llamada devuelve `io.EOF`) |
| No hay más mensajes | 0 | `io.EOF` |
| Would-block en fase de lectura | bytes leídos en esta llamada | `ErrWouldBlock` |
| Would-block en fase de escritura | bytes escritos en esta llamada | `ErrWouldBlock` |
| Mensaje excede el buffer interno | 0 | `io.ErrShortBuffer` |
| Mensaje excede ReadLimit | 0 | `ErrTooLong` |
| Stream terminó a mitad de mensaje | bytes hasta ahora | `io.ErrUnexpectedEOF` |

### Clasificación de operaciones

| Operación | Comportamiento de límites | Caso de uso |
|----------|----------------------------|------------|
| `Reader.Read` | **Preserva límites**: 1 llamada = 1 mensaje | Procesamiento por mensaje |
| `Writer.Write` | **Preserva límites**: 1 llamada = 1 mensaje | Envío por mensaje |
| `Reader.WriteTo` | **Chunking**: stream de bytes de payload (no wire format) | Transferencia eficiente; NO preserva límites |
| `Writer.ReadFrom` | **Chunking**: cada chunk de `src` se vuelve un mensaje | Codificación eficiente; NO preserva límites aguas arriba |
| `Forwarder.ForwardOnce` | **Relay con límites**: decodifica uno, re-encodifica uno | Proxy con preservación de límites |

### Política de bloqueo

Por defecto, framer es **no bloqueante** (`WithNonblock()`): devuelve `ErrWouldBlock` inmediatamente.

- `WithBlock()` — hace yield (`runtime.Gosched`) y reintenta ante would-block
- `WithRetryDelay(d)` — duerme `d` y reintenta ante would-block
- `RetryDelay` negativo (por defecto) — devuelve `ErrWouldBlock` inmediatamente

Ningún método oculta bloqueo a menos que se configure explícitamente.

`framer` utiliza las señales de control de flujo de `code.hybscloud.com/iox`. `ErrWouldBlock` y `ErrMore` son alias de `iox`, lo que permite la integración directa con otros componentes compatibles con `iox` (`iofd`, `takt`).

## Fast paths

`framer` implementa fast paths del stdlib para interoperar con motores tipo `io.Copy` y con `iox.CopyPolicy`:

- `(*Reader).WriteTo(io.Writer)` — transfiere eficientemente payloads a `dst`.
  - Stream (`BinaryStream`): procesa un mensaje por vez y escribe solo bytes de payload. Si `ReadLimit == 0`, usa un tope conservador (64KiB); mensajes más grandes devuelven `framer.ErrTooLong`.
  - Packet (`SeqPacket`/`Datagram`): pass-through.
  - `framer.ErrWouldBlock` y `framer.ErrMore` se propagan sin cambios, con el conteo reflejando bytes escritos.

- `(*Writer).ReadFrom(io.Reader)` — chunk-to-message: cada chunk leído de `src` se codifica como un mensaje y se escribe vía `w.Write`.
  - Es eficiente pero no preserva límites de mensaje de `src`.
  - En protocolos con límites preservados, se comporta como pass-through.
  - `framer.ErrWouldBlock` y `framer.ErrMore` se propagan sin cambios.

Recomendación: en bucles no bloqueantes, prefiere `iox.CopyPolicy` con política de reintentos (p. ej., `PolicyRetry`) para manejar explícitamente `ErrWouldBlock` / `ErrMore`.

**Cero asignaciones en steady-state**: Tras la asignación inicial del buffer, los paths de `Forwarder` y `WriteTo` reutilizan los buffers internos. No se producen asignaciones en el heap por mensaje en steady-state.

**Nota sobre recuperación de escrituras parciales:** Al usar `iox.Copy` con destinos no bloqueantes, pueden ocurrir escrituras parciales. Si la fuente no implementa `io.Seeker`, `iox.Copy` devuelve `iox.ErrNoSeeker` para evitar pérdida silenciosa de datos. Para fuentes no buscables (p. ej., sockets de red), usa `iox.CopyPolicy` con `PolicyRetry` para errores semánticos del lado de escritura, asegurando que todos los bytes leídos se escriban antes de retornar.

## Reenvío

- Proxy a nivel wire (motores de bytes): usa `iox.CopyPolicy` y fast paths estándar (`WriterTo`/`ReaderFrom`). Maximiza throughput cuando no necesitas preservar límites de nivel superior.
- Relay por mensaje (preserva límites): usa `framer.NewForwarder(dst, src, ...)` y llama `ForwardOnce()` en tu poll loop. Decodifica exactamente un mensaje desde `src` y lo re-encodifica como exactamente un mensaje hacia `dst`.
  - Semántica no bloqueante: `ForwardOnce` devuelve `(n>0, framer.ErrWouldBlock|framer.ErrMore)` cuando hubo progreso parcial; reintenta con la misma instancia.
  - Límites: `io.ErrShortBuffer` si el buffer interno es insuficiente; `framer.ErrTooLong` si excede `WithReadLimit`.
  - Cero asignaciones en steady-state tras la construcción; el buffer interno se reutiliza.

Ejemplo de relay por mensaje:

```go
fwd := framer.NewForwarder(dst, src, framer.WithReadTCP(), framer.WithWriteTCP())

for {
    _, err := fwd.ForwardOnce()
    if err != nil {
        if err == framer.ErrWouldBlock {
            continue // esperar src legible o dst escribible
        }
        if err == io.EOF {
            break
        }
        log.Fatal(err)
    }
}
```

## Licencia

MIT — ver [LICENSE](LICENSE).

©2025 [Hayabusa Cloud Co., Ltd.](https://code.hybscloud.com)
