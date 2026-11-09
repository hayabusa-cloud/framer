# framer — frontières de messages en E/S de flux

[![Go Reference](https://pkg.go.dev/badge/code.hybscloud.com/framer.svg)](https://pkg.go.dev/code.hybscloud.com/framer)
[![Go Report Card](https://goreportcard.com/badge/github.com/hayabusa-cloud/framer)](https://goreportcard.com/report/github.com/hayabusa-cloud/framer)
[![Coverage Status](https://codecov.io/gh/hayabusa-cloud/framer/graph/badge.svg)](https://codecov.io/gh/hayabusa-cloud/framer)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Langues / Languages:** [English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md) | Français

Encapsulation de messages en trames, portable pour Go. Préserve “un message par `Read`/`Write`” au-dessus des transports en flux.

Portée : préservation des frontières de messages dans les transports en flux.

## Vue d’ensemble

Les flux d'octets comme TCP, Unix stream et les pipes ne préservent pas les frontières des messages applicatifs. Framer ajoute des préfixes de longueur pour que chaque opération de lecture/écriture terminée corresponde à un message. Une opération non bloquante peut nécessiter plusieurs appels avec état conservé.

- Préservation des frontières de message sur les flux d’octets (TCP, Unix stream, pipes).
- Pass-through sur les transports qui préservent déjà les frontières (UDP, Unix datagram, WebSocket, SCTP).
- Format sur le fil portable ; ordre des octets configurable.

## Adaptation de protocole

- `BinaryStream` (transports stream : TCP, TLS-over-TCP, Unix stream, pipes) : ajoute un préfixe de longueur ; lit/écrit des messages entiers.
- `SeqPacket` (ex. SCTP, WebSocket) : passage direct ; le transport préserve déjà les frontières.
- `Datagram` (ex. UDP, Unix datagram) : passage direct ; le transport préserve déjà les frontières.
- Pour `Reader.Read`, les modes paquet sont en passage direct : `WithReadLimit` est vérifié après une réception, donc un paquet surdimensionné peut retourner `n > limit` avec `ErrTooLong` ; `n` est le nombre d’octets copiés depuis ce paquet.
- Les chemins de sortie paquet réessaient les paquets entiers seulement après `ErrWouldBlock` / `ErrMore` sans progrès ; un paquet non vide entièrement accepté et retourné avec `ErrWouldBlock` ou `ErrMore` n'est pas rejoué, et les écritures partielles de paquet sont reportées comme `io.ErrShortWrite`.

Sélection à la construction via `WithProtocol(...)` (variantes lecture/écriture) ou via des fonctions utilitaires de transport (voir Options).

## Format sur le fil

Préfixe de longueur compact à taille variable, suivi des octets de charge utile. L’ordre des octets pour la longueur étendue est configurable : `WithByteOrder`, ou par direction `WithReadByteOrder` / `WithWriteByteOrder`.

## Format des données de trame

Le schéma de framing utilisé par `framer` est volontairement compact :

- Octet d’en-tête `H0` + octets optionnels de longueur étendue.
- Soit `L` la longueur de la charge utile en octets.
  - Si `0 ≤ L ≤ 253` (`0x00..0xFD`) : `H0 = L`. Aucun octet supplémentaire.
  - Si `254 ≤ L ≤ 65535` (`0x0000..0xFFFF`) : `H0 = 0xFE` et les 2 octets suivants encodent `L` en entier non signé 16-bit dans l’ordre configuré.
  - Si `65536 ≤ L ≤ 2^56-1` : `H0 = 0xFF` et les 7 octets suivants portent les 56 bits de poids faible de `L`, dans l’ordre configuré.
    - Big-endian : les octets `[1..7]` sont les 56 bits de poids faible de `L` en big-endian.
    - Little-endian : les octets `[1..7]` sont les 56 bits de poids faible de `L` en little-endian.

Limites et erreurs :
- Longueur maximale de charge utile : `2^56-1` ; au-delà, `framer.ErrTooLong`.
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

En BinaryStream, conservez `r`, le `buf` entier et le compteur cumulé `received` dans l'état de la connexion. Chaque traitement de la boucle d'événements effectue un appel :

```go
n, err := r.Read(buf)
received += n
if err != nil {
	return err
}
process(buf[:received])
received = 0
```

Sur `ErrWouldBlock` ou `ErrMore`, conservez cet état et rendez la main à l'ordonnanceur. Reprenez avec le même buffer selon les données disponibles et le budget de travail. Traitez le message complet uniquement si l'erreur est nil, y compris les messages vides. `n` compte les octets de charge utile copiés pendant cet appel ; un appel terminant une progression antérieure peut retourner zéro.

## Codec BinaryStream avec entrée empruntée

Utilisez `Decoder` pour des octets déjà reçus dans les buffers de l'appelant.
Sa valeur zéro sélectionne big-endian et le maximum du format ;
`NewDecoder(order, limit)` accepte nil ou les ordres standard, avec une limite
nulle pour le maximum du format.

`Decode(src)` retourne les **octets du format consommés** et un `Chunk` :
`Data` est une vue de la charge utile dont la capacité égale la longueur,
`Length` est la longueur totale et `Begin`/`End` marquent les frontières.
Chaque appel s'arrête à la fin du message courant. Traitez le résultat et
avancez du nombre d'octets consommés, sans les rejouer. Gardez le stockage
valide pendant l'utilisation de `Data` ; le décodeur ne conserve aucune entrée
ni charge utile.

`nil` termine un message. `ErrMore` indique une progression dans une trame
incomplète, éventuellement limitée à l'en-tête ; il ne garantit pas d'autres
données disponibles. Une entrée vide retourne `ErrWouldBlock` : attendez
d'autres données sans scrutation continue. Appelez `Finish()` en fin d'entrée
pour détecter une troncature. Les échecs persistent jusqu'à `Reset()`, qui
conserve la configuration et exige un nouveau flux ou une frontière établie.

`EncodeHeader` écrit seulement l'en-tête minimal dans le stockage fourni ;
les erreurs le laissent intact. Les deux opérations évitent copie de charge
utile, allocation sur le tas et attente. L'appelant contrôle le budget de
traitement et la durée de vie des buffers.

```go
var header [framer.MaxHeaderSize]byte
n, err := framer.EncodeHeader(header[:], uint64(len(payload)), nil)
if err != nil {
    return err
}
// Envoyez header[:n] puis payload ; conservez les deux jusqu'à leur libération par l'E/S.
```

Voir l'[exemple de décodage incrémental](codec_example_test.go).

## Options

- `WithProtocol(proto Protocol)` : choisir `BinaryStream`, `SeqPacket` ou `Datagram` (variantes lecture/écriture disponibles).
- Ordre des octets : `WithByteOrder`, ou `WithReadByteOrder` / `WithWriteByteOrder`.
- `WithReadLimit(n int)` : limite la taille maximale de la charge utile à la lecture ; `Reader.Read` l’applique post-lecture en modes paquet et peut retourner `n > limit` avec `ErrTooLong`.
- `WithRetryDelay(d time.Duration)` : définit la politique `ErrWouldBlock` sans progrès ; une valeur négative retourne `ErrWouldBlock` immédiatement, zéro cède l’exécution et réessaye, et une valeur positive attend `d` avant de réessayer. Si une opération a déjà transféré des octets, elle retourne le compteur positif avec `ErrWouldBlock` afin que l’appelant traite ce progrès avant de réessayer ; options associées : `WithNonblock()` / `WithBlock()`.

Fonctions utilitaires de transport (préréglages) :
- `WithReadTCP` / `WithWriteTCP` (`BinaryStream`, BigEndian réseau)
- `WithReadUDP` / `WithWriteUDP` (`Datagram`, BigEndian)
- `WithReadWebSocket` / `WithWriteWebSocket` (`SeqPacket`, BigEndian)
- `WithReadSCTP` / `WithWriteSCTP` (`SeqPacket`, BigEndian)
- `WithReadUnix` / `WithWriteUnix` (`BinaryStream`, BigEndian)
- `WithReadUnixPacket` / `WithWriteUnixPacket` (`Datagram`, BigEndian)
- `WithReadLocal` / `WithWriteLocal` (`BinaryStream`, ordre natif)

Voir aussi : GoDoc https://pkg.go.dev/code.hybscloud.com/framer

## Contrat sémantique

### Note sur les modes paquet (`SeqPacket` / `Datagram`)

- Le mode paquet préserve les frontières du transport et ne découpe pas les paquets.
- `(0, nil)` d'une source paquet termine un message vide ; zéro octet avec une erreur source ne forme pas de message. Un paquet vide en sortie n'est accepté qu'avec `nil` ; `(0, ErrWouldBlock)` / `(0, ErrMore)` reste en attente.
- `Reader.Read` applique `WithReadLimit` après une lecture de paquet ; les chemins utilitaires de transfert utilisent un octet sentinelle pour rejeter les paquets surdimensionnés avant de transférer des octets.
- Les destinations qui préservent les paquets réessaient le paquet entier seulement après `ErrWouldBlock` / `ErrMore` sans progrès ; un paquet non vide entièrement accepté et retourné avec `ErrWouldBlock` ou `ErrMore` n'est pas rejoué, et une écriture partielle de paquet est un échec de frontière et retourne `io.ErrShortWrite`.
- `Reader.WriteTo` vers un `io.Writer` arbitraire est un transfert d’octets avec reprise par suffixe. Lorsque la destination est un `framer.Writer`, il utilise l’algèbre de destination : les destinations paquet réessaient le paquet entier après zéro progrès, et les destinations flux réessaient la même trame en cours.
- Si une source paquet retourne `(n > 0, err)`, `Reader.WriteTo` émet le paquet admis avant de reporter `err` ; une suspension côté écriture conserve ce signal de source en attente pendant le réessai.
- Les compteurs de progrès dépendent de l’opération : `Reader.Read` rapporte les octets copiés dans `p`, `Reader.WriteTo` rapporte les octets écrits vers `dst`, `Writer.ReadFrom` rapporte les octets lus depuis `src` et admis dans l’état du writer, et `Forwarder.ForwardOnce` rapporte le progrès de sa phase courante.

### Discipline de réessai

- `ErrWouldBlock` est une suspension de disponibilité, pas un échec ; les chemins agrégés peuvent retourner un compteur positif si des étapes précédentes de la boucle ont progressé avant la suspension.
- `ErrMore` conserve une opération active ; ce n'est pas EOF et cela ne garantit pas d'entrée disponible. Traitez la progression puis reprenez selon le contrat de l'opération et le budget de l'appelant.
- Réessayez `Reader.Read` après un progrès partiel de flux sur le même `Reader` avec le même tampon.
- Réessayez `Writer.Write` après une suspension BinaryStream sur le même `Writer` avec la même longueur de message ; les octets d’en-tête BinaryStream ne sont pas inclus dans `n`. L'en-tête d'une trame vide peut être écrit avant la suspension ; le réessai termine la même trame sans répéter l'en-tête. En modes paquet, `n == len(p) && n > 0` avec `ErrWouldBlock` ou `ErrMore` signifie que le paquet a été accepté ; ne rejouez pas `p`.
- Réessayez `Reader.WriteTo` sur le même `Reader` et la même destination, `Writer.ReadFrom` sur le même `Writer`, et `Forwarder.ForwardOnce` sur le même `Forwarder`.

### Contrat de performance

- Les chemins critiques minimisent les vérifications à l’exécution pour garder un débit stable.
- L’appelant est responsable des options et tampons valides, ainsi que du réessai propre à chaque opération après `ErrWouldBlock` ou `ErrMore`.

### Taxonomie des erreurs

| Error | Signification | Action appelant |
|-------|---------------|-----------------|
| `nil` | Opération complétée avec succès | Continuer ; n indique la progression de cet appel |
| `io.EOF` | Fin de flux (plus de messages) | Arrêter la lecture ; terminaison normale |
| `io.ErrUnexpectedEOF` | Fin de flux au milieu d’un message (en-tête ou charge utile incomplète) | Traiter comme fatal ; corruption ou déconnexion |
| `io.ErrShortBuffer` | Tampon destination trop petit pour la charge utile | Réessayer avec un tampon plus grand |
| `io.ErrShortWrite` | Le Writer destination a accepté moins d’octets que fourni | Réessayer ou traiter comme fatal selon le contexte |
| `io.ErrNoProgress` | Le Reader de flux ou la source générique de ReadFrom a retourné `(0, nil)` avec un tampon non vide | Arrêter ou déléguer la reprise à l'appelant ; framer ne scrute pas sans progrès |
| `framer.ErrWouldBlock` | Pas de progrès possible maintenant sans attendre | Réessayer plus tard (après poll/event) ; `n` peut être >0 |
| `framer.ErrMore` | La même opération a encore du progrès à livrer, distincte d’EOF et d’une suspension de disponibilité | Traiter la progression ; reprendre selon le contrat d'entrée et d'ordonnancement |
| `framer.ErrTooLong` | Message dépasse une limite configurée, un plafond de transfert ou une borne du format sur le fil | Rejeter ; possiblement fatal |
| `framer.ErrInvalidArgument` | Reader/Writer nil ou config invalide | Corriger la configuration |

### Tables de résultats

Les compteurs Reader/Writer ci-dessous concernent chaque appel, pas la longueur cumulée du message. Additionnez-les entre les reprises ; nil peut terminer une progression antérieure avec `n == 0`.

**`Reader.Read(p []byte) (n int, err error)`** (mode BinaryStream)

| Condition | n | err |
|----------|---|-----|
| Message complet livré | octets de charge utile copiés dans cet appel ; 0 possible | `nil` |
| `len(p) < longueur de charge utile` | 0 | `io.ErrShortBuffer` |
| La charge utile dépasse ReadLimit | 0 | `ErrTooLong` |
| Le sous-jacent retourne `ErrWouldBlock` | octets de charge utile copiés dans cet appel ; 0 possible | `ErrWouldBlock` |
| Sous-jacent retourne more | octets de charge utile copiés dans cet appel ; 0 possible | `ErrMore` |
| EOF à la frontière de message | 0 | `io.EOF` |
| EOF au milieu de l’en-tête ou de la charge utile | octets de charge utile copiés dans cet appel ; 0 possible | `io.ErrUnexpectedEOF` |

**`Writer.Write(p []byte) (n int, err error)`** (mode BinaryStream)

| Condition | n | err |
|----------|---|-----|
| Message encadré émis complètement | octets de charge utile acceptés dans cet appel ; 0 possible | `nil` |
| La charge utile dépasse le maximum (2^56-1) | 0 | `ErrTooLong` |
| Le sous-jacent retourne `ErrWouldBlock` | octets de charge utile acceptés dans cet appel ; 0 possible | `ErrWouldBlock` |
| Sous-jacent retourne more | octets de charge utile acceptés dans cet appel ; 0 possible | `ErrMore` |

**`Reader.WriteTo(dst io.Writer) (n int64, err error)`**

| Condition | n | err |
|----------|---|-----|
| Transfert jusqu’à EOF | total des octets de charge utile | `nil` |
| Le Reader sous-jacent retourne `ErrWouldBlock` | octets de charge utile écrits | `ErrWouldBlock` |
| Reader sous-jacent retourne more | octets de charge utile écrits | `ErrMore` |
| `dst` retourne `ErrWouldBlock` | octets de charge utile écrits | `ErrWouldBlock` |
| Source paquet dépasse ReadLimit avant transfert | octets déjà écrits avant ce paquet | `ErrTooLong` |
| Message dépasse le tampon interne (64KiB par défaut) | octets jusqu’ici | `ErrTooLong` |
| Fin de flux au milieu d’un message | octets jusqu’ici | `io.ErrUnexpectedEOF` |

**`Writer.ReadFrom(src io.Reader) (n int64, err error)`**

| Condition | n | err |
|----------|---|-----|
| Chunks encodés jusqu’à src EOF | total des octets lus depuis `src` | `nil` |
| `src` retourne `(0, nil)` | octets déjà admis dans cet appel | `io.ErrNoProgress` |
| `src` retourne `ErrWouldBlock` | octets lus depuis `src` avant le signal | `ErrWouldBlock` |
| `src` retourne more | octets lus depuis `src` avant le signal | `ErrMore` |
| Le Writer sous-jacent retourne `ErrWouldBlock` | octets lus depuis `src` et admis avant la suspension ; 0 lors d’une reprise seulement côté écriture | `ErrWouldBlock` |
| Writer sous-jacent retourne more | octets lus depuis `src` et admis avant la suspension ; 0 lors d’une reprise seulement côté écriture | `ErrMore` |

**`Forwarder.ForwardOnce() (n int, err error)`**

| Condition | n | err |
|----------|---|-----|
| Un message relayé complètement | octets de charge utile acceptés dans cet appel ; 0 possible | `nil` |
| Source paquet retourne `(n > 0, io.EOF)` | octets de charge utile acceptés dans cet appel ; 0 possible | `nil` (prochain appel : `io.EOF`) |
| Plus de messages | 0 | `io.EOF` |
| La source retourne `ErrWouldBlock` | octets lus si aucun paquet n'a été émis ; une source paquet avec `n > 0` est émise d'abord et retourne les octets de charge utile (phase écriture) | `ErrWouldBlock` |
| La source retourne more | octets lus si aucun paquet n'a été émis ; une source paquet avec `n > 0` est émise d'abord et retourne les octets de charge utile (phase écriture) | `ErrMore` |
| Would-block en phase écriture | octets écrits dans cet appel | `ErrWouldBlock` |
| More en phase écriture | octets écrits dans cet appel | `ErrMore` |
| Message de flux ou capacité de lecture de paquet requise dépasse le tampon interne | 0 | `io.ErrShortBuffer` |
| Paquet dépasse ReadLimit/plafond de transfert paquet par défaut avant transfert | octets lus depuis le paquet, non transférés | `ErrTooLong` |
| Fin de flux au milieu d’un message | octets jusqu’ici | `io.ErrUnexpectedEOF` |

### Classification des opérations

| Opération | Comportement frontière | Cas d’usage |
|----------|-------------------------|------------|
| `Reader.Read` | **Préserve les messages** : une opération terminée correspond à un message ; des reprises peuvent être nécessaires | Traitement applicatif par message |
| `Writer.Write` | **Préserve les messages** : une opération terminée correspond à un message ; des reprises peuvent être nécessaires | Envoi applicatif par message |
| `Reader.WriteTo` | **Transfert d’octets** vers les destinations arbitraires ; les destinations `framer` connues préservent la loi de réessai paquet/trame | Transfert efficace avec reprise par suffixe |
| `Writer.ReadFrom` | **Découpage en blocs** : chaque bloc de `src` devient un message ; la sortie paquet réessaie le paquet entier seulement après zéro progrès | Encodage efficace ; ne préserve PAS les frontières amont |
| `Forwarder.ForwardOnce` | **Relais préservant les frontières** : décoder un, ré-encoder un | Proxy conscient des messages |

### Politique de blocage

Par défaut, framer est **non bloquant** (`WithNonblock()`) : retourne `ErrWouldBlock` immédiatement.

- `WithBlock()` ou `WithRetryDelay(0)` : cède l’exécution (`runtime.Gosched`) et réessaye sur `ErrWouldBlock` sans progrès
- `WithRetryDelay(d > 0)` : attend `d` et réessaye sur `ErrWouldBlock` sans progrès
- `RetryDelay` négatif (par défaut) : retourne immédiatement `ErrWouldBlock` sans progrès
- Si une lecture ou une écriture a déjà transféré des octets, framer retourne le compteur positif avec `ErrWouldBlock` ; traitez le progrès puis rappelez la même opération comme documenté ci-dessus.

`WithNonblock()` désactive les reprises would-block de framer. Il ne transforme pas un transport bloquant en transport non bloquant.

`framer` utilise les signaux de contrôle de flux de `code.hybscloud.com/iox`. `ErrWouldBlock` et `ErrMore` sont des alias de `iox`, permettant l’intégration directe avec d’autres composants compatibles `iox` (`iofd`, `takt`).

## Chemins rapides

`framer` implémente les chemins rapides de la bibliothèque standard pour interopérer avec des moteurs type `io.Copy` et `iox.CopyPolicy` :

- `(*Reader).WriteTo(io.Writer)` : transfère efficacement les charges utiles vers `dst`.
  - Stream (`BinaryStream`) : traite un message à la fois et écrit uniquement les octets de charge utile. Si `ReadLimit == 0`, un plafond conservateur (64KiB) est utilisé ; au-delà, `framer.ErrTooLong`.
  - Packet (`SeqPacket`/`Datagram`) : transfert d’octets en passage direct ; les lectures avec capacité sentinelle rejettent les paquets surdimensionnés avant transfert, et `n` compte les octets écrits vers `dst`.
  - Les erreurs côté écriture `framer.ErrWouldBlock` et `framer.ErrMore` sont propagées telles quelles, avec un compteur reflétant les octets écrits ; les erreurs de source paquet retournées avec des octets sont reportées après l’émission du paquet admis.

- `(*Writer).ReadFrom(io.Reader)` : découpage en blocs vers messages ; chaque bloc non vide lu depuis `src` est encodé comme un message et écrit via `w.Write`.
  - Efficace mais ne préserve pas les frontières applicatives de `src`.
  - La sortie paquet émet chaque bloc comme un paquet ; elle ne détermine pas le protocole d'entrée. Une source retournant `(0, nil)` entraîne immédiatement `io.ErrNoProgress`, sans émission de message vide.
  - `framer.ErrWouldBlock` et `framer.ErrMore` sont propagées telles quelles ; `n` compte les octets lus depuis `src` et admis dans l’état du writer.

Recommandation : dans les boucles non bloquantes, préférez `iox.CopyPolicy` avec une politique de réessai (ex. `PolicyRetry`) pour traiter explicitement `ErrWouldBlock` / `ErrMore`.

**Zéro allocation en régime établi** : après l’allocation initiale du tampon, les chemins `Forwarder` et `WriteTo` réutilisent les tampons internes. Aucune allocation sur le tas ne se produit par message en régime établi.

**Note sur la récupération des écritures partielles :** Lors de l'utilisation de `iox.Copy` avec des destinations non bloquantes, des écritures partielles peuvent survenir. Si la source n'implémente pas `io.Seeker`, `iox.Copy` retourne `iox.ErrNoSeeker` pour éviter une perte silencieuse de données. Pour les sources non repositionnables (ex. sockets réseau), utilisez `iox.CopyPolicy` avec `PolicyRetry` pour les erreurs sémantiques côté écriture, afin de garantir que tous les octets lus soient écrits avant le retour.

## Relais

- Proxy au niveau du fil (moteurs d’octets) : utilisez `iox.CopyPolicy` et les chemins rapides (`WriterTo`/`ReaderFrom`) lorsque le transfert au niveau des octets suffit et que les frontières de niveau supérieur n’ont pas besoin d’être préservées.
- Relais message (préserve les frontières) : utilisez `framer.NewForwarder(dst, src, ...)` et appelez `ForwardOnce()` dans votre boucle de scrutation. Décode exactement un message depuis `src` et le ré-encode comme exactement un message vers `dst`.
  - Non bloquant : réessayez avec la même instance après `framer.ErrWouldBlock` ou `framer.ErrMore` ; une source paquet avec `(n > 0, err)` est émise avant de reporter le signal de source, et une suspension côté écriture conserve ce signal pour le réessai ultérieur sur le même `Forwarder`.
  - Limites : `io.ErrShortBuffer` si le tampon interne est insuffisant pour un message de flux ou la capacité de lecture de paquet requise ; `framer.ErrTooLong` si un paquet dépasse `WithReadLimit` ou le plafond de transfert de paquet par défaut avant transfert.
  - Zéro allocation en régime établi après construction ; tampon interne réutilisé.

Exemple de relais message :

```go
fwd := framer.NewForwarder(dst, src, framer.WithReadTCP(), framer.WithWriteTCP())
```

Construisez le relais une seule fois. Chaque traitement ultérieur utilise la même instance et rend le signal de contrôle à l'appelant :

```go
_, err := fwd.ForwardOnce()
return err
```

## Licence

MIT — voir [LICENSE](LICENSE).

©2025 [Hayabusa Cloud Co., Ltd.](https://code.hybscloud.com)
