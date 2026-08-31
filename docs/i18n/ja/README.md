# go-scout

> Go で書かれた検索同期ライブラリ — Laravel Scout の Go 移植版（PHP プラグイン [webman-scout](https://github.com/shopwwi/webman-scout) をベース）。
> Go 1.24 · 純粋な標準ライブラリのみ、サードパーティ依存ゼロ · バージョン v1.2.0

## プロジェクト概要

go-scout は「モデルと検索エンジンの同期」という問題を解決します。`scout.ScoutModel` インターフェースを実装したモデルは、保存・削除・復元時に自動的に検索エンジンへ書き込まれ・削除され、統一されたチェーン可能なクエリ API で検索できます。各検索エンジン（Elasticsearch、OpenSearch、Meilisearch、Typesense、Algolia、XunSearch…）の API 差異はすべて内部に隠蔽されます。

元の PHP ライブラリのクラス構造と動作をそのまま踏襲しています：

| PHP (webman/laravel-scout) | Go (go-scout) |
|---|---|
| `Scout` | `scout.Scout` (scout.go) |
| `Builder` | `scout.Builder` (builder.go / builder_search.go) |
| `EngineManager` | `scout.Manager` / `scout.EngineManager` (manager.go) |
| `ModelObserver` | `scout.ModelObserver` (observer.go) |
| `Searchable` trait | `scout.Searchable` (searchable.go) |
| `Engine` / `AdvancedEngine` と各エンジンクラス | `scout.Engine` / `scout.AdvancedEngine` と engines パッケージの 9 エンジン |

組み込みエンジン（engines パッケージ）：

- **null** — 空実装、デフォルトのフォールバックドライバ（インデックス無効時に使用）
- **collection** — インメモリ検索、外部サービス不要
- **database** — テーブルをインデックスとして利用：データベーステーブルへの LIKE/ILIKE および全文検索（mysql / pgsql / sqlite）
- **elasticsearch** / **opensearch** — REST クエリ、`engines/dsl.go` の DSL 中間表現を共有
- **meilisearch** — REST クエリ、ベクトル・ハイブリッド検索、フィルタ、ソート、ファセット
- **typesense** — REST クエリ、アグリゲーション、グルーピング、ベクトル最近傍検索
- **algolia** — REST クエリ、v3 / v4 対応
- **xunsearch** — HTTP デーモンプロトコル（索引サーバー + 検索サーバー）

`advanced_*` エイリアスを含め、`engines.Register` は合計 15 個のドライバ名を登録します（[engines/register.go](engines/register.go) 参照）。

## アーキテクチャ

![アーキテクチャ層の図](docs/i18n/ja/arch.svg)

下から上へ 5 つの層に分かれ、層ごとに責務を分担しています：

1. **モデル層** — ビジネスモデルが `scout.ScoutModel`（`ScoutKey`、`ToSearchableArray`、`ShouldBeSearchable`、`TableName` など。[model.go](model.go) 参照）を実装します。データは `scout.Source[T]` インターフェース（`All` / `ByIDs` / `Count`）が供給し、組み込み実装として `scout.NewMemorySource` を提供。`SoftDeleter`、`FullTextColumner`、`PrefixColumner` などのオプションの拡張インターフェースは必要に応じて実装します。
2. **同期層** — `scout.ModelObserver` がモデルの保存・削除・復元イベントを捕捉し、`scout.EventBus` が `scout.models_imported` / `scout.models_flushed` イベントを発行、`scout.Queue` がプロセス内非同期キュー（バッファ 4096）を提供します。キューが無い場合は同期フォールバックします。
3. **クエリ層** — `scout.Builder` がチェーン API（`Where`、`OrderBy`、`Take`、`WithTrashed`…）でクエリ状態を蓄積します。`builder_search.go` の実行メソッド（`Get` / `First` / `Paginate` / `Cursor` / `Keys`）がエンジンへの往復をちょうど 1 回だけ行い、結果を `scout.Result` / `PaginationResult` に正規化します。
4. **エンジンアダプタ層** — `Engine` インターフェース（`Search`、`Paginate`、`Update`、`Delete`、`Map`、`Flush`、`CreateIndex`、`DeleteIndex`…）と拡張インターフェース `AdvancedEngine`（`AdvancedSearch`、`GetAggregations`、`GetFacets`）がエンジン差異をインターフェースの背後に隔離。`scout.Manager` は `Extend` でドライバを登録し、`Driver(name)` で遅延生成・キャッシュします。
5. **エンジン層** — engines パッケージの 9 つのエンジン実装。それぞれが設定（`*scout.Config`）と HTTP クライアントにのみ依存し、互いに知りません。

## 機能

![機能の図](docs/i18n/ja/features.svg)

- **クエリビルダーチェーン** — `scout.Builder` の Fluent API：`Query` / `Where` / `WhereIn` / `WhereNotIn` / `OrderBy` / `OrderByDesc` / `Latest` / `Oldest` / `Take` / `Skip`、高度な条件 `VectorSearch` / `WhereRange` / `WhereGeoDistance` / `FulltextSearch` / `OrderByVectorSimilarity` / `OrderByGeoDistance`、コールバック注入 `QueryCB` / `CallbackCB` / `AddResultProcessor`。検索フィールドの解決順序：`Options["fields"]` → `FullTextColumner` → `FieldNames`。
- **DSL 中間表現** — [engines/dsl.go](engines/dsl.go) が高度な条件を統一的な bool クエリ JSON（`multi_match`、`term`、`range`、`geo_distance`、`wildcard`、`regexp` など）にコンパイルし、Elasticsearch / OpenSearch が直接消費。`MapBooleanToBoolKey` が and/or/not を `filter` / `should` / `must_not` に変換します。
- **ページネーションとカーソル** — `Paginate` / `PaginateRaw` / `SimplePaginate` / `Cursor`（バッファ付きチャネルでの遅延イテレーション）；`PaginationResult` は `LastPage` / `HasMorePages` / `AppendQuery` を提供。`Result.As[T]` とパッケージレベル汎用関数 `scout.GetAs[T]` で型アサーションなしにモデルを取得できます。
- **ソフトデリート** — インデックスドキュメントに `__soft_deleted` メタデータ（0/1）を付与。`OnlyTrashed` / `WithTrashed` でクエリをフィルタ。database エンジンは `deleted_at` カラムを使用します。
- **非同期キュー** — プロセス内キュー（`chan` バッファ 4096 + ワーカー goroutine）、`SCOUT_QUEUE=1` で有効化。キューが無い場合は警告付きで同期フォールバック。`MakeAllSearchable` はチャンク単位でインポート（デフォルト 500 件/チャンク）。
- **イベントとオブザーバー** — `ModelObserver` が `Saved` / `Deleted` / `Restored` を監視して自動同期。`EventBus` が `scout.models_imported` / `scout.models_flushed` を発行。`WithoutSyncingToSearch` で一時的に同期を無効化できます。
- **マルチエンジン登録** — `Manager.Extend` + `engines.Register` で 15 個のドライバ名を登録。ドライバは遅延生成されキャッシュされ、未知のドライバは `scout.ErrNotSupported` を返します。エンジン切り替えは `SCOUT_DRIVER` の変更だけです。

## 設計思想

![設計思想の図](docs/i18n/ja/design.svg)

- **チェーン Builder がクエリ意図を担う** — `Search(ctx, query, cb)` は `*scout.Builder` を返し、チェーンは状態を蓄積するだけ。`Get` / `First` / `Paginate` / `Cursor` を呼んだ時点で初めてエンジンへの往復が発生します。クエリ修正・リクエストボディの傍受・結果の後処理はすべてコールバック経由で行われ、エンジンは関知しません。
- **DSL 中間表現** — 高度な条件を bool クエリ JSON にコンパイルし、ES / OpenSearch が直接消費。Meilisearch / Typesense / Algolia / database はそれぞれ独自の構文に翻訳します。エンジン差異は翻訳層に隔離されます。
- **エンジンインターフェースの隔離** — `Engine` / `AdvancedEngine` はドキュメントと結果だけを扱い、モデル型には関与しません。`MapIDs` / `Map` が結果 ID をモデルに戻し、`attachMeta` が `KeyString` で位置合わせするため、フィルタ後のモデル集合がずれることはありません。
- **意図的な ponytail 簡略化** — トレードオフはコード内で `ponytail:` コメントとして明記：Redis の代わりにプロセス内キュー、`MemorySource` の線形スキャン O(ids×models)、Algolia の `whereNotIns` を `"0=1"` の no-op に、v3/v4 を単一構造体 + バージョンフラグに、ランダムソートを `_score asc` プレースホルダに、など。純粋な標準ライブラリのみ、サードパーティ SDK なし。

## ライフサイクル

![検索ライフサイクルの図](docs/i18n/ja/lifecycle.svg)

**検索パス**: `Searchable(model, source).Search(ctx, query, cb)` が `*scout.Builder` を作成 → `Manager.Driver(name)` がエンジンをディスパッチ（遅延生成・キャッシュ）→ `engine.Search` / `Paginate`（REST / SQL / インメモリ）→ `scout.Result`（Hits · Total · Aggregations · Raw）にパース → モデルの埋め戻しが必要な場合（`Get` / `Paginate` / `First`）：`MapIDs` → `Source.ByIDs` → `attachMeta`（`KeyString` で位置合わせ、エンジンが返した順序を維持）→ `ResultItem` / `PaginationResult` を返却。`Keys` / `GetAggregations` / `PaginateRaw` はモデルを読み込まず直接返却します。

**書き込みパス**: `ModelObserver` がモデルイベントを捕捉 → `Queue.PushNamed("scout_make" / "scout_remove" / "scout_make_range")` が非同期実行（無効時は同期フォールバック）→ `engine.Update` / `Delete`（ソフトデリート時は `__soft_deleted` メタデータ付き）→ `EventBus` が `scout.models_imported` / `scout.models_flushed` を発行。

## プロジェクト構造

```
go-scout/
├── go.mod                  # モジュール定義: github.com/erikwang2013/go-scout · go 1.24.1 · 依存ゼロ
├── .gitignore              # IDE・キャッシュ・鍵ファイルの無視
├── scout.go                # Scout ファサード: Config / Manager / Events / Queue / Observer の配線とファクトリ
├── config.go               # 設定ツリー: DefaultConfig + Env 上書き + ドットパスアクセス
├── engine.go               # Engine / AdvancedEngine インターフェース + Result / Hit / PaginationResult 型
├── manager.go              # EngineManager: Extend 登録、遅延 Driver 生成・キャッシュ、デフォルト null エンジン
├── builder.go              # Builder 構造体 + チェーン条件構築（Where / OrderBy / Take / 高度な条件）
├── builder_search.go       # クエリ実行: Raw / Get / First / Paginate / Cursor / GetAs[T]
├── searchable.go           # Searchable: モデルとデータソースのバインド、全量インポート/エクスポート、ソフトデリートメタデータ
├── observer.go             # ModelObserver: Saved / Deleted / Restored の自動同期
├── events.go               # EventBus: スレッドセーフな同期 Pub/Sub + import/flush イベント
├── queue.go                # Queue: プロセス内非同期キュー（chan 4096）+ 同期フォールバック
├── model.go                # ScoutModel インターフェース + オプション拡張インターフェース + KeyName/KeyString ヘルパー
├── source.go               # Source[T] インターフェース + MemorySource（線形スキャン）
├── exceptions.go           # エラー体系 ErrNotSupported / ErrScout
├── cmd/scout/main.go       # CLI デモ: import / flush / index / queue-import ほか計 7 サブコマンド
├── engines/
│   ├── engine.go           # HTTP クライアント（Auth、TLS スキップ）+ 共通 DoJSON/DoBytes ヘルパー
│   ├── register.go         # SetDatabase + Register: 15 ドライバ名 + Drivers()
│   ├── dsl.go              # DSL 中間表現: bool クエリ / ソート / アグリゲーション / ファセット / ハイライト
│   ├── null.go             # NullEngine: 空実装
│   ├── collection.go       # CollectionEngine: インメモリ検索（大文字小文字を無視した部分一致）
│   ├── database.go         # DatabaseEngine: テーブルをインデックスとして SQL（LIKE/ILIKE + pgsql tsvector）
│   ├── elasticsearch.go    # ElasticsearchEngine + OpenSearch と共有する es* ヘルパー
│   ├── opensearch.go       # OpenSearchEngine（基本ドライバ、Advanced Engine も兼ねる）
│   ├── meilisearch.go      # MeilisearchEngine: フィルタ / ソート / ベクトルハイブリッド / ファセット
│   ├── typesense.go        # TypesenseEngine: filter_by / アグリゲーション / グルーピング / 最近傍検索
│   ├── algolia.go          # AlgoliaEngine: v3/v4 を単一構造体 + バージョンフラグで対応
│   └── xunsearch.go        # XunSearchEngine: 索引デーモン + 検索デーモン
├── docs/
│   ├── arch.svg            # アーキテクチャ層の図
│   ├── features.svg        # 機能の図
│   ├── design.svg          # 設計思想の図
│   └── lifecycle.svg       # 検索ライフサイクルのフローチャート
└── engines/
    ├── collection_test.go  # インメモリエンジンの振る舞いテスト（フィルタ / ソート / ページネーション / 埋め戻し）
    ├── database_test.go    # SQL 生成とアサーション（assertSQL 完全一致）
    ├── meilisearch_test.go # Meilisearch リクエスト/パーステスト（httptest スタブ）
    ├── typesense_test.go   # Typesense リクエスト/パーステスト（404 自動コレクション作成含む）
    └── null_test.go        # NullEngine の空振る舞いテスト
```

## クイックスタート / 使い方

### 1. 導入

```bash
go get github.com/erikwang2013/go-scout
```

サードパーティ依存はありません — 取り込んですぐ使えます。

### 2. ドライバの設定

`scout.DefaultConfig()` が環境変数を読み取ります。共通オプション：

| 環境変数 | デフォルト | 説明 |
|---|---|---|
| `SCOUT_DRIVER` | `database` | エンジンドライバ名。空文字または `"false"` は null にフォールバック |
| `SCOUT_PREFIX` | 空 | インデックスプレフィックス |
| `SCOUT_QUEUE` | 無効 | 1 にすると非同期キューを有効化 |
| `SCOUT_SOFT_DELETE` | 無効 | ソフトデリートメタデータをドキュメントとともにインデックスへ |
| `SCOUT_CHUNK_SEARCHABLE` / `SCOUT_CHUNK_UNSEARCHABLE` | `500` | 一括インポート/削除のチャンクサイズ |
| `SCOUT_BULK_SIZE` | `100` | 一括書き込みサイズ（opensearch） |

エンジン固有（設定ツリーのパス `engine.key` は Env 名に対応、例：`opensearch.host` ← `OPENSEARCH_HTTP_HOST`）：

| エンジン | 環境変数（デフォルト） |
|---|---|
| meilisearch | `MEILISEARCH_HOST`（`http://127.0.0.1:7700`）、`MEILISEARCH_KEY` |
| typesense | `TYPESENSE_HOST`（`127.0.0.1`）、`TYPESENSE_PORT`（`8108`）、`TYPESENSE_PROTOCOL`（`http`）、`TYPESENSE_API_KEY`（`xyz`）、`TYPESENSE_IMPORT_ACTION`（`upsert`）、`TYPESENSE_MAX_TOTAL_RESULTS`（`1000`） |
| elasticsearch | `ELASTICSEARCH_HOST`（`http://127.0.0.1:9200`、hosts リストの先頭）+ `elasticsearch.auth`（user/password） |
| opensearch | `OPENSEARCH_HTTP_HOST`（`https://127.0.0.1:6205`）、`OPENSEARCH_USERNAME` / `OPENSEARCH_PASSWORD`（`admin`/`admin`）、`OPENSEARCH_SSL_VERIFICATION`（デフォルトで TLS 検証をスキップ）、`OPENSEARCH_TIMEOUT`（`30` 秒） |
| xunsearch | `XUNSEARCH_INDEX_HOST`（`127.0.0.1`）+ `XUNSEARCH_INDEX_PORT`（`8383`）、`XUNSEARCH_SEARCH_HOST` + `XUNSEARCH_SEARCH_PORT`（`8384`）、`XUNSEARCH_DEFAULT_INDEX`（`default`）、`XUNSEARCH_CHARSET`（`utf-8`） |
| algolia | `ALGOLIA_APP_ID`、`ALGOLIA_SECRET`（欠けているとエンジン構築時に panic） |

`database` ドライバはさらに、注入されたデータベース接続と方言が必要です：

```go
engines.SetDatabase(db, "mysql") // dialect: mysql / pgsql / postgres / sqlite
```

### 3. 最小限の例

```go
package main

import (
	"context"
	"fmt"

	"github.com/erikwang2013/go-scout"
	"github.com/erikwang2013/go-scout/engines"
)

// Post は scout.ScoutModel を実装して検索に参加する
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

	// 初期化: デフォルト設定 + 全エンジン登録（SCOUT_DRIVER が決定）
	s := scout.NewWithConfig(scout.DefaultConfig())
	engines.Register(s.Manager)

	// モデルとデータソースをバインドし、全量インポート（chunk 0 = デフォルトチャンク 500）
	sr := s.Searchable(&Post{}, scout.NewMemorySource(
		Post{ID: 1, Title: "Go module layout", Body: "packages and imports"},
		Post{ID: 2, Title: "Indexing strategies", Body: "bulk writes"},
	))
	_ = sr.MakeAllSearchable(ctx, 0)

	// チェーンクエリ: Search → Where → OrderByDesc → Take → Get
	items, err := sr.Search(ctx, "golang", nil). // 第 3 引数はクエリコールバック、nil 可
		Where("title", "indexing").
		OrderByDesc("created_at").
		Take(10).
		Get(ctx)
	if err != nil {
		panic(err)
	}
	for _, it := range items {
		post := it.Model.(Post) // ResultItem.Model は元のモデルが埋め戻されている
		fmt.Println(post.ID, it.Score)
	}

	// 汎用取得: 型アサーション不要の GetAs[T]
	posts, _ := scout.GetAs[Post](ctx, sr.Search(ctx, "indexing", nil).Take(5))
	_ = posts
}
```

主な実行メソッド：`Get(ctx) ([]ResultItem, error)`、`First(ctx)`、`Paginate(ctx, perPage, page, pageName)`（デフォルト 15 件/ページ、ページパラメータ名はデフォルト `page`）、`Cursor(ctx)`（遅延ストリーミング）、`Keys(ctx)`（主キーのみ）。ページネーション結果の `PaginationResult` は `LastPage()` / `HasMorePages()` / `AppendQuery()` も提供し、ページリンクに使えます。

### 4. CLI デモ

`cmd/scout` は自己完結型のデモ CLI です。起動時に `SCOUT_DRIVER` を `collection` に設定し、`NewMemorySource` で 7 件の `post` データをプリロードします（タイトルが空の下書きは `ShouldBeSearchable` が false のためスキップ）。

```bash
go run ./cmd/scout                # 全サブコマンドの使い方を表示
go run ./cmd/scout index posts --key id        # インデックス作成
go run ./cmd/scout import --chunk 3 --fresh    # 全量インポート（3 件/チャンク、先に空にする）
go run ./cmd/scout flush          # インデックスを空にする
go run ./cmd/scout queue-import --chunk 3 --min 1 --max 100 --workers 4  # キー範囲でキューインポート
go run ./cmd/scout sync-index-settings --driver meilisearch  # インデックス設定の同期（UpdateIndexSettings 対応エンジンのみ）
go run ./cmd/scout delete-index posts           # 単一インデックス削除
go run ./cmd/scout delete-all-indexes           # 全インデックス削除
```

## ライセンスと注意事項

本プロジェクトは PHP プラグイン webman-scout の Go 移植版です。クラス構造・ドライバ名・動作セマンティクスはオリジナルと一貫しています。簡略化のトレードオフはすべてソースコード内の `ponytail:` コメントで明記されています（プロセス内キュー、インメモリデータソースの線形スキャン、Algolia の `"0=1"` no-op など）。

## 寄付（Donate）

ご支援ありがとうございます！ いただいた寄付は本プロジェクトの継続的なメンテナンスと発展に役立てます。ご協力お願いいたします：

<p align="center">
<table>
<tr>
<td align="center">
<b>微信支付（WeChat Pay）</b><br/>
<img src="docs/weixinpay.png" width="130" height="130" alt="微信支付 QRコード"/>
</td>
<td align="center">
<b>支付宝（Alipay）</b><br/>
<img src="docs/alipay.png" width="130" height="130" alt="支付宝 QRコード"/>
</td>
</tr>
</table>
</p>

### 暗号資産での寄付（Crypto Donate）

以下のメインネットに対応しています。送金前に受取アドレスが各メインネットと一致しているかご確認ください：

| メインネット | ウォレットアドレス | QRコード |
|---|---|---|
| BNB Smart Chain (BEP20) | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/1.jpg" width="100" height="100" alt="BNB Smart Chain (BEP20) QRコード"/> |
| Tron (TRC20) | `TEdDHWLajt1XvqtPDWmQctdrJaC3pzZZzz` | <img src="docs/coin/2.jpg" width="100" height="100" alt="Tron (TRC20) QRコード"/> |
| Ethereum (ERC20) | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/3.jpg" width="100" height="100" alt="Ethereum (ERC20) QRコード"/> |
| Aptos | `0x836e3780edfc3f7b2372b39e2a1a3a5d7adfaccd96c726f21cfde1b50dd68030` | <img src="docs/coin/4.jpg" width="100" height="100" alt="Aptos QRコード"/> |
| Plasma | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/5.jpg" width="100" height="100" alt="Plasma QRコード"/> |
| Polygon POS | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/6.jpg" width="100" height="100" alt="Polygon POS QRコード"/> |
| Solana | `2hfhboHdmdrYsY25XfQSsEWxq5ip4EQsR7f4AzSRMUyr` | <img src="docs/coin/7.jpg" width="100" height="100" alt="Solana QRコード"/> |
| The Open Network (TON) | `UQB9kFQohzmXUir9QSSZq01iwl9aQZIDdBpNmDklljRtCoGK` | <img src="docs/coin/8.jpg" width="100" height="100" alt="The Open Network (TON) QRコード"/> |
| Arbitrum One | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/9.jpg" width="100" height="100" alt="Arbitrum One QRコード"/> |
| AVAX C-Chain | `0x355d429f97511897ccb4e271ec888205f9ab6629` | <img src="docs/coin/10.jpg" width="100" height="100" alt="AVAX C-Chain QRコード"/> |

### 海外送金（銀行振込）

**受取人情報**

- 受取人名：WANG KEXUN
- 受取口座番号：881015918251

**受取銀行**

- ZA Bank SWIFT コード：`AABLHKHHXXX`
- 銀行名：ZA Bank Limited
- 銀行コード：387
- 銀行住所：`Core F, Cyberport 3, 100 Cyberport Road, Hong Kong`

**クロスボーダー送金時のコルレス銀行（必要な場合）**

> これはクロスボーダー送金の中継銀行（コルレス銀行）であり、受取銀行ではありません。送金元銀行にコルレス銀行情報が必要かどうかお問い合わせください。

- HKD・CNY・USD 建ての送金では、コルレス銀行は Citibank です：
  - 銀行名：Citibank N.A. Hong Kong
  - SWIFT コード：`CITIHKHXXXX`
  - 銀行コード：006
  - 支店名：Hong Kong Branch
  - 支店番号：391
  - 銀行住所：`Citibank Tower, Citibank Plaza, 3 Garden Road, Central, Hong Kong`
- その他の通貨の送金では、コルレス銀行は BNY Mellon です：
  - 銀行名：THE BANK OF NEW YORK MELLON
  - SWIFT コード：`IRVTUS3NXXX`
  - 銀行住所：`THE BANK OF NEW YORK MELLON, 240 GREENWICH STREET, NEW YORK, United States`
