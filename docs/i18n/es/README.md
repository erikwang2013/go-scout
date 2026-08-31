# go-scout

> Librería de sincronización de búsquedas escrita en Go — un port de Laravel Scout a Go (basado en el plugin PHP [webman-scout](https://github.com/shopwwi/webman-scout)).
> Go 1.24 · implementación 100 % con la biblioteca estándar, cero dependencias de terceros · versión v1.2.0

## Presentación del proyecto

go-scout resuelve el problema de la «sincronización entre los modelos y el motor de búsqueda»: los modelos que implementan la interfaz `scout.ScoutModel` se escriben o se retiran automáticamente del motor de búsqueda al guardarse, borrarse o restaurarse, y se pueden consultar mediante una API de consultas encadenadas unificada que oculta las diferencias de API entre los motores de búsqueda (Elasticsearch, OpenSearch, Meilisearch, Typesense, Algolia, XunSearch…).

Refleja la estructura de clases y el comportamiento de la librería PHP original:

| PHP（webman/laravel-scout） | Go（go-scout） |
|---|---|
| `Scout` | `scout.Scout`（scout.go） |
| `Builder` | `scout.Builder`（builder.go / builder_search.go） |
| `EngineManager` | `scout.Manager` / `scout.EngineManager`（manager.go） |
| `ModelObserver` | `scout.ModelObserver`（observer.go） |
| `Searchable` trait | `scout.Searchable`（searchable.go） |
| `Engine` / `AdvancedEngine` 及各引擎类 | `scout.Engine` / `scout.AdvancedEngine` 及 `engines` 包 9 个引擎 |

Motores integrados (paquete `engines`) :

- **null** — implementación vacía, controlador de respaldo por defecto (se usa cuando la indexación está desactivada)
- **collection** — búsqueda en memoria, sin ningún servicio externo
- **database** — la tabla sirve de índice: búsqueda LIKE/ILIKE y de texto completo directamente sobre las tablas (mysql / pgsql / sqlite)
- **elasticsearch** / **opensearch** — consultas REST, comparten la representación intermedia DSL de `engines/dsl.go`
- **meilisearch** — consultas REST, admite vectorial e híbrido, filtros, ordenación y facetas
- **typesense** — consultas REST, agregaciones, agrupación, vecindad vectorial
- **algolia** — consultas REST, versiones v3 / v4
- **xunsearch** — protocolo de demonio HTTP (lado de indexación + lado de búsqueda)

Con los alias `advanced_*`, `engines.Register` registra en total 15 nombres de controladores (ver [engines/register.go](engines/register.go)).

## Diseño de la arquitectura

![Diagrama de arquitectura por capas](docs/i18n/es/arch.svg)

Cinco capas de abajo hacia arriba, con responsabilidades cada vez más estrechas:

1. **Capa de modelos** — los modelos de negocio implementan `scout.ScoutModel` (`ScoutKey`, `ToSearchableArray`, `ShouldBeSearchable`, `TableName`, etc., ver [model.go](model.go)) ; los datos se aportan a través de la interfaz `scout.Source[T]` (`All` / `ByIDs` / `Count`), con `scout.NewMemorySource` integrado. Las interfaces opcionales como `SoftDeleter`, `FullTextColumner`, `PrefixColumner` se implementan según necesidad.
2. **Capa de sincronización** — `scout.ModelObserver` captura los eventos de guardado/borrado/restauración de los modelos ; `scout.EventBus` publica los eventos `scout.models_imported`, `scout.models_flushed` ; `scout.Queue` ofrece una cola asíncrona en proceso (buffer de 4096), con respaldo síncrono si la cola no está disponible.
3. **Capa de consulta** — `scout.Builder` acumula el estado de la consulta por encadenamiento (`Where`, `OrderBy`, `Take`, `WithTrashed`…), los métodos de ejecución de `builder_search.go` (`Get` / `First` / `Paginate` / `Cursor` / `Keys`) disparan un viaje de ida y vuelta al motor, y el resultado se normaliza en `scout.Result` / `PaginationResult`.
4. **Capa de adaptación de motores** — la interfaz `Engine` (`Search`, `Paginate`, `Update`, `Delete`, `Map`, `Flush`, `CreateIndex`, `DeleteIndex`…) y la interfaz extendida `AdvancedEngine` (`AdvancedSearch`, `GetAggregations`, `GetFacets`) aíslan las diferencias entre motores tras la interfaz ; `scout.Manager` registra mediante `Extend` y crea perezosamente y cachea los controladores mediante `Driver(name)`.
5. **Capa de motores** — los 9 motores del paquete `engines` dependen únicamente de la configuración (`*scout.Config`) y de un cliente HTTP, sin conocerse entre sí.

## Características

![Diagrama de características](docs/i18n/es/features.svg)

- **Cadena de construcción de consultas** — API fluida de `scout.Builder`: `Query` / `Where` / `WhereIn` / `WhereNotIn` / `OrderBy` / `OrderByDesc` / `Latest` / `Oldest` / `Take` / `Skip` ; condiciones avanzadas `VectorSearch` / `WhereRange` / `WhereGeoDistance` / `FulltextSearch` / `OrderByVectorSimilarity` / `OrderByGeoDistance` ; inyección de callbacks `QueryCB` / `CallbackCB` / `AddResultProcessor`. Orden de resolución de los campos de búsqueda: `Options["fields"]` → `FullTextColumner` → `FieldNames`.
- **Representación intermedia DSL** — [engines/dsl.go](engines/dsl.go) compila las condiciones avanzadas en JSON de bool query (`multi_match`, `term`, `range`, `geo_distance`, `wildcard`, `regexp`, etc.), consumido directamente por Elasticsearch / OpenSearch ; `MapBooleanToBoolKey` mapea and/or/not hacia `filter` / `should` / `must_not`.
- **Paginación y cursor** — `Paginate` / `PaginateRaw` / `SimplePaginate` / `Cursor` (recorrido perezoso mediante canal con buffer) ; `PaginationResult` ofrece `LastPage` / `HasMorePages` / `AppendQuery` ; `Result.As[T]` y la función genérica de paquete `scout.GetAs[T]` recuperan los modelos sin aserción de tipos.
- **Borrado suave** — los documentos indexados reciben los metadatos `__soft_deleted` (0/1) ; filtrado de consultas con `OnlyTrashed` / `WithTrashed` ; el motor database usa la columna `deleted_at`.
- **Cola asíncrona** — cola en proceso (canal `chan` con buffer de 4096 + goroutines de trabajo), activada con `SCOUT_QUEUE=1` ; respaldo síncrono con aviso si la cola no está disponible ; `MakeAllSearchable` importa por lotes (`chunk`, 500 elementos por defecto).
- **Eventos y observadores** — `ModelObserver` escucha `Saved` / `Deleted` / `Restored` y sincroniza automáticamente ; `EventBus` publica `scout.models_imported`, `scout.models_flushed` ; `WithoutSyncingToSearch` desactiva temporalmente la sincronización.
- **Registro multi-motor** — `Manager.Extend` + `engines.Register` registran 15 nombres de controladores, creados perezosamente y cacheados ; un controlador desconocido devuelve `scout.ErrNotSupported` ; cambiar de motor solo requiere la configuración `SCOUT_DRIVER`.

## Filosofía de diseño

![Diagrama de filosofía de diseño](docs/i18n/es/design.svg)

- **El Builder encadenado porta la intención de la consulta** — `Search(ctx, query, cb)` devuelve un `*scout.Builder` ; el encadenamiento solo acumula estado, únicamente `Get` / `First` / `Paginate` / `Cursor` disparan un viaje al motor ; modificar la consulta, interceptar el cuerpo de la petición HTTP y post-procesar los resultados pasan todos por callbacks, sin que el motor tenga que preocuparse.
- **Representación intermedia DSL** — las condiciones avanzadas se compilan en JSON de bool query, consumido directamente por ES / OpenSearch ; Meilisearch / Typesense / Algolia / database traducen cada uno por su lado ; las diferencias entre motores quedan confinadas en la capa de traducción.
- **Aislamiento por la interfaz del motor** — `Engine` / `AdvancedEngine` solo hablan de documentos y resultados, sin conocer el tipo del modelo ; `MapIDs` / `Map` rellenan los modelos a partir de los IDs de los resultados, `attachMeta` alinea mediante `KeyString`, los conjuntos de modelos filtrados nunca se desalinean.
- **Simplificaciones ponytail deliberadas** — el código señala sus compromisos en comentarios `ponytail:`: cola en proceso en vez de Redis, barrido lineal de `MemorySource` en O(ids×models), `whereNotIns` de Algolia como no-op `"0=1"`, estructura única v3/v4 con flag de versión, orden aleatorio sustituido por `_score asc`, etc. 100 % biblioteca estándar, cero SDK de terceros.

## Ciclo de vida

![Diagrama del ciclo de vida de la búsqueda](docs/i18n/es/lifecycle.svg)

**Camino de búsqueda** : `Searchable(model, source).Search(ctx, query, cb)` construye un `*scout.Builder` → `Manager.Driver(name)` reparte hacia el motor (creación perezosa y caché) → `engine.Search` / `Paginate` (REST / SQL / memoria) → análisis en `scout.Result` (Hits · Total · Aggregations · Raw) → si es necesario rellenar los modelos (`Get` / `Paginate` / `First`): `MapIDs` → `Source.ByIDs` → `attachMeta` (alineación vía `KeyString`, preservación del orden devuelto por el motor) → retorno de `ResultItem` / `PaginationResult` ; `Keys` / `GetAggregations` / `PaginateRaw` devuelven directamente sin cargar los modelos.

**Camino de escritura** : `ModelObserver` captura los eventos del modelo → `Queue.PushNamed("scout_make" / "scout_remove" / "scout_make_range")` ejecución asíncrona (respaldo síncrono si no está activada) → `engine.Update` / `Delete` (metadatos `__soft_deleted` en caso de borrado suave) → `EventBus` publica `scout.models_imported` / `scout.models_flushed`.

## Estructura del proyecto

```
go-scout/
├── go.mod                  # Definición del módulo: github.com/erikwang2013/go-scout · go 1.24.1 · cero dependencias
├── .gitignore              # Ignora archivos de IDE / cachés / claves
├── scout.go                # Fachada Scout: ensamblado y fábricas de Config / Manager / Events / Queue / Observer
├── config.go               # Árbol de configuración: DefaultConfig + sobrescritura por variables de entorno + acceso dot-path
├── engine.go               # Interfaces Engine / AdvancedEngine + tipos Result / Hit / PaginationResult
├── manager.go              # EngineManager: registro Extend, creación perezosa y caché de Driver, motor null por defecto
├── builder.go              # Estructura Builder + construcción encadenada de condiciones (Where / OrderBy / Take / avanzadas)
├── builder_search.go       # Ejecución de consultas: Raw / Get / First / Paginate / Cursor / GetAs[T]
├── searchable.go           # Searchable: enlace modelo-fuente, import/export completo, metadatos de borrado suave
├── observer.go             # ModelObserver: sincronización automática Saved / Deleted / Restored
├── events.go               # EventBus: publicación-suscripción síncrona thread-safe + eventos de import / purge
├── queue.go                # Queue: cola asíncrona en proceso (chan 4096) + respaldo síncrono
├── model.go                # Interfaz ScoutModel + interfaces de extensión opcionales + ayudas KeyName/KeyString…
├── source.go               # Interfaz Source[T] + MemorySource (barrido lineal)
├── exceptions.go           # Sistema de errores ErrNotSupported / ErrScout
├── cmd/scout/main.go       # Programa de demostración CLI: 7 subcomandos import / flush / index / queue-import…
├── engines/
│   ├── engine.go           # Cliente HTTP (autenticación, salto de verificación TLS) + ayudas compartidas DoJSON/DoBytes
│   ├── register.go         # SetDatabase + Register registran 15 nombres de controladores + Drivers()
│   ├── dsl.go              # Representación intermedia DSL: bool query / ordenación / agregaciones / facetas / resaltado
│   ├── null.go             # NullEngine: implementación vacía
│   ├── collection.go       # CollectionEngine: búsqueda en memoria (coincidencia de subcadena insensible a mayúsculas)
│   ├── database.go         # DatabaseEngine: la tabla sirve de índice SQL (LIKE/ILIKE + tsvector de pgsql)
│   ├── elasticsearch.go    # ElasticsearchEngine + ayudas es* compartidas con OpenSearch
│   ├── opensearch.go       # OpenSearchEngine (el controlador base también es un motor avanzado)
│   ├── meilisearch.go      # MeilisearchEngine: filtros / ordenación / híbrido vectorial / facetas
│   ├── typesense.go        # TypesenseEngine: filter_by / agregaciones / agrupación / búsqueda de vecinos
│   ├── algolia.go          # AlgoliaEngine: estructura única v3/v4 + flag de versión
│   └── xunsearch.go        # XunSearchEngine: demonio de indexación + demonio de búsqueda
├── docs/
│   ├── arch.svg            # Diagrama de arquitectura por capas
│   ├── features.svg        # Diagrama de características
│   ├── design.svg          # Diagrama de filosofía de diseño
│   └── lifecycle.svg       # Diagrama del ciclo de vida de la búsqueda
└── engines/
    ├── collection_test.go  # Tests de comportamiento del motor en memoria (filtros / ordenación / paginación / relleno)
    ├── database_test.go    # Generación y aserciones de SQL (assertSQL con coincidencia exacta)
    ├── meilisearch_test.go # Tests de consulta/análisis de Meilisearch (stubs httptest)
    ├── typesense_test.go   # Tests de consulta/análisis de Typesense (creación automática de colección en 404)
    └── null_test.go        # Tests del comportamiento vacío de NullEngine
```

## Inicio rápido / Guía de uso

### 1. Instalación

```bash
go get github.com/erikwang2013/go-scout
```

Cero dependencias de terceros, listo para usar desde el import.

### 2. Configuración de los controladores (drivers)

`scout.DefaultConfig()` lee las variables de entorno ; elementos comunes :

| Variable de entorno | Valor por defecto | Descripción |
|---|---|---|
| `SCOUT_DRIVER` | `database` | Nombre del controlador del motor ; cadena vacía o `"false"` → respaldo en `null` |
| `SCOUT_PREFIX` | vacío | Prefijo de índice |
| `SCOUT_QUEUE` | desactivado | Poner a 1 para activar la cola asíncrona |
| `SCOUT_SOFT_DELETE` | desactivado | Metadatos de borrado suave escritos junto al documento en el índice |
| `SCOUT_CHUNK_SEARCHABLE` / `SCOUT_CHUNK_UNSEARCHABLE` | `500` | Tamaño de los lotes de import/retiro masivo |
| `SCOUT_BULK_SIZE` | `100` | Tamaño de escritura masiva (opensearch) |

Específicas de cada motor (la ruta `engine.key` del árbol de configuración tiene el mismo nombre que la variable de entorno, p. ej. `opensearch.host` ← `OPENSEARCH_HTTP_HOST`) :

| Motor | Variable de entorno (valor por defecto) |
|---|---|
| meilisearch | `MEILISEARCH_HOST` (`http://127.0.0.1:7700`), `MEILISEARCH_KEY` |
| typesense | `TYPESENSE_HOST` (`127.0.0.1`), `TYPESENSE_PORT` (`8108`), `TYPESENSE_PROTOCOL` (`http`), `TYPESENSE_API_KEY` (`xyz`), `TYPESENSE_IMPORT_ACTION` (`upsert`), `TYPESENSE_MAX_TOTAL_RESULTS` (`1000`) |
| elasticsearch | `ELASTICSEARCH_HOST` (`http://127.0.0.1:9200`, primera entrada de la lista hosts) + `elasticsearch.auth` (user/password) |
| opensearch | `OPENSEARCH_HTTP_HOST` (`https://127.0.0.1:6205`), `OPENSEARCH_USERNAME` / `OPENSEARCH_PASSWORD` (`admin`/`admin`), `OPENSEARCH_SSL_VERIFICATION` (verificación TLS omitida por defecto), `OPENSEARCH_TIMEOUT` (`30` segundos) |
| xunsearch | `XUNSEARCH_INDEX_HOST` (`127.0.0.1`) + `XUNSEARCH_INDEX_PORT` (`8383`), `XUNSEARCH_SEARCH_HOST` + `XUNSEARCH_SEARCH_PORT` (`8384`), `XUNSEARCH_DEFAULT_INDEX` (`default`), `XUNSEARCH_CHARSET` (`utf-8`) |
| algolia | `ALGOLIA_APP_ID`, `ALGOLIA_SECRET` (panic al construir el motor si faltan) |

El controlador `database` también requiere inyectar la conexión y el dialecto de la base de datos :

```go
engines.SetDatabase(db, "mysql") // dialect: mysql / pgsql / postgres / sqlite
```

### 3. Ejemplo mínimo de uso

```go
package main

import (
	"context"
	"fmt"

	"github.com/erikwang2013/go-scout"
	"github.com/erikwang2013/go-scout/engines"
)

// Post 实现 scout.ScoutModel 即可参与搜索
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

	// 初始化：默认配置 + 注册全部引擎（SCOUT_DRIVER 决定用哪个）
	s := scout.NewWithConfig(scout.DefaultConfig())
	engines.Register(s.Manager)

	// 绑定模型与数据源，全量导入（chunk 传 0 表示用默认块大小 500）
	sr := s.Searchable(&Post{}, scout.NewMemorySource(
		Post{ID: 1, Title: "Go module layout", Body: "packages and imports"},
		Post{ID: 2, Title: "Indexing strategies", Body: "bulk writes"},
	))
	_ = sr.MakeAllSearchable(ctx, 0)

	// 链式查询：Search → Where → OrderByDesc → Take → Get
	items, err := sr.Search(ctx, "golang", nil). // 第三参为查询回调，可传 nil
		Where("title", "indexing").
		OrderByDesc("created_at").
		Take(10).
		Get(ctx)
	if err != nil {
		panic(err)
	}
	for _, it := range items {
		post := it.Model.(Post) // ResultItem.Model 已回填为原始模型
		fmt.Println(post.ID, it.Score)
	}

	// 泛型取数：GetAs[T] 免类型断言
	posts, _ := scout.GetAs[Post](ctx, sr.Search(ctx, "indexing", nil).Take(5))
	_ = posts
}
```

Métodos clave de ejecución de consultas: `Get(ctx) ([]ResultItem, error)`, `First(ctx)`, `Paginate(ctx, perPage, page, pageName)` (15 elementos por página por defecto, nombre del parámetro de página `page` por defecto), `Cursor(ctx)` (flujo perezoso), `Keys(ctx)` (solo claves primarias). El resultado paginado `PaginationResult` también ofrece `LastPage()` / `HasMorePages()` / `AppendQuery()` para generar los enlaces de paginación.

### 4. Programa de demostración CLI

`cmd/scout` es un CLI de demostración autocontenido: al arrancar, fija `SCOUT_DRIVER` en `collection` y precarga 7 documentos `post` mediante `NewMemorySource` (los borradores sin título se omiten porque `ShouldBeSearchable` devuelve false).

```bash
go run ./cmd/scout                # 打印全部子命令用法
go run ./cmd/scout index posts --key id        # 创建索引
go run ./cmd/scout import --chunk 3 --fresh    # 全量导入（每块 3 条，先清空）
go run ./cmd/scout flush          # 清空索引
go run ./cmd/scout queue-import --chunk 3 --min 1 --max 100 --workers 4  # 按键范围入队导入
go run ./cmd/scout sync-index-settings --driver meilisearch  # 同步索引设置（需引擎实现 UpdateIndexSettings）
go run ./cmd/scout delete-index posts           # 删除单个索引
go run ./cmd/scout delete-all-indexes           # 删除全部索引
```

## Licencia y notas

Este proyecto es un port a Go del plugin PHP webman-scout ; la estructura de clases, los nombres de los controladores y la semántica de comportamiento se mantienen coherentes con el original ; las simplificaciones se señalan en el código fuente con comentarios `ponytail:` (cola en proceso, barrido lineal de la fuente en memoria, no-op `"0=1"` de Algolia, etc.).

## Donaciones (Donate)

¡Gracias por tu apoyo! Tu donación ayudará a mantener y hacer evolucionar el proyecto de forma continuada. Cualquier apoyo es bienvenido:

<p align="center">
<table>
<tr>
<td align="center">
<b>WeChat Pay</b><br/>
<img src="docs/weixinpay.png" width="130" height="130" alt="Código QR de WeChat Pay"/>
</td>
<td align="center">
<b>Alipay</b><br/>
<img src="docs/alipay.png" width="130" height="130" alt="Código QR de Alipay"/>
</td>
</tr>
</table>
</p>

### Donaciones en criptomonedas (Crypto Donate)

Se admiten las siguientes redes ; verifica bien la dirección de recepción y la red correspondiente antes de transferir:

| Red | Dirección de la billetera | Código QR |
|---|---|---|
| BNB Smart Chain (BEP20) | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/1.jpg" width="100" height="100" alt="Código QR de BNB Smart Chain (BEP20)"/> |
| Tron (TRC20) | `TEdDHWLajt1XvqtPDWmQctdrJaC3pzZZzz` | <img src="docs/coin/2.jpg" width="100" height="100" alt="Código QR de Tron (TRC20)"/> |
| Ethereum (ERC20) | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/3.jpg" width="100" height="100" alt="Código QR de Ethereum (ERC20)"/> |
| Aptos | `0x836e3780edfc3f7b2372b39e2a1a3a5d7adfaccd96c726f21cfde1b50dd68030` | <img src="docs/coin/4.jpg" width="100" height="100" alt="Código QR de Aptos"/> |
| Plasma | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/5.jpg" width="100" height="100" alt="Código QR de Plasma"/> |
| Polygon POS | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/6.jpg" width="100" height="100" alt="Código QR de Polygon POS"/> |
| Solana | `2hfhboHdmdrYsY25XfQSsEWxq5ip4EQsR7f4AzSRMUyr` | <img src="docs/coin/7.jpg" width="100" height="100" alt="Código QR de Solana"/> |
| The Open Network (TON) | `UQB9kFQohzmXUir9QSSZq01iwl9aQZIDdBpNmDklljRtCoGK` | <img src="docs/coin/8.jpg" width="100" height="100" alt="Código QR de The Open Network (TON)"/> |
| Arbitrum One | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/9.jpg" width="100" height="100" alt="Código QR de Arbitrum One"/> |
| AVAX C-Chain | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/10.jpg" width="100" height="100" alt="Código QR de AVAX C-Chain"/> |

### Transferencias internacionales (transferencia bancaria)

**Información del beneficiario**

- Nombre del beneficiario: WANG KEXUN
- Número de cuenta del beneficiario: 881015918251

**Banco del beneficiario**

- SWIFT Code de ZA Bank: `AABLHKHHXXX`
- Nombre del banco: ZA Bank Limited
- Número de banco: 387
- Dirección del banco: `Core F, Cyberport 3, 100 Cyberport Road, Hong Kong`

**Banco corresponsal para transferencias transfronterizas (si es necesario)**

> Esta es la información del banco corresponsal (banco intermediario) para transferencias transfronterizas, no la del banco del beneficiario. Consulta a tu banco si es necesario proporcionar la información del banco corresponsal.

- Para depósitos en dólares de Hong Kong, RMB y dólares estadounidenses, el banco corresponsal es Citibank:
  - Nombre del banco: Citibank N.A. Hong Kong
  - SWIFT Code: `CITIHKHXXXX`
  - Número de banco: 006
  - Nombre de la sucursal: Hong Kong Branch
  - Número de sucursal: 391
  - Dirección del banco: `Citibank Tower, Citibank Plaza, 3 Garden Road, Central, Hong Kong`
- Para otras monedas, el banco corresponsal es BNY Mellon:
  - Nombre del banco: THE BANK OF NEW YORK MELLON
  - SWIFT Code: `IRVTUS3NXXX`
  - Dirección del banco: `THE BANK OF NEW YORK MELLON, 240 GREENWICH STREET, NEW YORK, United States`
