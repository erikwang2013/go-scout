package scout

import (
	"fmt"
	"strconv"
	"time"
)

// ScoutModel is the contract a struct must satisfy to be searchable.
// It is the Go equivalent of a Laravel Eloquent Model using the Searchable trait.
//
// Required methods mirror the trait's defaults; everything else is provided by
// the optional interfaces below, which are detected by type assertion so a model
// only implements what it needs.
type ScoutModel interface {
	// ScoutKey returns the primary key value used to identify the record in the index.
	ScoutKey() any
	// TableName returns the backing table / collection name (used as the default index name).
	TableName() string
	// ToSearchableArray returns the fields to index for this record.
	ToSearchableArray() map[string]any
	// ShouldBeSearchable reports whether this record belongs in the index.
	ShouldBeSearchable() bool
}

// --- Optional interfaces (all defaults: index = prefix+TableName, key "id", searchable, etc.) ---

// SearchableAser customizes the search index name (trait: searchableAs).
type SearchableAser interface{ SearchableAs() string }

// IndexableAser customizes the indexing index name (trait: indexableAs).
type IndexableAser interface{ IndexableAs() string }

// IndexUpdater decides whether the index is refreshed on save (trait: searchIndexShouldBeUpdated).
type IndexUpdater interface{ SearchIndexShouldBeUpdated() bool }

// WasSearchableer reports prior index state for save/delete (trait: wasSearchableBeforeUpdate/Delete).
type WasSearchableer interface {
	WasSearchableBeforeUpdate() bool
	WasSearchableBeforeDelete() bool
}

// KeyNameer overrides the key column (trait: getScoutKeyName, default "id").
type KeyNameer interface{ KeyName() string }

// KeyTypeer overrides the key type (trait: getScoutKeyType, default "int").
type KeyTypeer interface{ KeyType() string }

// CreatedAtColer names the created-at column for latest()/oldest() (default "created_at").
type CreatedAtColer interface{ GetCreatedAtColumn() string }

// FullTextColumner declares full-text columns (trait attribute SearchUsingFullText).
type FullTextColumner interface{ FullTextColumns() []string }

// FullTextOptionser declares full-text search options (attribute options arg).
type FullTextOptionser interface{ FullTextOptions() map[string]any }

// PrefixColumner declares prefix-search columns (trait attribute SearchUsingPrefix).
type PrefixColumner interface{ PrefixColumns() []string }

// MakeSearchableUser filters/transforms the model set before indexing (trait: makeSearchableUsing).
type MakeSearchableUser interface {
	MakeSearchableUsing(models []ScoutModel) []ScoutModel
}

// PerPageer sets the default page size (trait: getPerPage).
type PerPageer interface{ GetPerPage() int }

// SoftDeleter marks a model that uses soft deletes (trait: SoftDeletes).
type SoftDeleter interface {
	// Trashed reports whether the record is soft-deleted.
	Trashed() bool
	// DeletedAt returns the soft-delete timestamp, or nil if not deleted.
	DeletedAt() *time.Time
}

// Restoreable lets soft-deleted models be restored (observer: restored event).
type Restoreable interface{ Restore() bool }

// Source loads ScoutModels from a backing store. Engines that search the source
// data directly (collection, database) need this; the database engine uses a
// *sql.DB instead. It is the Go stand-in for Eloquent's model->query().
type Source[T any] interface {
	// All returns every record currently visible in the store.
	All(ctx any) ([]T, error)
	// ByIDs returns the records whose key matches one of ids, preserving order.
	ByIDs(ctx any, ids []any) ([]T, error)
	// Count returns the number of records visible in the store.
	Count(ctx any) (int, error)
}

// --- Helper accessors: resolve trait defaults without boilerplate in models. ---

// SearchableAsOf returns the model's search index name (prefix + table, or override).
func SearchableAsOf(m ScoutModel, prefix string) string {
	if s, ok := m.(SearchableAser); ok {
		if name := s.SearchableAs(); name != "" {
			return name
		}
	}
	return prefix + m.TableName()
}

