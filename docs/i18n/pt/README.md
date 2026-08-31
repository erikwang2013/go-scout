# go-scout

> Biblioteca de sincronização de busca escrita em Go — um port de Laravel Scout para Go (baseado no plugin PHP [webman-scout](https://github.com/shopwwi/webman-scout)).
> Go 1.24 · implementação 100 % com a biblioteca padrão, zero dependências de terceiros · versão v1.2.0

## Apresentação do projeto

O go-scout resolve o problema da «sincronização entre os modelos e o mecanismo de busca»: os modelos que implementam a interface `scout.ScoutModel` são gravados ou removidos automaticamente do mecanismo de busca ao serem salvos, excluídos ou restaurados, e podem ser consultados por meio de uma API de consultas encadeadas unificada que oculta as diferenças de API entre os mecanismos de busca (Elasticsearch, OpenSearch, Meilisearch, Typesense, Algolia, XunSearch…).

Espelha a estrutura de classes e o comportamento da biblioteca PHP original:

| PHP（webman/laravel-scout） | Go（go-scout） |
|---|---|
| `Scout` | `scout.Scout`（scout.go） |
| `Builder` | `scout.Builder`（builder.go / builder_search.go） |
| `EngineManager` | `scout.Manager` / `scout.EngineManager`（manager.go） |
| `ModelObserver` | `scout.ModelObserver`（observer.go） |
| `Searchable` trait | `scout.Searchable`（searchable.go） |
| `Engine` / `AdvancedEngine` 及各引擎类 | `scout.Engine` / `scout.AdvancedEngine` 及 `engines` 包 9 个引擎 |

Mecanismos integrados (pacote `engines`):

- **null** — implementação vazia, driver de fallback padrão (usado quando a indexação está desativada)
- **collection** — busca em memória, sem nenhum serviço externo
- **database** — a tabela serve de índice: busca LIKE/ILIKE e de texto completo diretamente sobre as tabelas (mysql / pgsql / sqlite)
- **elasticsearch** / **opensearch** — consultas REST, compartilham a representação intermediária DSL de `engines/dsl.go`
- **meilisearch** — consultas REST, suporta vetorial e híbrido, filtros, ordenação e facetas
- **typesense** — consultas REST, agregações, agrupamento, vizinhança vetorial
- **algolia** — consultas REST, versões v3 / v4
- **xunsearch** — protocolo de daemon HTTP (lado de indexação + lado de busca)

Com os aliases `advanced_*`, `engines.Register` registra ao todo 15 nomes de drivers (ver [engines/register.go](engines/register.go)).

## Design da arquitetura

![Diagrama de arquitetura em camadas](docs/i18n/pt/arch.svg)

Cinco camadas de baixo para cima, com responsabilidades cada vez mais estreitas:

1. **Camada de modelos** — os modelos de negócio implementam `scout.ScoutModel` (`ScoutKey`, `ToSearchableArray`, `ShouldBeSearchable`, `TableName`, etc., ver [model.go](model.go)); os dados são fornecidos por meio da interface `scout.Source[T]` (`All` / `ByIDs` / `Count`), com `scout.NewMemorySource` integrado. As interfaces opcionais como `SoftDeleter`, `FullTextColumner`, `PrefixColumner` são implementadas conforme a necessidade.
2. **Camada de sincronização** — `scout.ModelObserver` captura os eventos de salvar/excluir/restaurar dos modelos; `scout.EventBus` publica os eventos `scout.models_imported`, `scout.models_flushed`; `scout.Queue` oferece uma fila assíncrona em processo (buffer de 4096), com fallback síncrono se a fila não estiver disponível.
3. **Camada de consulta** — `scout.Builder` acumula o estado da consulta por encadeamento (`Where`, `OrderBy`, `Take`, `WithTrashed`…), os métodos de execução de `builder_search.go` (`Get` / `First` / `Paginate` / `Cursor` / `Keys`) disparam uma ida e volta ao mecanismo, e o resultado é normalizado em `scout.Result` / `PaginationResult`.
4. **Camada de adaptação de mecanismos** — a interface `Engine` (`Search`, `Paginate`, `Update`, `Delete`, `Map`, `Flush`, `CreateIndex`, `DeleteIndex`…) e a interface estendida `AdvancedEngine` (`AdvancedSearch`, `GetAggregations`, `GetFacets`) isolam as diferenças entre mecanismos atrás da interface; `scout.Manager` registra via `Extend` e cria preguiçosamente e armazena em cache os drivers via `Driver(name)`.
5. **Camada de mecanismos** — os 9 mecanismos do pacote `engines` dependem apenas da configuração (`*scout.Config`) e de um cliente HTTP, sem conhecerem uns aos outros.

## Funcionalidades

![Diagrama de funcionalidades](docs/i18n/pt/features.svg)

- **Encadeamento de construção de consultas** — API fluida do `scout.Builder`: `Query` / `Where` / `WhereIn` / `WhereNotIn` / `OrderBy` / `OrderByDesc` / `Latest` / `Oldest` / `Take` / `Skip`; condições avançadas `VectorSearch` / `WhereRange` / `WhereGeoDistance` / `FulltextSearch` / `OrderByVectorSimilarity` / `OrderByGeoDistance`; injeção de callbacks `QueryCB` / `CallbackCB` / `AddResultProcessor`. Ordem de resolução dos campos de busca: `Options["fields"]` → `FullTextColumner` → `FieldNames`.
- **Representação intermediária DSL** — [engines/dsl.go](engines/dsl.go) compila as condições avançadas em JSON de bool query (`multi_match`, `term`, `range`, `geo_distance`, `wildcard`, `regexp`, etc.), consumido diretamente por Elasticsearch / OpenSearch; `MapBooleanToBoolKey` mapeia and/or/not para `filter` / `should` / `must_not`.
- **Paginação e cursor** — `Paginate` / `PaginateRaw` / `SimplePaginate` / `Cursor` (iteração preguiçosa via canal com buffer); `PaginationResult` oferece `LastPage` / `HasMorePages` / `AppendQuery`; `Result.As[T]` e a função genérica de pacote `scout.GetAs[T]` recuperam os modelos sem asserção de tipos.
- **Soft delete** — os documentos indexados recebem os metadados `__soft_deleted` (0/1); filtragem de consultas com `OnlyTrashed` / `WithTrashed`; o mecanismo database usa a coluna `deleted_at`.
- **Fila assíncrona** — fila em processo (canal `chan` com buffer de 4096 + goroutines de trabalho), ativada com `SCOUT_QUEUE=1`; fallback síncrono com aviso se a fila não estiver disponível; `MakeAllSearchable` importa em lotes (`chunk`, 500 itens por padrão).
- **Eventos e observadores** — `ModelObserver` escuta `Saved` / `Deleted` / `Restored` e sincroniza automaticamente; `EventBus` publica `scout.models_imported`, `scout.models_flushed`; `WithoutSyncingToSearch` desativa temporariamente a sincronização.
- **Registro multi-mecanismo** — `Manager.Extend` + `engines.Register` registram 15 nomes de drivers, criados preguiçosamente e armazenados em cache; um driver desconhecido retorna `scout.ErrNotSupported`; trocar de mecanismo exige apenas a configuração `SCOUT_DRIVER`.

## Filosofia de design

![Diagrama da filosofia de design](docs/i18n/pt/design.svg)

- **O Builder encadeado carrega a intenção da consulta** — `Search(ctx, query, cb)` retorna um `*scout.Builder`; o encadeamento apenas acumula estado, somente `Get` / `First` / `Paginate` / `Cursor` disparam uma ida ao mecanismo; modificar a consulta, interceptar o corpo da requisição HTTP e pós-processar os resultados passam todos por callbacks, sem que o mecanismo precise se preocupar.
- **Representação intermediária DSL** — as condições avançadas são compiladas em JSON de bool query, consumido diretamente por ES / OpenSearch; Meilisearch / Typesense / Algolia / database traduzem cada um de seu lado; as diferenças entre mecanismos ficam confinadas na camada de tradução.
- **Isolamento pela interface do mecanismo** — `Engine` / `AdvancedEngine` só falam de documentos e resultados, sem conhecer o tipo do modelo; `MapIDs` / `Map` preenchem os modelos a partir dos IDs dos resultados, `attachMeta` alinha via `KeyString`, os conjuntos de modelos filtrados nunca se desalinham.
- **Simplificações ponytail deliberadas** — o código sinaliza seus compromissos em comentários `ponytail:`: fila em processo em vez de Redis, varredura linear do `MemorySource` em O(ids×models), `whereNotIns` do Algolia como no-op `"0=1"`, estrutura única v3/v4 com flag de versão, ordem aleatória substituída por `_score asc`, etc. 100 % biblioteca padrão, zero SDKs de terceiros.

## Ciclo de vida

![Diagrama do ciclo de vida da busca](docs/i18n/pt/lifecycle.svg)

**Caminho de busca**: `Searchable(model, source).Search(ctx, query, cb)` constrói um `*scout.Builder` → `Manager.Driver(name)` despacha para o mecanismo (criação preguiçosa e cache) → `engine.Search` / `Paginate` (REST / SQL / memória) → análise em `scout.Result` (Hits · Total · Aggregations · Raw) → se for necessário preencher os modelos (`Get` / `Paginate` / `First`): `MapIDs` → `Source.ByIDs` → `attachMeta` (alinhamento via `KeyString`, preservação da ordem retornada pelo mecanismo) → retorno de `ResultItem` / `PaginationResult`; `Keys` / `GetAggregations` / `PaginateRaw` retornam diretamente sem carregar os modelos.

**Caminho de escrita**: `ModelObserver` captura os eventos do modelo → `Queue.PushNamed("scout_make" / "scout_remove" / "scout_make_range")` execução assíncrona (fallback síncrono se não estiver ativada) → `engine.Update` / `Delete` (metadados `__soft_deleted` em caso de soft delete) → `EventBus` publica `scout.models_imported` / `scout.models_flushed`.

## Estrutura do projeto

```
go-scout/
├── go.mod                  # Definição do módulo: github.com/erikwang2013/go-scout · go 1.24.1 · zero dependências
├── .gitignore              # Ignora arquivos de IDE / caches / chaves
├── scout.go                # Fachada Scout: montagem e factories de Config / Manager / Events / Queue / Observer
├── config.go               # Árvore de configuração: DefaultConfig + sobrescrita por env vars + acesso dot-path
├── engine.go               # Interfaces Engine / AdvancedEngine + tipos Result / Hit / PaginationResult
├── manager.go              # EngineManager: registro Extend, criação preguiçosa e cache de Driver, motor null padrão
├── builder.go              # Estrutura Builder + encadeamento de condições (Where / OrderBy / Take / avançadas)
├── builder_search.go       # Execução de consultas: Raw / Get / First / Paginate / Cursor / GetAs[T]
├── searchable.go           # Searchable: vínculo model-fonte, import/export completo, metadados de soft delete
├── observer.go             # ModelObserver: sincronização automática Saved / Deleted / Restored
├── events.go               # EventBus: pub-sub síncrono thread-safe + eventos de import / purge
├── queue.go                # Queue: fila assíncrona em processo (chan 4096) + fallback síncrono
├── model.go                # Interface ScoutModel + interfaces de extensão opcionais + helpers KeyName/KeyString…
├── source.go               # Interface Source[T] + MemorySource (varredura linear)
├── exceptions.go           # Sistema de erros ErrNotSupported / ErrScout
├── cmd/scout/main.go       # Programa de demonstração CLI: 7 subcomandos import / flush / index / queue-import…
├── engines/
│   ├── engine.go           # Cliente HTTP (autenticação, skip da verificação TLS) + helpers compartilhados DoJSON/DoBytes
│   ├── register.go         # SetDatabase + Register registram 15 nomes de drivers + Drivers()
│   ├── dsl.go              # Representação intermediária DSL: bool query / ordenação / agregações / facetas / highlight
│   ├── null.go             # NullEngine: implementação vazia
│   ├── collection.go       # CollectionEngine: busca em memória (correspondência de substring case-insensitive)
│   ├── database.go         # DatabaseEngine: a tabela serve de índice SQL (LIKE/ILIKE + tsvector do pgsql)
│   ├── elasticsearch.go    # ElasticsearchEngine + helpers es* compartilhados com OpenSearch
│   ├── opensearch.go       # OpenSearchEngine (o driver base também é um mecanismo avançado)
│   ├── meilisearch.go      # MeilisearchEngine: filtros / ordenação / híbrido vetorial / facetas
│   ├── typesense.go        # TypesenseEngine: filter_by / agregações / agrupamento / busca de vizinhos
│   ├── algolia.go          # AlgoliaEngine: estrutura única v3/v4 + flag de versão
│   └── xunsearch.go        # XunSearchEngine: daemon de indexação + daemon de busca
├── docs/
│   ├── arch.svg            # Diagrama de arquitetura em camadas
│   ├── features.svg        # Diagrama de funcionalidades
│   ├── design.svg          # Diagrama da filosofia de design
│   └── lifecycle.svg       # Diagrama do ciclo de vida da busca
└── engines/
    ├── collection_test.go  # Testes de comportamento do mecanismo em memória (filtros / ordenação / paginação / preenchimento)
    ├── database_test.go    # Geração e asserções de SQL (assertSQL com correspondência exata)
    ├── meilisearch_test.go # Testes de consulta/análise do Meilisearch (stubs httptest)
    ├── typesense_test.go   # Testes de consulta/análise do Typesense (criação automática de coleção no 404)
    └── null_test.go        # Testes do comportamento vazio do NullEngine
```

## Início rápido / Guia de uso

### 1. Instalação

```bash
go get github.com/erikwang2013/go-scout
```

Zero dependências de terceiros, pronto para usar a partir do import.

### 2. Configuração dos drivers

`scout.DefaultConfig()` lê as variáveis de ambiente; itens comuns:

| Variável de ambiente | Padrão | Descrição |
|---|---|---|
| `SCOUT_DRIVER` | `database` | Nome do driver do mecanismo; string vazia ou `"false"` → fallback em `null` |
| `SCOUT_PREFIX` | vazio | Prefixo de índice |
| `SCOUT_QUEUE` | desativada | Definir como 1 para ativar a fila assíncrona |
| `SCOUT_SOFT_DELETE` | desativado | Metadados de soft delete gravados junto ao documento no índice |
| `SCOUT_CHUNK_SEARCHABLE` / `SCOUT_CHUNK_UNSEARCHABLE` | `500` | Tamanho dos lotes de import/remoção em massa |
| `SCOUT_BULK_SIZE` | `100` | Tamanho de escrita em massa (opensearch) |

Específicas de cada mecanismo (a chave `engine.key` da árvore de configuração tem o mesmo nome da variável de ambiente, ex.: `opensearch.host` ← `OPENSEARCH_HTTP_HOST`):

| Mecanismo | Variável de ambiente (padrão) |
|---|---|
| meilisearch | `MEILISEARCH_HOST` (`http://127.0.0.1:7700`), `MEILISEARCH_KEY` |
| typesense | `TYPESENSE_HOST` (`127.0.0.1`), `TYPESENSE_PORT` (`8108`), `TYPESENSE_PROTOCOL` (`http`), `TYPESENSE_API_KEY` (`xyz`), `TYPESENSE_IMPORT_ACTION` (`upsert`), `TYPESENSE_MAX_TOTAL_RESULTS` (`1000`) |
| elasticsearch | `ELASTICSEARCH_HOST` (`http://127.0.0.1:9200`, primeira entrada da lista de hosts) + `elasticsearch.auth` (user/password) |
| opensearch | `OPENSEARCH_HTTP_HOST` (`https://127.0.0.1:6205`), `OPENSEARCH_USERNAME` / `OPENSEARCH_PASSWORD` (`admin`/`admin`), `OPENSEARCH_SSL_VERIFICATION` (verificação TLS omitida por padrão), `OPENSEARCH_TIMEOUT` (`30` segundos) |
| xunsearch | `XUNSEARCH_INDEX_HOST` (`127.0.0.1`) + `XUNSEARCH_INDEX_PORT` (`8383`), `XUNSEARCH_SEARCH_HOST` + `XUNSEARCH_SEARCH_PORT` (`8384`), `XUNSEARCH_DEFAULT_INDEX` (`default`), `XUNSEARCH_CHARSET` (`utf-8`) |
| algolia | `ALGOLIA_APP_ID`, `ALGOLIA_SECRET` (panic ao construir o mecanismo se faltarem) |

O driver `database` também exige injetar a conexão e o dialeto do banco de dados:

```go
engines.SetDatabase(db, "mysql") // dialect: mysql / pgsql / postgres / sqlite
```

### 3. Exemplo mínimo de uso

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

Métodos-chave de execução de consultas: `Get(ctx) ([]ResultItem, error)`, `First(ctx)`, `Paginate(ctx, perPage, page, pageName)` (15 itens por página por padrão, nome do parâmetro de página `page` por padrão), `Cursor(ctx)` (fluxo preguiçoso), `Keys(ctx)` (apenas chaves primárias). O resultado paginado `PaginationResult` também oferece `LastPage()` / `HasMorePages()` / `AppendQuery()` para gerar os links de paginação.

### 4. Programa de demonstração CLI

`cmd/scout` é um CLI de demonstração autocontido: ao iniciar, fixa `SCOUT_DRIVER` em `collection` e pré-carrega 7 documentos `post` via `NewMemorySource` (os rascunhos sem título são omitidos porque `ShouldBeSearchable` retorna false).

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

## Licença e notas

Este projeto é um port para Go do plugin PHP webman-scout; a estrutura de classes, os nomes dos drivers e a semântica de comportamento são mantidos coerentes com o original; as simplificações são sinalizadas no código-fonte com comentários `ponytail:` (fila em processo, varredura linear da fonte em memória, no-op `"0=1"` do Algolia, etc.).

## Doações (Donate)

Obrigado pelo seu apoio! Sua doação ajudará a manter e fazer evoluir o projeto de forma contínua. Qualquer apoio é bem-vindo:

<p align="center">
<table>
<tr>
<td align="center">
<b>WeChat Pay</b><br/>
<img src="docs/weixinpay.png" width="130" height="130" alt="Código QR do WeChat Pay"/>
</td>
<td align="center">
<b>Alipay</b><br/>
<img src="docs/alipay.png" width="130" height="130" alt="Código QR do Alipay"/>
</td>
</tr>
</table>
</p>

### Doações em criptomoedas (Crypto Donate)

As seguintes redes são suportadas; verifique bem o endereço de recebimento e a rede correspondente antes de transferir:

| Rede | Endereço da carteira | Código QR |
|---|---|---|
| BNB Smart Chain (BEP20) | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/1.jpg" width="100" height="100" alt="Código QR do BNB Smart Chain (BEP20)"/> |
| Tron (TRC20) | `TEdDHWLajt1XvqtPDWmQctdrJaC3pzZZzz` | <img src="docs/coin/2.jpg" width="100" height="100" alt="Código QR do Tron (TRC20)"/> |
| Ethereum (ERC20) | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/3.jpg" width="100" height="100" alt="Código QR do Ethereum (ERC20)"/> |
| Aptos | `0x836e3780edfc3f7b2372b39e2a1a3a5d7adfaccd96c726f21cfde1b50dd68030` | <img src="docs/coin/4.jpg" width="100" height="100" alt="Código QR do Aptos"/> |
| Plasma | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/5.jpg" width="100" height="100" alt="Código QR do Plasma"/> |
| Polygon POS | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/6.jpg" width="100" height="100" alt="Código QR do Polygon POS"/> |
| Solana | `2hfhboHdmdrYsY25XfQSsEWxq5ip4EQsR7f4AzSRMUyr` | <img src="docs/coin/7.jpg" width="100" height="100" alt="Código QR do Solana"/> |
| The Open Network (TON) | `UQB9kFQohzmXUir9QSSZq01iwl9aQZIDdBpNmDklljRtCoGK` | <img src="docs/coin/8.jpg" width="100" height="100" alt="Código QR do The Open Network (TON)"/> |
| Arbitrum One | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/9.jpg" width="100" height="100" alt="Código QR do Arbitrum One"/> |
| AVAX C-Chain | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/10.jpg" width="100" height="100" alt="Código QR do AVAX C-Chain"/> |

### Transferência internacional (transferência bancária)

**Informações do beneficiário**

- Nome do beneficiário: WANG KEXUN
- Número da conta do beneficiário: 881015918251

**Banco do beneficiário**

- Código SWIFT do ZA Bank: `AABLHKHHXXX`
- Nome do banco: ZA Bank Limited
- Número do banco: 387
- Endereço do banco: `Core F, Cyberport 3, 100 Cyberport Road, Hong Kong`

**Banco correspondente para transferências transfronteiriças (se necessário)**

> Estas são as informações do banco correspondente (banco intermediário) para transferências transfronteiriças, não as do banco do beneficiário. Consulte o seu banco para saber se é necessário fornecer as informações do banco correspondente.

- Para depósitos em dólares de Hong Kong, RMB e dólares americanos, o banco correspondente é o Citibank:
  - Nome do banco: Citibank N.A. Hong Kong
  - Código SWIFT: `CITIHKHXXXX`
  - Número do banco: 006
  - Nome da agência: Hong Kong Branch
  - Número da agência: 391
  - Endereço do banco: `Citibank Tower, Citibank Plaza, 3 Garden Road, Central, Hong Kong`
- Para outras moedas, o banco correspondente é o BNY Mellon:
  - Nome do banco: THE BANK OF NEW YORK MELLON
  - Código SWIFT: `IRVTUS3NXXX`
  - Endereço do banco: `THE BANK OF NEW YORK MELLON, 240 GREENWICH STREET, NEW YORK, United States`
