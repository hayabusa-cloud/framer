# framer — límites de mensaje en E/S de flujo

[![Go Reference](https://pkg.go.dev/badge/code.hybscloud.com/framer.svg)](https://pkg.go.dev/code.hybscloud.com/framer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hayabusa-cloud/framer)](https://goreportcard.com/report/github.com/hayabusa-cloud/framer)
[![Coverage Status](https://codecov.io/gh/hayabusa-cloud/framer/graph/badge.svg)](https://codecov.io/gh/hayabusa-cloud/framer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Idiomas / Languages:** [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | Español | [Français](README.fr.md)

Enmarcado de mensajes portable para Go. Conserva “un mensaje por `Read`/`Write`” sobre transportes de flujo.

Alcance: conservación de límites de mensaje en transportes de flujo.

## Descripción general

Muchos transportes son flujos de bytes (TCP, Unix stream, pipes). Un solo `Read` puede devolver una fracción de un mensaje de aplicación, o varios mensajes concatenados. `framer` restaura los límites: en modo stream, un `Read` devuelve exactamente una carga útil de mensaje y un `Write` emite exactamente un mensaje enmarcado.

- Preservación de límites de mensaje en flujos de bytes (TCP, Unix stream, pipes).
- Pass-through en transportes que ya preservan límites (UDP, Unix datagram, WebSocket, SCTP).
- Formato en el cable portable; orden de bytes configurable.

## Adaptación de protocolo

- `BinaryStream` (transportes stream: TCP, TLS-over-TCP, Unix stream, pipes): agrega un prefijo de longitud; lee/escribe mensajes completos.
- `SeqPacket` (p. ej., SCTP, WebSocket): paso directo; el transporte ya preserva límites.
- `Datagram` (p. ej., UDP, Unix datagram): paso directo; el transporte ya preserva límites.
- Para `Reader.Read`, los modos de paquete son de paso directo: `WithReadLimit` se verifica después de una recepción, por lo que un paquete sobredimensionado puede devolver `n > limit` con `ErrTooLong`; `n` son los bytes copiados desde ese paquete.
- Las rutas de salida de paquete reintentan paquetes completos solo después de `ErrWouldBlock` / `ErrMore` con progreso cero; un paquete aceptado por completo y devuelto con `ErrWouldBlock` o `ErrMore` no se repite, y las escrituras parciales de paquete se reportan como `io.ErrShortWrite`.

Selecciona al construir vía `WithProtocol(...)` (hay variantes de lectura/escritura) o usa los ayudantes de transporte (ver Opciones).

## Formato en el cable

Prefijo de longitud compacto de tamaño variable, seguido por bytes de carga útil. El orden de bytes para la longitud extendida es configurable: `WithByteOrder`, o por dirección `WithReadByteOrder` / `WithWriteByteOrder`.

## Formato de datos de frame

El esquema de framing de `framer` es intencionalmente compacto:

- Byte de cabecera `H0` + bytes opcionales de longitud extendida.
- Sea `L` la longitud de la carga útil en bytes.
  - Si `0 ≤ L ≤ 253` (`0x00..0xFD`): `H0 = L`. Sin bytes extra.
  - Si `254 ≤ L ≤ 65535` (`0x0000..0xFFFF`): `H0 = 0xFE` y los siguientes 2 bytes codifican `L` como entero sin signo de 16 bits en el orden configurado.
  - Si `65536 ≤ L ≤ 2^56-1`: `H0 = 0xFF` y los siguientes 7 bytes llevan los 56 bits bajos de `L` en el orden configurado.
    - Big-endian: bytes `[1..7]` son los 56 bits bajos de `L` en big-endian.
    - Little-endian: bytes `[1..7]` son los 56 bits bajos de `L` en little-endian.

Límites y errores:
- La longitud máxima de carga útil soportada es `2^56-1`; valores mayores producen `framer.ErrTooLong`.
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

## Opciones

- `WithProtocol(proto Protocol)`: elige `BinaryStream`, `SeqPacket` o `Datagram` (hay variantes de lectura/escritura).
- Orden de bytes: `WithByteOrder`, o `WithReadByteOrder` / `WithWriteByteOrder`.
- `WithReadLimit(n int)`: limita el tamaño máximo de la carga útil al leer; `Reader.Read` lo aplica post-lectura en modos de paquete y puede devolver `n > limit` con `ErrTooLong`.
- `WithRetryDelay(d time.Duration)`: define la política de `ErrWouldBlock` con progreso cero; un valor negativo devuelve `ErrWouldBlock` de inmediato, cero cede la ejecución y reintenta, y un valor positivo espera `d` antes de reintentar. Si una operación ya transfirió bytes, devuelve el conteo positivo con `ErrWouldBlock` para que el llamador procese el progreso antes de reintentar; opciones relacionadas: `WithNonblock()` / `WithBlock()`.

Funciones auxiliares de transporte (preajustes):
- `WithReadTCP` / `WithWriteTCP` (`BinaryStream`, BigEndian en orden de red)
- `WithReadUDP` / `WithWriteUDP` (`Datagram`, BigEndian)
- `WithReadWebSocket` / `WithWriteWebSocket` (`SeqPacket`, BigEndian)
- `WithReadSCTP` / `WithWriteSCTP` (`SeqPacket`, BigEndian)
- `WithReadUnix` / `WithWriteUnix` (`BinaryStream`, BigEndian)
- `WithReadUnixPacket` / `WithWriteUnixPacket` (`Datagram`, BigEndian)
- `WithReadLocal` / `WithWriteLocal` (`BinaryStream`, orden nativo)

Más detalles: GoDoc https://pkg.go.dev/code.hybscloud.com/framer

## Contrato semántico

### Nota sobre modos de paquete (`SeqPacket` / `Datagram`)

- El modo de paquete preserva límites del transporte y no divide paquetes.
- `Reader.Read` aplica `WithReadLimit` después de una lectura de paquete; las rutas auxiliares de transferencia usan un byte centinela para rechazar paquetes sobredimensionados antes de reenviar bytes.
- Los destinos que preservan paquetes reintentan el paquete completo solo después de `ErrWouldBlock` / `ErrMore` con progreso cero; un paquete aceptado por completo y devuelto con `ErrWouldBlock` o `ErrMore` no se repite, y las escrituras parciales de paquete son fallos de límite y devuelven `io.ErrShortWrite`.
- `Reader.WriteTo` hacia un `io.Writer` arbitrario es una transferencia de bytes con reanudación por sufijo. Cuando el destino es un `framer.Writer`, usa el álgebra del destino: los escritores de paquete reintentan el paquete completo tras progreso cero, y los escritores de flujo reintentan el mismo marco en curso.
- Si una fuente de paquete devuelve `(n > 0, err)`, `Reader.WriteTo` emite el paquete admitido antes de reportar `err`; una suspensión de escritura conserva esa señal de fuente pendiente durante el reintento.
- Los conteos de progreso dependen de la operación: `Reader.Read` reporta bytes copiados en `p`, `Reader.WriteTo` reporta bytes escritos en `dst`, `Writer.ReadFrom` reporta bytes leídos desde `src` y admitidos en el estado del escritor, y `Forwarder.ForwardOnce` reporta el progreso de su fase actual.

### Disciplina de reintento

- `ErrWouldBlock` es suspensión de disponibilidad, no fallo; las rutas agregadas pueden devolver un conteo positivo cuando pasos anteriores del bucle avanzaron antes de la suspensión.
- `ErrMore` significa que la misma operación todavía tiene progreso por entregar; no es `io.EOF` ni suspensión de disponibilidad. Procesa cualquier progreso devuelto y vuelve a llamar la misma operación.
- Reintenta `Reader.Read` tras progreso parcial de flujo en el mismo `Reader` y con el mismo búfer.
- Reintenta `Writer.Write` tras una suspensión BinaryStream en el mismo `Writer` y con la misma longitud de mensaje; los bytes de cabecera BinaryStream no se incluyen en `n`. En modos de paquete, `n == len(p)` con `ErrWouldBlock` o `ErrMore` significa que el paquete fue aceptado, así que no repitas `p`.
- Reintenta `Reader.WriteTo` en el mismo `Reader` y el mismo destino, `Writer.ReadFrom` en el mismo `Writer`, y `Forwarder.ForwardOnce` en el mismo `Forwarder`.

### Contrato de rendimiento

- Las rutas calientes minimizan verificaciones en tiempo de ejecución para mantener un rendimiento estable.
- El llamador es responsable de opciones y búferes válidos, y del reintento específico de cada operación tras `ErrWouldBlock` o `ErrMore`.

### Taxonomía de errores

| Error | Significado | Acción del llamador |
|-------|-------------|---------------------|
| `nil` | Operación completada con éxito | Continúa; `n` refleja el progreso total |
| `io.EOF` | Fin de stream (no hay más mensajes) | Deja de leer; terminación normal |
| `io.ErrUnexpectedEOF` | El flujo terminó a mitad de mensaje (cabecera o carga útil incompleta) | Trátalo como fatal; posible corrupción o desconexión |
| `io.ErrShortBuffer` | Búfer destino demasiado pequeño para la carga útil | Reintenta con un búfer más grande |
| `io.ErrShortWrite` | El destino aceptó menos bytes que los provistos | Reintenta o trátalo como fatal según el contexto |
| `io.ErrNoProgress` | El Reader subyacente no avanzó (`n==0, err==nil`) con búfer no vacío | Trátalo como fatal; indica un `io.Reader` roto |
| `framer.ErrWouldBlock` | No es posible avanzar ahora sin esperar | Reintenta más tarde (tras poll/event); `n` puede ser >0 |
| `framer.ErrMore` | La misma operación todavía tiene progreso por entregar, distinto de EOF y de suspensión de disponibilidad | Procesa el progreso devuelto y vuelve a llamar la misma operación |
| `framer.ErrTooLong` | El mensaje excede un límite configurado, un tope de transferencia o el límite del formato en línea | Rechaza; posiblemente fatal |
| `framer.ErrInvalidArgument` | Reader/Writer nil o configuración inválida | Corrige la configuración |

### Tablas de resultados

**`Reader.Read(p []byte) (n int, err error)`** (modo BinaryStream)

| Condición | n | err |
|----------|---|-----|
| Mensaje completo entregado | longitud de carga útil | `nil` |
| `len(p) < longitud de carga útil` | 0 | `io.ErrShortBuffer` |
| La carga útil excede ReadLimit | 0 | `ErrTooLong` |
| El subyacente devuelve `ErrWouldBlock` | bytes leídos hasta ahora | `ErrWouldBlock` |
| El subyacente devuelve more | bytes leídos hasta ahora | `ErrMore` |
| EOF en el límite de mensaje | 0 | `io.EOF` |
| EOF a mitad de cabecera o carga útil | bytes leídos | `io.ErrUnexpectedEOF` |

**`Writer.Write(p []byte) (n int, err error)`** (modo BinaryStream)

| Condición | n | err |
|----------|---|-----|
| Mensaje enmarcado completo emitido | `len(p)` | `nil` |
| La carga útil excede el máximo (2^56-1) | 0 | `ErrTooLong` |
| El subyacente devuelve `ErrWouldBlock` | bytes de carga útil escritos | `ErrWouldBlock` |
| El subyacente devuelve more | bytes de carga útil escritos | `ErrMore` |

**`Reader.WriteTo(dst io.Writer) (n int64, err error)`**

| Condición | n | err |
|----------|---|-----|
| Transferencia hasta EOF | bytes totales de carga útil | `nil` |
| Reader subyacente devuelve `ErrWouldBlock` | bytes de carga útil escritos | `ErrWouldBlock` |
| Reader subyacente devuelve more | bytes de carga útil escritos | `ErrMore` |
| `dst` devuelve `ErrWouldBlock` | bytes de carga útil escritos | `ErrWouldBlock` |
| Fuente de paquete excede ReadLimit antes de reenviar | bytes ya escritos antes de ese paquete | `ErrTooLong` |
| Mensaje excede el búfer interno (64KiB por defecto) | bytes hasta ahora | `ErrTooLong` |
| Stream terminó a mitad de mensaje | bytes hasta ahora | `io.ErrUnexpectedEOF` |

**`Writer.ReadFrom(src io.Reader) (n int64, err error)`**

| Condición | n | err |
|----------|---|-----|
| Chunks codificados hasta src EOF | bytes totales leídos desde `src` | `nil` |
| `src` devuelve `ErrWouldBlock` | bytes leídos desde `src` antes de la señal | `ErrWouldBlock` |
| `src` devuelve more | bytes leídos desde `src` antes de la señal | `ErrMore` |
| Writer subyacente devuelve `ErrWouldBlock` | bytes leídos desde `src` y admitidos antes de la suspensión; 0 en reanudación solo de escritura | `ErrWouldBlock` |
| Writer subyacente devuelve more | bytes leídos desde `src` y admitidos antes de la suspensión; 0 en reanudación solo de escritura | `ErrMore` |

**`Forwarder.ForwardOnce() (n int, err error)`**

| Condición | n | err |
|----------|---|-----|
| Un mensaje reenviado completamente | bytes de carga útil (fase de escritura) | `nil` |
| Fuente de paquete devuelve `(n > 0, io.EOF)` | bytes de carga útil (fase de escritura) | `nil` (la próxima llamada devuelve `io.EOF`) |
| No hay más mensajes | 0 | `io.EOF` |
| La fuente devuelve `ErrWouldBlock` | bytes leídos si no se emitió ningún paquete; un `n > 0` de fuente de paquete se emite primero y devuelve bytes de carga útil (fase de escritura) | `ErrWouldBlock` |
| La fuente devuelve more | bytes leídos si no se emitió ningún paquete; un `n > 0` de fuente de paquete se emite primero y devuelve bytes de carga útil (fase de escritura) | `ErrMore` |
| Would-block en fase de escritura | bytes escritos en esta llamada | `ErrWouldBlock` |
| More en fase de escritura | bytes escritos en esta llamada | `ErrMore` |
| Mensaje de flujo o capacidad de lectura de paquete requerida excede el búfer interno | 0 | `io.ErrShortBuffer` |
| Paquete excede ReadLimit/tope de transferencia de paquete por defecto antes de reenviar | bytes leídos del paquete, no reenviados | `ErrTooLong` |
| Stream terminó a mitad de mensaje | bytes hasta ahora | `io.ErrUnexpectedEOF` |

### Clasificación de operaciones

| Operación | Comportamiento de límites | Caso de uso |
|----------|----------------------------|------------|
| `Reader.Read` | **Preserva límites**: 1 llamada = 1 mensaje | Procesamiento por mensaje |
| `Writer.Write` | **Preserva límites**: 1 llamada = 1 mensaje | Envío por mensaje |
| `Reader.WriteTo` | **Transferencia de bytes** hacia escritores arbitrarios; destinos `framer` conocidos preservan la ley de reintento de paquete/marco | Transferencia eficiente con reanudación por sufijo |
| `Writer.ReadFrom` | **Fragmentación**: cada fragmento de `src` se vuelve un mensaje; la salida de paquete reintenta el paquete completo solo tras progreso cero | Codificación eficiente; NO preserva límites aguas arriba |
| `Forwarder.ForwardOnce` | **Relay con límites**: decodifica uno, re-encodifica uno | Proxy con preservación de límites |

### Política de bloqueo

Por defecto, framer es **no bloqueante** (`WithNonblock()`): devuelve `ErrWouldBlock` inmediatamente.

- `WithBlock()` o `WithRetryDelay(0)`: cede la ejecución (`runtime.Gosched`) y reintenta ante `ErrWouldBlock` con progreso cero
- `WithRetryDelay(d > 0)`: espera `d` y reintenta ante `ErrWouldBlock` con progreso cero
- `RetryDelay` negativo (por defecto): devuelve `ErrWouldBlock` de progreso cero inmediatamente
- Si una lectura o escritura ya transfirió bytes, framer devuelve el conteo positivo con `ErrWouldBlock`; procesa el progreso y reintenta la misma operación según se documenta arriba.

Ningún método oculta bloqueo a menos que se configure explícitamente.

`framer` utiliza las señales de control de flujo de `code.hybscloud.com/iox`. `ErrWouldBlock` y `ErrMore` son alias de `iox`, lo que permite la integración directa con otros componentes compatibles con `iox` (`iofd`, `takt`).

## Rutas rápidas

`framer` implementa rutas rápidas de la biblioteca estándar para interoperar con motores tipo `io.Copy` y con `iox.CopyPolicy`:

- `(*Reader).WriteTo(io.Writer)`: transfiere eficientemente cargas útiles a `dst`.
  - Stream (`BinaryStream`): procesa un mensaje por vez y escribe solo bytes de carga útil. Si `ReadLimit == 0`, usa un tope conservador (64KiB); mensajes más grandes devuelven `framer.ErrTooLong`.
  - Packet (`SeqPacket`/`Datagram`): transferencia de bytes de paso directo; las lecturas con capacidad centinela rechazan paquetes sobredimensionados antes de reenviar, y `n` cuenta bytes escritos en `dst`.
  - Los errores de escritura `framer.ErrWouldBlock` y `framer.ErrMore` se propagan sin cambios, con el conteo reflejando bytes escritos; los errores de fuente de paquete devueltos con bytes se reportan después de emitir el paquete admitido.

- `(*Writer).ReadFrom(io.Reader)`: de fragmentos a mensajes; cada fragmento leído de `src` se codifica como un mensaje y se escribe vía `w.Write`.
  - Es eficiente pero no preserva límites de mensaje de `src`.
  - En protocolos que preservan límites, se comporta como paso directo.
  - `framer.ErrWouldBlock` y `framer.ErrMore` se propagan sin cambios; `n` cuenta bytes leídos desde `src` y admitidos en el estado del escritor.

Recomendación: en bucles no bloqueantes, prefiere `iox.CopyPolicy` con política de reintentos (p. ej., `PolicyRetry`) para manejar explícitamente `ErrWouldBlock` / `ErrMore`.

**Cero asignaciones en estado estable**: Tras la asignación inicial del búfer, las rutas de `Forwarder` y `WriteTo` reutilizan los búferes internos. No se producen asignaciones en el heap por mensaje en estado estable.

**Nota sobre recuperación de escrituras parciales:** Al usar `iox.Copy` con destinos no bloqueantes, pueden ocurrir escrituras parciales. Si la fuente no implementa `io.Seeker`, `iox.Copy` devuelve `iox.ErrNoSeeker` para evitar pérdida silenciosa de datos. Para fuentes no buscables (p. ej., sockets de red), usa `iox.CopyPolicy` con `PolicyRetry` para errores semánticos del lado de escritura, asegurando que todos los bytes leídos se escriban antes de retornar.

## Reenvío

- Proxy a nivel de cable (motores de bytes): usa `iox.CopyPolicy` y rutas rápidas estándar (`WriterTo`/`ReaderFrom`) cuando la retransmisión a nivel de bytes es aceptable y no se necesitan límites de nivel superior.
- Relevo por mensaje (preserva límites): usa `framer.NewForwarder(dst, src, ...)` y llama `ForwardOnce()` en tu bucle de sondeo. Decodifica exactamente un mensaje desde `src` y lo vuelve a codificar como exactamente un mensaje hacia `dst`.
  - Semántica no bloqueante: reintenta con la misma instancia tras `framer.ErrWouldBlock` o `framer.ErrMore`; una fuente de paquete con `(n > 0, err)` se emite antes de reportar la señal de fuente, y una suspensión de escritura conserva esa señal para el reintento posterior en el mismo `Forwarder`.
  - Límites: `io.ErrShortBuffer` si el búfer interno es insuficiente para un mensaje de flujo o para la capacidad de lectura de paquete requerida; `framer.ErrTooLong` si un paquete excede `WithReadLimit` o el tope de transferencia de paquete por defecto antes de reenviar.
  - Cero asignaciones en estado estable tras la construcción; el búfer interno se reutiliza.

Ejemplo de relevo por mensaje:

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
