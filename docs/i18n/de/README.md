# go-scout

> Eine in Go geschriebene Such-Synchronisierungs-Bibliothek — ein Go-Port von Laravel Scout (basiert auf dem PHP-Plugin [webman-scout](https://github.com/shopwwi/webman-scout)).
> Go 1.24 · reine Standardbibliothek, null Drittanbieter-Abhängigkeiten · Version v1.2.0

## Projektübersicht

go-scout löst das Problem der "Synchronisierung zwischen Modell und Suchmaschine": Modelle, die das Interface `scout.ScoutModel` implementieren, werden beim Speichern, Löschen oder Wiederherstellen automatisch in die Suchmaschine geschrieben bzw. daraus entfernt und über eine einheitliche, verkettbare Query-API durchsucht. Die API-Unterschiede der einzelnen Suchmaschinen (Elasticsearch, OpenSearch, Meilisearch, Typesense, Algolia, XunSearch…) bleiben dabei vollständig verborgen.

Es spiegelt die Klassenstruktur und das Verhalten der ursprünglichen PHP-Bibliothek:

| PHP (webman/laravel-scout) | Go (go-scout) |
|---|---|
| `Scout` | `scout.Scout` (scout.go) |
| `Builder` | `scout.Builder` (builder.go / builder_search.go) |
| `EngineManager` | `scout.Manager` / `scout.EngineManager` (manager.go) |
| `ModelObserver` | `scout.ModelObserver` (observer.go) |
| `Searchable` trait | `scout.Searchable` (searchable.go) |
| `Engine` / `AdvancedEngine` und die Engine-Klassen | `scout.Engine` / `scout.AdvancedEngine` sowie die 9 Engines im Paket `engines` |

Eingebaute Engines (Paket `engines`):

- **null** — leere Implementierung, Standard-Fallback-Treiber (wird bei deaktivierter Indizierung verwendet)
- **collection** — In-Memory-Suche, keine externen Dienste nötig
- **database** — Tabelle als Index: LIKE/ILIKE und Volltextsuche direkt auf Datenbanktabellen (mysql / pgsql / sqlite)
- **elasticsearch** / **opensearch** — REST-Queries, gemeinsame Nutzung der DSL-Zwischendarstellung in `engines/dsl.go`
- **meilisearch** — REST-Queries mit Vektor- & Hybridsuche, Filtern, Sortieren, Facetten
- **typesense** — REST-Queries mit Aggregationen, Gruppierung, Vektor-Nearest-Neighbor
- **algolia** — REST-Queries, Versionen v3 / v4
- **xunsearch** — HTTP-Daemon-Protokoll (Index-Seite + Such-Seite)

Zusammen mit den `advanced_*`-Aliasen registriert `engines.Register` insgesamt 15 Treibernamen (siehe [engines/register.go](engines/register.go)).

## Architektur

![Architektur-Ebenen-Diagramm](docs/i18n/de/arch.svg)

Fünf Ebenen von unten nach oben, die Verantwortung wird pro Ebene enger:

1. **Modellebene** — Geschäftsmodelle implementieren `scout.ScoutModel` (`ScoutKey`, `ToSearchableArray`, `ShouldBeSearchable`, `TableName` usw.; siehe [model.go](model.go)). Die Daten werden über das Interface `scout.Source[T]` (`All` / `ByIDs` / `Count`) geliefert, eingebaut ist `scout.NewMemorySource`. Optionale Interfaces wie `SoftDeleter`, `FullTextColumner`, `PrefixColumner` werden bei Bedarf implementiert.
2. **Sync-Ebene** — `scout.ModelObserver` fängt die Speicher-/Lösch-/Wiederherstell-Events der Modelle ab; `scout.EventBus` veröffentlicht die Events `scout.models_imported` und `scout.models_flushed`; `scout.Queue` stellt eine prozessinterne asynchrone Warteschlange bereit (Puffer 4096) mit synchronem Fallback, wenn die Warteschlange nicht verfügbar ist.
3. **Query-Ebene** — `scout.Builder` akkumuliert den Query-Zustand über verkettete Aufrufe (`Where`, `OrderBy`, `Take`, `WithTrashed`…). Die Ausführungsmethoden in `builder_search.go` (`Get` / `First` / `Paginate` / `Cursor` / `Keys`) lösen genau einen Engine-Roundtrip aus; Ergebnisse werden zu `scout.Result` / `PaginationResult` normalisiert.
4. **Engine-Adapter-Ebene** — das Interface `Engine` (`Search`, `Paginate`, `Update`, `Delete`, `Map`, `Flush`, `CreateIndex`, `DeleteIndex`…) und das Erweiterungs-Interface `AdvancedEngine` (`AdvancedSearch`, `GetAggregations`, `GetFacets`) isolieren die Engine-Unterschiede hinter den Interfaces; `scout.Manager` registriert Treiber per `Extend` und erstellt bzw. cached sie lazy über `Driver(name)`.
5. **Engine-Ebene** — 9 Engine-Implementierungen im Paket `engines`, die jeweils nur von der Konfiguration (`*scout.Config`) und einem HTTP-Client abhängen und voneinander nichts wissen.

## Funktionen

![Funktionen-Diagramm](docs/i18n/de/features.svg)

- **Query-Builder-Kette** — die Fluent-API von `scout.Builder`: `Query` / `Where` / `WhereIn` / `WhereNotIn` / `OrderBy` / `OrderByDesc` / `Latest` / `Oldest` / `Take` / `Skip`; erweiterte Bedingungen `VectorSearch` / `WhereRange` / `WhereGeoDistance` / `FulltextSearch` / `OrderByVectorSimilarity` / `OrderByGeoDistance`; Callback-Injektion `QueryCB` / `CallbackCB` / `AddResultProcessor`. Auflösungsreihenfolge der Suchfelder: `Options["fields"]` → `FullTextColumner` → `FieldNames`.
- **DSL-Zwischendarstellung** — [engines/dsl.go](engines/dsl.go) kompiliert erweiterte Bedingungen in einheitliches Bool-Query-JSON (`multi_match`, `term`, `range`, `geo_distance`, `wildcard`, `regexp` usw.), das Elasticsearch / OpenSearch direkt konsumieren; `MapBooleanToBoolKey` bildet and/or/not auf `filter` / `should` / `must_not` ab.
- **Paginierung & Cursor** — `Paginate` / `PaginateRaw` / `SimplePaginate` / `Cursor` (lazy Iteration über einen gepufferten Channel); `PaginationResult` bietet `LastPage` / `HasMorePages` / `AppendQuery`; `Result.As[T]` und die Paket-generische Funktion `scout.GetAs[T]` holen Modelle ohne Typ-Assertion.
- **Soft Deletes** — Index-Dokumente tragen `__soft_deleted`-Metadaten (0/1); `OnlyTrashed` / `WithTrashed` filtern Queries; die database-Engine nutzt die Spalte `deleted_at`.
- **Asynchrone Warteschlange** — prozessinterne Queue (`chan`-Puffer 4096 + Worker-Goroutinen), aktivierbar über `SCOUT_QUEUE=1`; synchroner Fallback mit Warnung, wenn die Queue nicht verfügbar ist; `MakeAllSearchable` importiert in Chunks (Standard 500 pro Chunk).
- **Events & Observer** — `ModelObserver` lauscht auf `Saved` / `Deleted` / `Restored` und synchronisiert automatisch; `EventBus` veröffentlicht `scout.models_imported` und `scout.models_flushed`; `WithoutSyncingToSearch` deaktiviert die Synchronisierung temporär.
- **Multi-Engine-Registrierung** — `Manager.Extend` + `engines.Register` registrieren 15 Treibernamen; Treiber werden lazy erstellt und gecacht; unbekannte Treiber liefern `scout.ErrNotSupported`; der Engine-Wechsel erfordert nur eine Änderung an `SCOUT_DRIVER`.

## Design-Prinzipien

![Design-Diagramm](docs/i18n/de/design.svg)

- **Der verkettbare Builder trägt die Query-Absicht** — `Search(ctx, query, cb)` liefert einen `*scout.Builder`; verkettete Aufrufe akkumulieren nur Zustand, und erst `Get` / `First` / `Paginate` / `Cursor` lösen einen Engine-Roundtrip aus. Query-Modifikation, Abfangen des Request-Bodys und Ergebnis-Nachbearbeitung laufen komplett über Callbacks — die Engine muss sich darum nicht kümmern.
- **DSL-Zwischendarstellung** — erweiterte Bedingungen werden in Bool-Query-JSON kompiliert, das ES / OpenSearch direkt konsumieren; Meilisearch / Typesense / Algolia / database übersetzen jeweils in ihre eigene Syntax. Engine-Unterschiede bleiben in der Übersetzungsschicht.
- **Isolation der Engine-Interfaces** — `Engine` / `AdvancedEngine` sprechen nur über Dokumente und Ergebnisse, nie über Modelltypen; `MapIDs` / `Map` befüllen die Ergebnis-IDs zurück in Modelle, und `attachMeta` richtet per `KeyString` aus, sodass gefilterte Modellmengen nie verrutschen.
- **Bewusste `ponytail:`-Vereinfachungen** — Abwägungen sind im Code durchgehend mit `ponytail:`-Kommentaren markiert: prozessinterne Queue statt Redis, `MemorySource` mit linearem Scan O(ids×models), Algolias `whereNotIns` als `"0=1"`-No-op, v3/v4 als ein Struct mit Versionsflag, Zufallssortierung als `_score asc`-Platzhalter usw. Reine Standardbibliothek, null Drittanbieter-SDKs.

## Lebenszyklus

![Such-Lebenszyklus-Diagramm](docs/i18n/de/lifecycle.svg)

**Suchpfad**: `Searchable(model, source).Search(ctx, query, cb)` erstellt einen `*scout.Builder` → `Manager.Driver(name)` dispatcht die Engine (lazy erstellt und gecacht) → `engine.Search` / `Paginate` (REST / SQL / In-Memory) → geparst zu `scout.Result` (Hits · Total · Aggregations · Raw) → falls Modelle befüllt werden müssen (`Get` / `Paginate` / `First`): `MapIDs` → `Source.ByIDs` → `attachMeta` (per `KeyString` ausgerichtet, Reihenfolge der Engine bleibt erhalten) → Rückgabe von `ResultItem` / `PaginationResult`; `Keys` / `GetAggregations` / `PaginateRaw` geben direkt zurück, ohne Modelle zu laden.

**Schreibpfad**: `ModelObserver` fängt Modell-Events ab → `Queue.PushNamed("scout_make" / "scout_remove" / "scout_make_range")` führt asynchron aus (synchroner Fallback, wenn nicht aktiviert) → `engine.Update` / `Delete` (bei Soft Delete mit `__soft_deleted`-Metadaten) → `EventBus` veröffentlicht `scout.models_imported` / `scout.models_flushed`.

## Projektstruktur

```
go-scout/
├── go.mod                  # Moduldefinition: github.com/erikwang2013/go-scout · go 1.24.1 · null Abhängigkeiten
├── .gitignore              # IDE- / Cache- / Schlüsseldateien ignorieren
├── scout.go                # Scout-Fassade: Verdrahtung & Factorys für Config / Manager / Events / Queue / Observer
├── config.go               # Konfigurationsbaum: DefaultConfig + Env-Overrides + Dot-Path-Zugriff
├── engine.go               # Engine / AdvancedEngine-Interfaces + Typen Result / Hit / PaginationResult
├── manager.go              # EngineManager: Extend-Registrierung, lazy Driver-Erzeugung & Cache, Standard-null-Engine
├── builder.go              # Builder-Struct + verkettete Bedingungskonstruktion (Where / OrderBy / Take / erweitert)
├── builder_search.go       # Query-Ausführung: Raw / Get / First / Paginate / Cursor / GetAs[T]
├── searchable.go           # Searchable: Modell-Datenquellen-Bindung, Voll-Import/-Export, Soft-Delete-Metadaten
├── observer.go             # ModelObserver: Auto-Sync bei Saved / Deleted / Restored
├── events.go               # EventBus: thread-sichere synchrone Pub/Sub + Import-/Flush-Events
├── queue.go                # Queue: prozessinterne asynchrone Queue (chan 4096) + Sync-Fallback
├── model.go                # ScoutModel-Interface + optionale Erweiterungs-Interfaces + KeyName/KeyString-Helfer
├── source.go               # Source[T]-Interface + MemorySource (linearer Scan)
├── exceptions.go           # Fehlersystem ErrNotSupported / ErrScout
├── cmd/scout/main.go       # CLI-Demoprogramm: import / flush / index / queue-import, insgesamt 7 Unterbefehle
├── engines/
│   ├── engine.go           # HTTP-Client (Auth, TLS-Skip) + gemeinsame DoJSON/DoBytes-Helfer
│   ├── register.go         # SetDatabase + Register: 15 Treibernamen + Drivers()
│   ├── dsl.go              # DSL-Zwischendarstellung: Bool-Queries / Sortierung / Aggregationen / Facetten / Highlighting
│   ├── null.go             # NullEngine: leere Implementierung
│   ├── collection.go       # CollectionEngine: In-Memory-Suche (case-insensitiver Substring-Match)
│   ├── database.go         # DatabaseEngine: Tabelle als Index SQL (LIKE/ILIKE + pgsql tsvector)
│   ├── elasticsearch.go    # ElasticsearchEngine + mit OpenSearch geteilte es*-Helfer
│   ├── opensearch.go       # OpenSearchEngine (Basis-Treiber, zugleich Advanced Engine)
│   ├── meilisearch.go      # MeilisearchEngine: Filtern / Sortieren / Vektor-Hybrid / Facetten
│   ├── typesense.go        # TypesenseEngine: filter_by / Aggregationen / Gruppierung / Nearest-Neighbor
│   ├── algolia.go          # AlgoliaEngine: ein Struct für v3/v4 + Versionsflag
│   └── xunsearch.go        # XunSearchEngine: Index-Daemon + Such-Daemon
├── docs/
│   ├── arch.svg            # Architektur-Ebenen-Diagramm
│   ├── features.svg        # Funktionen-Diagramm
│   ├── design.svg          # Design-Prinzipien-Diagramm
│   └── lifecycle.svg       # Such-Lebenszyklus-Ablaufdiagramm
└── engines/
    ├── collection_test.go  # Verhaltenstests der In-Memory-Engine (Filter / Sortierung / Paginierung / Backfill)
    ├── database_test.go    # SQL-Erzeugung & Assertions (assertSQL exakter Match)
    ├── meilisearch_test.go # Meilisearch-Request-/Parse-Tests (httptest-Stubs)
    ├── typesense_test.go   # Typesense-Request-/Parse-Tests (inkl. 404 Auto-Collection-Erstellung)
    └── null_test.go        # NullEngine-Leerverhaltenstests
```

## Schnellstart / Verwendung

### 1. Einbinden

```bash
go get github.com/erikwang2013/go-scout
```

Keine Drittanbieter-Abhängigkeiten — einbinden und loslegen.

### 2. Treiber konfigurieren

`scout.DefaultConfig()` liest Umgebungsvariablen; allgemeine Optionen:

| Umgebungsvariable | Standardwert | Beschreibung |
|---|---|---|
| `SCOUT_DRIVER` | `database` | Name des Engine-Treibers; leerer String oder `"false"` fällt auf `null` zurück |
| `SCOUT_PREFIX` | leer | Index-Präfix |
| `SCOUT_QUEUE` | aus | auf 1 setzen, um die asynchrone Queue zu aktivieren |
| `SCOUT_SOFT_DELETE` | aus | Soft-Delete-Metadaten werden mit den Dokumenten in den Index geschrieben |
| `SCOUT_CHUNK_SEARCHABLE` / `SCOUT_CHUNK_UNSEARCHABLE` | `500` | Blockgröße für Bulk-Import/-Entfernung |
| `SCOUT_BULK_SIZE` | `100` | Bulk-Schreibgröße (opensearch) |

Engine-spezifisch (Konfigurationsbaum-Pfade `engine.key` entsprechen den Env-Namen, z. B. `opensearch.host` ← `OPENSEARCH_HTTP_HOST`):

| Engine | Umgebungsvariablen (Standardwerte) |
|---|---|
| meilisearch | `MEILISEARCH_HOST` (`http://127.0.0.1:7700`), `MEILISEARCH_KEY` |
| typesense | `TYPESENSE_HOST` (`127.0.0.1`), `TYPESENSE_PORT` (`8108`), `TYPESENSE_PROTOCOL` (`http`), `TYPESENSE_API_KEY` (`xyz`), `TYPESENSE_IMPORT_ACTION` (`upsert`), `TYPESENSE_MAX_TOTAL_RESULTS` (`1000`) |
| elasticsearch | `ELASTICSEARCH_HOST` (`http://127.0.0.1:9200`, erster Eintrag der Hosts-Liste) + `elasticsearch.auth` (user/password) |
| opensearch | `OPENSEARCH_HTTP_HOST` (`https://127.0.0.1:6205`), `OPENSEARCH_USERNAME` / `OPENSEARCH_PASSWORD` (`admin`/`admin`), `OPENSEARCH_SSL_VERIFICATION` (TLS-Prüfung standardmäßig übersprungen), `OPENSEARCH_TIMEOUT` (`30` Sekunden) |
| xunsearch | `XUNSEARCH_INDEX_HOST` (`127.0.0.1`) + `XUNSEARCH_INDEX_PORT` (`8383`), `XUNSEARCH_SEARCH_HOST` + `XUNSEARCH_SEARCH_PORT` (`8384`), `XUNSEARCH_DEFAULT_INDEX` (`default`), `XUNSEARCH_CHARSET` (`utf-8`) |
| algolia | `ALGOLIA_APP_ID`, `ALGOLIA_SECRET` (fehlen sie, panict die Engine-Konstruktion) |

Der `database`-Treiber benötigt zusätzlich eine injizierte Datenbankverbindung und Dialekt:

```go
engines.SetDatabase(db, "mysql") // dialect: mysql / pgsql / postgres / sqlite
```

### 3. Minimales Beispiel

```go
package main

import (
	"context"
	"fmt"

	"github.com/erikwang2013/go-scout"
	"github.com/erikwang2013/go-scout/engines"
)

// Post nimmt an der Suche teil, indem es scout.ScoutModel implementiert
type Post struct {
	ID    int
	Title string
	Body  string
}

func (p Post) ScoutKey() any            { return p.ID }
func (p Post) TableName() string        { return "posts" }
func (p Post) ShouldBeSearchable() bool { return p.Title != "" }
func (p Post) ToSearchableArray() map[string]any {
	return map[string]any{"title": p.Title, "body": p.Body}
}

func main() {
	ctx := context.Background()

	// Initialisierung: Standardkonfiguration + alle Engines registrieren (SCOUT_DRIVER entscheidet)
	s := scout.NewWithConfig(scout.DefaultConfig())
	engines.Register(s.Manager)

	// Modell und Datenquelle binden, dann Vollimport (chunk 0 = Standardblockgröße 500)
	sr := s.Searchable(&Post{}, scout.NewMemorySource(
		Post{ID: 1, Title: "Go module layout", Body: "packages and imports"},
		Post{ID: 2, Title: "Indexing strategies", Body: "bulk writes"},
	))
	_ = sr.MakeAllSearchable(ctx, 0)

	// Verkettete Query: Search → Where → OrderByDesc → Take → Get
	items, err := sr.Search(ctx, "golang", nil). // drittes Argument: Query-Callback, nil erlaubt
		Where("title", "indexing").
		OrderByDesc("created_at").
		Take(10).
		Get(ctx)
	if err != nil {
		panic(err)
	}
	for _, it := range items {
		post := it.Model.(Post) // ResultItem.Model ist bereits mit dem Originalmodell befüllt
		fmt.Println(post.ID, it.Score)
	}

	// Generisch holen: GetAs[T] ohne Typ-Assertion
	posts, _ := scout.GetAs[Post](ctx, sr.Search(ctx, "indexing", nil).Take(5))
	_ = posts
}
```

Wichtige Ausführungsmethoden: `Get(ctx) ([]ResultItem, error)`, `First(ctx)`, `Paginate(ctx, perPage, page, pageName)` (standardmäßig 15 Einträge pro Seite, Seitenparameter standardmäßig `page`), `Cursor(ctx)` (lazy Streaming), `Keys(ctx)` (nur Primärschlüssel). Das paginierte Ergebnis `PaginationResult` bietet außerdem `LastPage()` / `HasMorePages()` / `AppendQuery()` für Seitenlinks.

### 4. CLI-Demoprogramm

`cmd/scout` ist eine in sich geschlossene Demo-CLI: Beim Start setzt sie `SCOUT_DRIVER` auf `collection` und lädt über `NewMemorySource` 7 `post`-Datensätze vor (Entwürfe mit leerem Titel werden übersprungen, weil `ShouldBeSearchable` false ist).

```bash
go run ./cmd/scout                # zeigt die Verwendung aller Unterbefehle
go run ./cmd/scout index posts --key id        # Index erstellen
go run ./cmd/scout import --chunk 3 --fresh    # Vollimport (3 pro Block, vorher leeren)
go run ./cmd/scout flush          # Index leeren
go run ./cmd/scout queue-import --chunk 3 --min 1 --max 100 --workers 4  # Queue-Import nach Schlüsselbereich
go run ./cmd/scout sync-index-settings --driver meilisearch  # Index-Einstellungen synchronisieren (Engine muss UpdateIndexSettings unterstützen)
go run ./cmd/scout delete-index posts           # einzelnen Index löschen
go run ./cmd/scout delete-all-indexes           # alle Indizes löschen
```

## Lizenz & Hinweise

Dieses Projekt ist ein Go-Port des PHP-Plugins webman-scout; Klassenstruktur, Treibernamen und Verhaltenssemantik bleiben konsistent zum Original. Jede Vereinfachungs-Abwägung ist im Quellcode mit einem `ponytail:`-Kommentar markiert (prozessinterne Queue, linearer Scan der In-Memory-Datenquelle, Algolias `"0=1"`-No-op usw.).

## Spenden (Donate)

Danke für Ihre Unterstützung! Ihre Spende hilft, dieses Projekt weiter zu pflegen und auszubauen. Wir freuen uns über jeden Beitrag:

<p align="center">
<table>
<tr>
<td align="center">
<b>WeChat Pay</b><br/>
<img src="docs/weixinpay.png" width="130" height="130" alt="WeChat-Pay-QR-Code"/>
</td>
<td align="center">
<b>Alipay</b><br/>
<img src="docs/alipay.png" width="130" height="130" alt="Alipay-QR-Code"/>
</td>
</tr>
</table>
</p>

### Krypto-Spenden (Crypto Donate)

Folgende Mainnets werden unterstützt. Bitte gleichen Sie die Empfängeradresse vor der Überweisung mit dem jeweiligen Mainnet ab:

| Mainnet | Wallet-Adresse | QR-Code |
|---|---|---|
| BNB Smart Chain (BEP20) | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/1.jpg" width="100" height="100" alt="BNB Smart Chain (BEP20) QR-Code"/> |
| Tron (TRC20) | `TEdDHWLajt1XvqtPDWmQctdrJaC3pzZZzz` | <img src="docs/coin/2.jpg" width="100" height="100" alt="Tron (TRC20) QR-Code"/> |
| Ethereum (ERC20) | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/3.jpg" width="100" height="100" alt="Ethereum (ERC20) QR-Code"/> |
| Aptos | `0x836e3780edfc3f7b2372b39e2a1a3a5d7adfaccd96c726f21cfde1b50dd68030` | <img src="docs/coin/4.jpg" width="100" height="100" alt="Aptos QR-Code"/> |
| Plasma | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/5.jpg" width="100" height="100" alt="Plasma QR-Code"/> |
| Polygon POS | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/6.jpg" width="100" height="100" alt="Polygon POS QR-Code"/> |
| Solana | `2hfhboHdmdrYsY25XfQSsEWxq5ip4EQsR7f4AzSRMUyr` | <img src="docs/coin/7.jpg" width="100" height="100" alt="Solana QR-Code"/> |
| The Open Network (TON) | `UQB9kFQohzmXUir9QSSZq01iwl9aQZIDdBpNmDklljRtCoGK` | <img src="docs/coin/8.jpg" width="100" height="100" alt="The Open Network (TON) QR-Code"/> |
| Arbitrum One | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/9.jpg" width="100" height="100" alt="Arbitrum One QR-Code"/> |
| AVAX C-Chain | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/10.jpg" width="100" height="100" alt="AVAX C-Chain QR-Code"/> |

### Internationale Überweisung (Banküberweisung)

**Empfängerinformationen**

- Empfängername: WANG KEXUN
- Empfängerkontonummer: 881015918251

**Empfängerbank**

- ZA Bank SWIFT-Code: `AABLHKHHXXX`
- Bankname: ZA Bank Limited
- Bankleitzahl: 387
- Bankadresse: `Core F, Cyberport 3, 100 Cyberport Road, Hong Kong`

**Korrespondenzbank für grenzüberschreitende Überweisungen (falls erforderlich)**

> Dabei handelt es sich um die Korrespondenzbank (Zwischenbank) für grenzüberschreitende Überweisungen, nicht um die Empfängerbank. Fragen Sie Ihre überweisende Bank, ob Korrespondenzbank-Informationen benötigt werden.

- Bei Überweisungen in HKD, CNY und USD ist die Korrespondenzbank Citibank:
  - Bankname: Citibank N.A. Hong Kong
  - SWIFT-Code: `CITIHKHXXXX`
  - Bankleitzahl: 006
  - Filialname: Hong Kong Branch
  - Filialnummer: 391
  - Bankadresse: `Citibank Tower, Citibank Plaza, 3 Garden Road, Central, Hong Kong`
- Bei Überweisungen in anderen Währungen ist die Korrespondenzbank BNY Mellon:
  - Bankname: THE BANK OF NEW YORK MELLON
  - SWIFT-Code: `IRVTUS3NXXX`
  - Bankadresse: `THE BANK OF NEW YORK MELLON, 240 GREENWICH STREET, NEW YORK, United States`