// IndexableAsOf returns the model's index name when indexing.
func IndexableAsOf(m ScoutModel, prefix string) string {
	if s, ok := m.(IndexableAser); ok {
		if name := s.IndexableAs(); name != "" {
			return name
		}
	}
	return SearchableAsOf(m, prefix)
}

// KeyNameOf returns the key column name (default "id").
func KeyNameOf(m ScoutModel) string {
	if k, ok := m.(KeyNameer); ok && k.KeyName() != "" {
		return k.KeyName()
	}
	return "id"
}

// KeyTypeOf returns the key type (default "int").
func KeyTypeOf(m ScoutModel) string {
	if k, ok := m.(KeyTypeer); ok && k.KeyType() != "" {
		return k.KeyType()
	}
	return "int"
}

// CreatedAtColumnOf returns the created-at column (default "created_at").
func CreatedAtColumnOf(m ScoutModel) string {
	if c, ok := m.(CreatedAtColer); ok && c.GetCreatedAtColumn() != "" {
		return c.GetCreatedAtColumn()
	}
	return "created_at"
}

// PerPageOf returns the default per-page (default 15).
func PerPageOf(m ScoutModel) int {
	if p, ok := m.(PerPageer); ok && p.GetPerPage() > 0 {
		return p.GetPerPage()
	}
	return 15
}

// SearchIndexShouldBeUpdated reports whether the index refreshes on save (default true).
func SearchIndexShouldBeUpdated(m ScoutModel) bool {
	if u, ok := m.(IndexUpdater); ok {
		return u.SearchIndexShouldBeUpdated()
	}
	return true
}

// WasSearchableBeforeUpdate reports whether the record was indexed before an update (default true).
func WasSearchableBeforeUpdate(m ScoutModel) bool {
	if w, ok := m.(WasSearchableer); ok {
		return w.WasSearchableBeforeUpdate()
	}
	return true
}

// WasSearchableBeforeDelete reports whether the record was indexed before deletion (default true).
func WasSearchableBeforeDelete(m ScoutModel) bool {
	if w, ok := m.(WasSearchableer); ok {
		return w.WasSearchableBeforeDelete()
	}
	return true
}

// UsesSoftDelete reports whether the model type opts into soft-delete handling.
func UsesSoftDelete(m ScoutModel) bool { _, ok := m.(SoftDeleter); return ok }

// FullTextColumnsOf returns the model's full-text columns (may be nil).
func FullTextColumnsOf(m ScoutModel) []string {
	if f, ok := m.(FullTextColumner); ok {
		return f.FullTextColumns()
	}
	return nil
}

// FullTextOptionsOf returns the model's full-text options (may be nil).
func FullTextOptionsOf(m ScoutModel) map[string]any {
	if f, ok := m.(FullTextOptionser); ok {
		return f.FullTextOptions()
	}
	return nil
}

// PrefixColumnsOf returns the model's prefix-search columns (may be nil).
func PrefixColumnsOf(m ScoutModel) []string {
	if p, ok := m.(PrefixColumner); ok {
		return p.PrefixColumns()
	}
	return nil
}

// MakeSearchableUsing applies the model's pre-index transformation (identity if unset).
func MakeSearchableUsing(m ScoutModel, models []ScoutModel) []ScoutModel {
	if u, ok := m.(MakeSearchableUser); ok {
		return u.MakeSearchableUsing(models)
	}
	return models
}

// IsIntegerKey reports whether the key type is an integer (mirrors in_array(keyType, ['int','integer'])).
func IsIntegerKey(m ScoutModel) bool {
	switch KeyTypeOf(m) {
	case "int", "integer":
		return true
	}
	return false
}

// FieldNames returns the field names the model contributes to the index, in
// insertion order. Engines use this as the default search column set, mirroring
// array_keys($model->toSearchableArray()) in the PHP database engine.
func FieldNames(m ScoutModel) []string {
	arr := m.ToSearchableArray()
	out := make([]string, 0, len(arr))
	for k := range arr {
		out = append(out, k)
	}
	return out
}

// KeyString renders a key value as a string for index document ids.
func KeyString(v any) string {
	switch n := v.(type) {
	case nil:
		return ""
	case string:
		return n
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}
