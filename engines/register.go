package engines

import (
	"database/sql"

	"github.com/erikwang2013/go-scout"
)

// databaseDB / databaseDriver hold the connection the database engine runs
// against. The database engine is the only shipped driver that needs a resource
// not described by *scout.Config, so it is set explicitly rather than passed
// through the Factory signature.
//
// ponytail: package-level state instead of a Factory parameter; a
// per-EngineManager field would be cleaner if multiple *sql.DB instances are
// ever needed in one process.
var (
	databaseDB     *sql.DB
	databaseDriver = "mysql"
)

// SetDatabase registers the *sql.DB and dialect the "database" driver uses.
// Call before resolving the driver. dialect is one of "mysql", "pgsql",
// "postgres", "sqlite".
func SetDatabase(db *sql.DB, dialect string) {
	databaseDB = db
	if dialect != "" {
		databaseDriver = dialect
	}
}

// Register wires every shipped driver onto the given EngineManager. Call it
// once at startup; re-calling just re-registers the same factories. Driver
// names mirror the PHP plugin's EngineManager createXxxDriver methods.
func Register(m *scout.EngineManager) {
	m.Extend("null", func(cfg *scout.Config) scout.Engine { return NewNull() })
	m.Extend("collection", func(cfg *scout.Config) scout.Engine { return NewCollection() })
	m.Extend("database", func(cfg *scout.Config) scout.Engine {
		return NewDatabase(databaseDB, databaseDriver)
	})

	m.Extend("elasticsearch", func(cfg *scout.Config) scout.Engine { return NewElasticSearch(cfg) })
	// The PHP plugin ships an AdvancedElasticsearchEngine; its features live on
	// the same Go engine, so this is an explicit alias like advanced_opensearch.
	m.Extend("advanced_elasticsearch", func(cfg *scout.Config) scout.Engine { return NewElasticSearch(cfg) })
	m.Extend("opensearch", func(cfg *scout.Config) scout.Engine { return NewOpenSearch(cfg) })
	// The PHP plugin resolves "opensearch" straight to its Advanced engine,
	// which implements the full advanced builder API. Keep an explicit alias.
	m.Extend("advanced_opensearch", func(cfg *scout.Config) scout.Engine { return NewOpenSearch(cfg) })

	m.Extend("meilisearch", func(cfg *scout.Config) scout.Engine { return NewMeilisearch(cfg) })
	m.Extend("advanced_meilisearch", func(cfg *scout.Config) scout.Engine { return NewMeilisearch(cfg) })

	m.Extend("typesense", func(cfg *scout.Config) scout.Engine { return NewTypesense(cfg) })
	m.Extend("advanced_typesense", func(cfg *scout.Config) scout.Engine { return NewTypesense(cfg) })

	m.Extend("algolia", func(cfg *scout.Config) scout.Engine { return NewAlgolia(cfg) })
	m.Extend("algolia3", func(cfg *scout.Config) scout.Engine { return NewAlgolia3(cfg) })
	m.Extend("algolia4", func(cfg *scout.Config) scout.Engine { return NewAlgolia4(cfg) })

	m.Extend("xunsearch", func(cfg *scout.Config) scout.Engine { return NewXunSearch(cfg) })
	m.Extend("advanced_xunsearch", func(cfg *scout.Config) scout.Engine { return NewAdvancedXunSearch(cfg) })
}

// Drivers lists every driver name Register wires, in registration order.
// Used by the `scout index` CLI subcommand.
func Drivers() []string {
	return []string{
		"null", "collection", "database",
		"elasticsearch", "advanced_elasticsearch", "opensearch", "advanced_opensearch",
		"meilisearch", "advanced_meilisearch",
		"typesense", "advanced_typesense",
		"algolia", "algolia3", "algolia4",
		"xunsearch", "advanced_xunsearch",
	}
}
