# framer — frontières de messages sur E/S de flux

[![Go Reference](https://pkg.go.dev/badge/code.hybscloud.com/framer.svg)](https://pkg.go.dev/code.hybscloud.com/framer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hayabusa-cloud/framer)](https://goreportcard.com/report/github.com/hayabusa-cloud/framer)
[![Coverage Status](https://codecov.io/gh/hayabusa-cloud/framer/graph/badge.svg)](https://codecov.io/gh/hayabusa-cloud/framer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Langues / Languages:** [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md) | Français

Framing de messages portable pour Go. Préserve “un message par `Read`/`Write`” au-dessus des transports de type stream.

Portée : préservation des frontières de messages sur les transports en flux.

## Vue d’ensemble

Beaucoup de transports sont des flux d’octets (TCP, Unix stream, pipes). Un seul `Read` peut retourner une partie d’un message applicatif, ou plusieurs messages concaténés. `framer` restaure les frontières : en mode stream, un `Read` retourne exactement un payload de message, et un `Write` émet exactement un message encadré.

- Préservation des frontières de message sur les flux d’octets (TCP, Unix stream, pipes).
- Pass-through sur les transports qui préservent déjà les frontières (UDP, Unix datagram, WebSocket, SCTP).
- Format wire portable ; ordre des octets configurable.

## Adaptation de protocole

- `BinaryStream` (transports stream : TCP, TLS-over-TCP, Unix stream, pipes) : ajoute un préfixe de longueur ; lit/écrit des messages entiers.
- `SeqPacket` (ex. SCTP, WebSocket) : pass-through ; le transport préserve déjà les frontières.
- `Datagram` (ex. UDP, Unix datagram) : pass-through ; le transport préserve déjà les frontières.

Sélection à la construction via `WithProtocol(...)` (variantes lecture/écriture) ou via des helpers de transport (voir Options).

## Format wire

Préfixe de longueur compact à taille variable, suivi des octets de payload. L’ordre des octets pour la longueur étendue est configurable : `WithByteOrder`, ou par direction `WithReadByteOrder` / `WithWriteByteOrder`.

## Format des données de frame

Le schéma de framing utilisé par `framer` est volontairement compact :

- Octet d’en-tête `H0` + octets optionnels de longueur étendue.
- Soit `L` la longueur du payload en octets.
  - Si `0 ≤ L ≤ 253` (`0x00..0xFD`) : `H0 = L`. Aucun octet supplémentaire.
  - Si `254 ≤ L ≤ 65535` (`0x0000..0xFFFF`) : `H0 = 0xFE` et les 2 octets suivants encodent `L` en entier non signé 16-bit dans l’ordre configuré.
  - Si `65536 ≤ L ≤ 2^56-1` : `H0 = 0xFF` et les 7 octets suivants portent les 56 bits de poids faible de `L`, dans l’ordre configuré.
    - Big-endian : les octets `[1..7]` sont les 56 bits de poids faible de `L` en big-endian.
    - Little-endian : les octets `[1..7]` sont les 56 bits de poids faible de `L` en little-endian.

Limites et erreurs :
- Longueur maximale de payload : `2^56-1` ; au-delà, `framer.ErrTooLong`.
- Avec une limite de lecture (`WithReadLimit`), les longueurs au-delà échouent avec `framer.ErrTooLong`.

## Installation

Installer avec `go get` :
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

## Utilisation non bloquante

`framer` fonctionne en mode non bloquant par défaut. Dans une boucle événementielle :

```go
for {
    n, err := r.Read(buf)
    if n > 0 {
        process(buf[:n])
    }
    if err != nil {
        if err == framer.ErrWouldBlock {
            // Pas de données ; attendre la disponibilité en lecture (epoll, io_uring, etc.)
            continue
        }
        if err == io.EOF {
            break
        }
        log.Fatal(err)
    }
}
```

## Options

- `WithProtocol(proto Protocol)` — choisir `BinaryStream`, `SeqPacket` ou `Datagram` (variantes lecture/écriture disponibles).
- Ordre des octets : `WithByteOrder`, ou `WithReadByteOrder` / `WithWriteByteOrder`.
- `WithReadLimit(n int)` — limite la taille max du payload à la lecture.
- `WithRetryDelay(d time.Duration)` — politique would-block ; helpers : `WithNonblock()` / `WithBlock()`.

Helpers de transport (presets) :
- `WithReadTCP` / `WithWriteTCP` (`BinaryStream`, BigEndian réseau)
- `WithReadUDP` / `WithWriteUDP` (`Datagram`, BigEndian)
- `WithReadWebSocket` / `WithWriteWebSocket` (`SeqPacket`, BigEndian)
- `WithReadSCTP` / `WithWriteSCTP` (`SeqPacket`, BigEndian)
- `WithReadUnix` / `WithWriteUnix` (`BinaryStream`, BigEndian)
- `WithReadUnixPacket` / `WithWriteUnixPacket` (`Datagram`, BigEndian)
- `WithReadLocal` / `WithWriteLocal` (`BinaryStream`, ordre natif)

Voir aussi : GoDoc https://pkg.go.dev/code.hybscloud.com/framer

## Contrat sémantique (Semantics Contract)

### Taxonomie des erreurs

| Error | Signification | Action appelant |
|-------|---------------|-----------------|
| `nil` | Opération complétée avec succès | Continuer ; `n` reflète le progrès total |
| `io.EOF` | Fin de flux (plus de messages) | Arrêter la lecture ; terminaison normale |
| `io.ErrUnexpectedEOF` | Fin de flux au milieu d’un message (header/payload incomplet) | Traiter comme fatal ; corruption ou déconnexion |
| `io.ErrShortBuffer` | Buffer destination trop petit pour le payload | Réessayer avec un buffer plus grand |
| `io.ErrShortWrite` | Le Writer destination a accepté moins d’octets que fourni | Réessayer ou traiter comme fatal selon le contexte |
| `io.ErrNoProgress` | Le Reader sous-jacent n’a pas progressé (`n==0, err==nil`) avec un buffer non vide | Traiter comme fatal ; indique un `io.Reader` cassé |
| `framer.ErrWouldBlock` | Pas de progrès possible maintenant sans attendre | Réessayer plus tard (après poll/event) ; `n` peut être >0 |
| `framer.ErrMore` | Progrès réalisé ; d’autres completions suivront pour la même op | Traiter puis rappeler |
| `framer.ErrTooLong` | Message dépasse une limite ou le maximum du wire format | Rejeter ; possiblement fatal |
| `framer.ErrInvalidArgument` | Reader/Writer nil ou config invalide | Corriger la configuration |

### Tables de résultats

**`Reader.Read(p []byte) (n int, err error)`** — mode BinaryStream

| Condition | n | err |
|----------|---|-----|
| Message complet livré | payload length | `nil` |
| `len(p) < payload length` | 0 | `io.ErrShortBuffer` |
| Payload dépasse ReadLimit | 0 | `ErrTooLong` |
| Sous-jacent retourne would-block | octets lus jusqu’ici | `ErrWouldBlock` |
| Sous-jacent retourne more | octets lus jusqu’ici | `ErrMore` |
| EOF à la frontière de message | 0 | `io.EOF` |
| EOF au milieu du header/payload | octets lus | `io.ErrUnexpectedEOF` |

**`Writer.Write(p []byte) (n int, err error)`** — mode BinaryStream

| Condition | n | err |
|----------|---|-----|
| Message encadré émis complètement | `len(p)` | `nil` |
| Payload dépasse le max (2^56-1) | 0 | `ErrTooLong` |
| Sous-jacent retourne would-block | octets payload écrits | `ErrWouldBlock` |
| Sous-jacent retourne more | octets payload écrits | `ErrMore` |

**`Reader.WriteTo(dst io.Writer) (n int64, err error)`**

| Condition | n | err |
|----------|---|-----|
| Transfert jusqu’à EOF | total octets payload | `nil` |
| Reader sous-jacent retourne would-block | octets payload écrits | `ErrWouldBlock` |
| Reader sous-jacent retourne more | octets payload écrits | `ErrMore` |
| `dst` retourne would-block | octets payload écrits | `ErrWouldBlock` |
| Message dépasse le buffer interne (64KiB par défaut) | octets jusqu’ici | `ErrTooLong` |
| Fin de flux au milieu d’un message | octets jusqu’ici | `io.ErrUnexpectedEOF` |

**`Writer.ReadFrom(src io.Reader) (n int64, err error)`**

| Condition | n | err |
|----------|---|-----|
| Chunks encodés jusqu’à src EOF | total octets payload | `nil` |
| `src` retourne would-block | octets payload écrits | `ErrWouldBlock` |
| `src` retourne more | octets payload écrits | `ErrMore` |
| Writer sous-jacent retourne would-block | octets payload écrits | `ErrWouldBlock` |

**`Forwarder.ForwardOnce() (n int, err error)`**

| Condition | n | err |
|----------|---|-----|
| Un message relayé complètement | octets payload (phase écriture) | `nil` |
| Source packet retourne `(n>0, io.EOF)` | octets payload (phase écriture) | `nil` (prochain appel : `io.EOF`) |
| Plus de messages | 0 | `io.EOF` |
| Would-block en phase lecture | octets lus dans cet appel | `ErrWouldBlock` |
| Would-block en phase écriture | octets écrits dans cet appel | `ErrWouldBlock` |
| Message dépasse le buffer interne | 0 | `io.ErrShortBuffer` |
| Message dépasse ReadLimit | 0 | `ErrTooLong` |
| Fin de flux au milieu d’un message | octets jusqu’ici | `io.ErrUnexpectedEOF` |

### Classification des opérations

| Opération | Comportement frontière | Cas d’usage |
|----------|-------------------------|------------|
| `Reader.Read` | **Préserve les frontières** : 1 appel = 1 message | Traitement applicatif par message |
| `Writer.Write` | **Préserve les frontières** : 1 appel = 1 message | Envoi applicatif par message |
| `Reader.WriteTo` | **Chunking** : flux d’octets payload (pas wire format) | Transfert efficace ; ne préserve PAS les frontières |
| `Writer.ReadFrom` | **Chunking** : chaque chunk de `src` devient un message | Encodage efficace ; ne préserve PAS les frontières amont |
| `Forwarder.ForwardOnce` | **Relais préservant les frontières** : décoder un, ré-encoder un | Proxy conscient des messages |

### Politique de blocage

Par défaut, framer est **non bloquant** (`WithNonblock()`) : retourne `ErrWouldBlock` immédiatement.

- `WithBlock()` — yield (`runtime.Gosched`) et réessaye sur would-block
- `WithRetryDelay(d)` — sleep `d` et réessaye sur would-block
- `RetryDelay` négatif (par défaut) — retourne `ErrWouldBlock` immédiatement

Aucune méthode ne masque un blocage sans configuration explicite.

`framer` utilise les signaux de contrôle de flux de `code.hybscloud.com/iox`. `ErrWouldBlock` et `ErrMore` sont des alias de `iox`, permettant l’intégration directe avec d’autres composants compatibles `iox` (`iofd`, `takt`).

## Fast paths

`framer` implémente les fast paths du stdlib pour interopérer avec des moteurs type `io.Copy` et `iox.CopyPolicy` :

- `(*Reader).WriteTo(io.Writer)` — transfère efficacement les payloads vers `dst`.
  - Stream (`BinaryStream`) : traite un message à la fois et écrit uniquement les octets de payload. Si `ReadLimit == 0`, un plafond conservateur (64KiB) est utilisé ; au-delà, `framer.ErrTooLong`.
  - Packet (`SeqPacket`/`Datagram`) : pass-through.
  - `framer.ErrWouldBlock` et `framer.ErrMore` sont propagées telles quelles, avec un compteur reflétant les octets écrits.

- `(*Writer).ReadFrom(io.Reader)` — chunk-to-message : chaque chunk lu depuis `src` est encodé comme un message et écrit via `w.Write`.
  - Efficace mais ne préserve pas les frontières applicatives de `src`.
  - Sur les protocoles préservant les frontières, c’est un pass-through.
  - `framer.ErrWouldBlock` et `framer.ErrMore` sont propagées telles quelles.

Recommandation : dans les boucles non bloquantes, préférez `iox.CopyPolicy` avec une politique de retry (ex. `PolicyRetry`) pour traiter explicitement `ErrWouldBlock` / `ErrMore`.

**Zéro allocation en régime établi** : Après l’allocation initiale du buffer, les chemins `Forwarder` et `WriteTo` réutilisent les buffers internes. Aucune allocation sur le tas ne se produit par message en régime établi.

**Note sur la récupération des écritures partielles :** Lors de l'utilisation de `iox.Copy` avec des destinations non bloquantes, des écritures partielles peuvent survenir. Si la source n'implémente pas `io.Seeker`, `iox.Copy` retourne `iox.ErrNoSeeker` pour éviter une perte silencieuse de données. Pour les sources non repositionnables (ex. sockets réseau), utilisez `iox.CopyPolicy` avec `PolicyRetry` pour les erreurs sémantiques côté écriture, afin de garantir que tous les octets lus soient écrits avant le retour.

## Relais

- Proxy wire (moteurs d’octets) : utilisez `iox.CopyPolicy` et les fast paths (`WriterTo`/`ReaderFrom`). Maximise le débit lorsque vous n’avez pas besoin de préserver des frontières de niveau supérieur.
- Relais message (préserve les frontières) : utilisez `framer.NewForwarder(dst, src, ...)` et appelez `ForwardOnce()` dans votre boucle de poll. Décode exactement un message depuis `src` et le ré-encode comme exactement un message vers `dst`.
  - Non bloquant : `ForwardOnce` retourne `(n>0, framer.ErrWouldBlock|framer.ErrMore)` en cas de progrès partiel ; réessayez avec la même instance.
  - Limites : `io.ErrShortBuffer` si le buffer interne est insuffisant ; `framer.ErrTooLong` si le message dépasse `WithReadLimit`.
  - Zéro allocation en régime établi après construction ; buffer interne réutilisé.

Exemple de relais message :

```go
fwd := framer.NewForwarder(dst, src, framer.WithReadTCP(), framer.WithWriteTCP())

for {
    _, err := fwd.ForwardOnce()
    if err != nil {
        if err == framer.ErrWouldBlock {
            continue // attendre src lisible ou dst écrivable
        }
        if err == io.EOF {
            break
        }
        log.Fatal(err)
    }
}
```

## Licence

MIT — voir [LICENSE](LICENSE).

©2025 [Hayabusa Cloud Co., Ltd.](https://code.hybscloud.com)
