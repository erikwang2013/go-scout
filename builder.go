package scout

import (
	"context"
	"reflect"
	"strings"
)

// OrderClause is a single ORDER BY directive.
type OrderClause struct {
	Column    string
	Direction string // "asc" | "desc"
}

// AdvancedWhere is a structured query condition (range, geo_distance, ...).
type AdvancedWhere struct {
	Field    string
	Operator string // range, date_range, geo_distance, exists, wildcard, match, gt, ...
	Value    any
	Boolean  string // "and" | "or" | "not"
	Nested   bool
	Options  map[string]any
}

// SortClause is an advanced sort directive.
type SortClause struct {
	Type      string // "field" | "vector_similarity" | "geo_distance" | "random"
	Field     string
	Direction string // "asc" | "desc"
	Options   map[string]any
	// Location is set for geo_distance sorts: {"lat": x, "lng": y}.
	Location map[string]float64
	// Vector is set for vector_similarity sorts.
	Vector []float64
}

// Aggregation describes a named aggregation.
type Aggregation struct {
	Type    string // terms, range, date_range, histogram, date_histogram, stats, ...
	Field   string
	Options map[string]any
}

// Builder is the fluent search query builder. It mirrors scout's Builder class,
// including the advanced OpenSearch/Elasticsearch-oriented extensions.
type Builder struct {
	// Model is a prototype instance of the searched type (used for index names,
	// key columns and full-text/prefix column discovery).
	Model ScoutModel
	// Source loads models from the backing store; used by collection/database
	// engines and when hydrating results back into models.
	Source Source[ScoutModel]
	// Engine is the search backend this builder runs against.
	Engine Engine
	// Config is the Scout configuration in effect.
	Config *Config

	// Query is the search expression.
	Query string
	// Index is an explicit index override (Within).
	Index string
	// Wheres are equality constraints.
	Wheres map[string]any
	// WhereIns are IN constraints.
	WhereIns map[string][]any
	// WhereNotIns are NOT IN constraints.
	WhereNotIns map[string][]any
	// Limit caps the number of results.
	Limit int
	// Offset is the starting position (paged queries set this).
	Offset int
	// Orders are ORDER BY clauses.
	Orders []OrderClause
	// Options are engine-specific search options (highlight, fields, _source, ...).
	Options map[string]any
	// Callback is an engine-specific query modifier. For the database engine it
	// receives a SQL condition builder; for advanced engines the raw params map.
	Callback func(ctx context.Context, b *Builder, params any) any
	// QueryCallback modifies the model-loading query when hydrating by ids.
	QueryCallback func(ctx context.Context, b *Builder) error
	// AfterRawSearchCallback inspects/rewrites the raw engine result.
	AfterRawSearchCallback func(*Result) *Result

	// --- advanced builder state ---
	vectorSearch     map[string]any
	advancedWheres   []AdvancedWhere
	sorts            []SortClause
	resultProcessors []func(*Result) *Result
	aggregations     map[string]Aggregation
	facets           map[string]map[string]any

	// SoftDelete seeds the __soft_deleted=0 constraint when the model is
	// soft-deletable and the soft_delete config is enabled.
	SoftDelete bool

	// modelType is the concrete type of Model, used by As() to type-assert.
	modelType reflect.Type
}

// NewBuilder creates a builder for a model type, mirroring Scout::search().
func NewBuilder(model ScoutModel, source Source[ScoutModel], engine Engine, cfg *Config, query string, callback func(context.Context, *Builder, any) any, softDelete bool) *Builder {
	b := &Builder{
		Model:        model,
		Source:       source,
		Engine:       engine,
		Config:       cfg,
		Query:        query,
		Callback:     callback,
		Wheres:       map[string]any{},
		WhereIns:     map[string][]any{},
		WhereNotIns:  map[string][]any{},
		Options:      map[string]any{},
		aggregations: map[string]Aggregation{},
		facets:       map[string]map[string]any{},
		SoftDelete:   softDelete,
		modelType:    reflect.TypeOf(model),
	}
	if softDelete {
		b.Wheres["__soft_deleted"] = 0
	}
	return b
}

// --- fluent setters ---

// Within performs the search on a custom index.
func (b *Builder) Within(index string) *Builder { b.Index = index; return b }

// Where adds an equality constraint.
func (b *Builder) Where(field string, value any) *Builder { b.Wheres[field] = value; return b }

// WhereIn adds an IN constraint.
func (b *Builder) WhereIn(field string, values []any) *Builder { b.WhereIns[field] = values; return b }

// WhereNotIn adds a NOT IN constraint.
func (b *Builder) WhereNotIn(field string, values []any) *Builder { b.WhereNotIns[field] = values; return b }

// WithTrashed includes soft-deleted records.
func (b *Builder) WithTrashed() *Builder { delete(b.Wheres, "__soft_deleted"); return b }

// OnlyTrashed restricts results to soft-deleted records.
func (b *Builder) OnlyTrashed() *Builder {
	b.WithTrashed()
	b.Wheres["__soft_deleted"] = 1
	return b
}

// Take caps the result count.
func (b *Builder) Take(limit int) *Builder { b.Limit = limit; return b }

// LimitN is an alias of Take, matching the README's ->limit(20) usage.
func (b *Builder) LimitN(limit int) *Builder { b.Limit = limit; return b }

// Skip sets the offset.
func (b *Builder) Skip(offset int) *Builder { b.Offset = offset; return b }

// OrderBy adds an ORDER BY clause (also recorded as an advanced sort).
func (b *Builder) OrderBy(column string, direction string) *Builder {
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir != "asc" {
		dir = "desc"
	}
	b.Orders = append(b.Orders, OrderClause{Column: column, Direction: dir})
	b.sorts = append(b.sorts, SortClause{Type: "field", Field: column, Direction: dir})
	return b
}

// OrderByDesc adds a descending ORDER BY clause.
func (b *Builder) OrderByDesc(column string) *Builder { return b.OrderBy(column, "desc") }

// Latest orders by the created-at column descending.
func (b *Builder) Latest(column string) *Builder {
	if column == "" {
		column = CreatedAtColumnOf(b.Model)
	}
	return b.OrderBy(column, "desc")
}

// Oldest orders by the created-at column ascending.
func (b *Builder) Oldest(column string) *Builder {
	if column == "" {
		column = CreatedAtColumnOf(b.Model)
	}
	return b.OrderBy(column, "asc")
}

// WithOptions sets engine-specific search options.
func (b *Builder) WithOptions(opts map[string]any) *Builder {
	for k, v := range opts {
		b.Options[k] = v
	}
	return b
}

// SetOption sets a single engine-specific option.
func (b *Builder) SetOption(key string, value any) *Builder { b.Options[key] = value; return b }

// QueryCB sets the callback that may modify the model-loading query.
func (b *Builder) QueryCB(cb func(ctx context.Context, b *Builder) error) *Builder {
	b.QueryCallback = cb
	return b
}

// CallbackCB sets the engine-specific query modifier.
func (b *Builder) CallbackCB(cb func(ctx context.Context, b *Builder, params any) any) *Builder {
	b.Callback = cb
	return b
}

// WithRawResults sets the callback that inspects the raw engine result.
func (b *Builder) WithRawResults(cb func(*Result) *Result) *Builder {
	b.AfterRawSearchCallback = cb
	return b
}

// --- advanced builder (OpenSearch / Elasticsearch oriented) ---

// VectorSearch enables vector similarity search. When vector is nil, vectorField
// alone enables vector-mode ranking with the given options.
func (b *Builder) VectorSearch(vector []float64, vectorField string, options map[string]any) *Builder {
	opts := map[string]any{
		"metric":    "cosine",
		"top_k":     10,
		"threshold": 0.7,
	}
	for k, v := range options {
		opts[k] = v
	}
	if vector != nil {
		b.vectorSearch = map[string]any{"vector": vector, "field": vectorField, "options": opts}
	} else {
		b.vectorSearch = map[string]any{"field": vectorField, "options": opts}
	}
	return b
}

// WhereAdvanced adds a structured query condition.
func (b *Builder) WhereAdvanced(field, operator string, value any, boolean string, nested bool) *Builder {
	if boolean == "" {
		boolean = "and"
	}
	b.advancedWheres = append(b.advancedWheres, AdvancedWhere{
		Field: field, Operator: operator, Value: value, Boolean: boolean, Nested: nested,
	})
	return b
}

// WhereRange adds a range condition. rangeOpts may hold "gte"/"lte"/"gt"/"lt" keys.
func (b *Builder) WhereRange(field string, rangeOpts map[string]any, inclusive bool) *Builder {
	return b.WhereAdvanced(field, "range", map[string]any{"range": rangeOpts, "inclusive": inclusive}, "and", false)
}

// WhereGeoDistance adds a geo-radius condition (radius in km).
func (b *Builder) WhereGeoDistance(field string, lat, lng, radius float64) *Builder {
	return b.WhereAdvanced(field, "geo_distance", map[string]any{"lat": lat, "lng": lng, "radius": radius}, "and", false)
}

// FulltextSearch adds an enhanced full-text clause over the given fields.
func (b *Builder) FulltextSearch(query string, fields []string, options map[string]any) *Builder {
	if len(fields) == 0 {
		fields = FullTextColumnsOf(b.Model)
	}
	opts := map[string]any{"operator": "and", "fuzziness": "auto", "boost": 1.0}
	for k, v := range options {
		opts[k] = v
	}
	b.advancedWheres = append(b.advancedWheres, AdvancedWhere{
		Field: "fulltext", Operator: "fulltext",
		Value:   map[string]any{"query": query, "fields": fields},
		Boolean: "and", Options: opts,
	})
	return b
}

// OrderByVectorSimilarity sorts by vector similarity.
func (b *Builder) OrderByVectorSimilarity(vector []float64, vectorField string) *Builder {
	b.sorts = append(b.sorts, SortClause{Type: "vector_similarity", Vector: vector, Field: vectorField})
	return b
}

// OrderByGeoDistance sorts by distance from a location.
func (b *Builder) OrderByGeoDistance(field string, lat, lng float64, direction string) *Builder {
	if direction == "" {
		direction = "asc"
	}
	b.sorts = append(b.sorts, SortClause{
		Type: "geo_distance", Field: field, Direction: direction,
		Location: map[string]float64{"lat": lat, "lng": lng},
	})
	return b
}

// AddResultProcessor registers a processor applied to the raw result.
func (b *Builder) AddResultProcessor(p func(*Result) *Result) *Builder {
	b.resultProcessors = append(b.resultProcessors, p)
	return b
}

// Aggregate registers a named aggregation.
func (b *Builder) Aggregate(name, aggType, field string, options map[string]any) *Builder {
	b.aggregations[name] = Aggregation{Type: aggType, Field: field, Options: options}
	return b
}

// Facet registers a facet over a field.
func (b *Builder) Facet(field string, options map[string]any) *Builder {
	b.facets[field] = options
	return b
}

// ClearAdvancedConditions resets the advanced builder state.
func (b *Builder) ClearAdvancedConditions() *Builder {
	b.vectorSearch = nil
	b.advancedWheres = nil
	b.sorts = nil
	b.aggregations = map[string]Aggregation{}
	b.facets = map[string]map[string]any{}
	b.resultProcessors = nil
	return b
}

// --- getters used by engines ---

// GetIndex returns the effective index name (override or model default).
func (b *Builder) GetIndex() string {
	if b.Index != "" {
		return b.Index
	}
	return SearchableAsOf(b.Model, b.configPrefix())
}

// GetWhereIns returns the IN constraints.
func (b *Builder) GetWhereIns() map[string][]any { return b.WhereIns }

// GetWhereNotIns returns the NOT IN constraints.
func (b *Builder) GetWhereNotIns() map[string][]any { return b.WhereNotIns }

// GetOrders returns the ORDER BY clauses.
func (b *Builder) GetOrders() []OrderClause { return b.Orders }

// GetSorts returns the advanced sort directives.
func (b *Builder) GetSorts() []SortClause { return b.sorts }

// GetAdvancedWheres returns the structured conditions.
func (b *Builder) GetAdvancedWheres() []AdvancedWhere { return b.advancedWheres }

// GetVectorSearch returns the vector search configuration.
func (b *Builder) GetVectorSearch() map[string]any { return b.vectorSearch }

// GetOptions returns the engine-specific options.
func (b *Builder) GetOptions() map[string]any { return b.Options }

// GetAggregationConfig returns the aggregation definitions.
func (b *Builder) GetAggregationConfig() map[string]Aggregation { return b.aggregations }

// GetFacetConfig returns the facet definitions.
func (b *Builder) GetFacetConfig() map[string]map[string]any { return b.facets }

// GetResultProcessors returns the result processors.
func (b *Builder) GetResultProcessors() []func(*Result) *Result { return b.resultProcessors }

// GetCallback returns the engine-specific query modifier.
func (b *Builder) GetCallback() func(ctx context.Context, b *Builder, params any) any { return b.Callback }

// GetQueryCallback returns the model-loading query modifier.
func (b *Builder) GetQueryCallback() func(ctx context.Context, b *Builder) error { return b.QueryCallback }

// GetAfterRawSearchCallback returns the raw result callback.
func (b *Builder) GetAfterRawSearchCallback() func(*Result) *Result { return b.AfterRawSearchCallback }

// GetLimit returns the result limit (0 = unlimited).
func (b *Builder) GetLimit() int { return b.Limit }

// GetOffset returns the result offset.
func (b *Builder) GetOffset() int { return b.Offset }

// GetSoftDelete reports whether the soft-delete constraint is active.
func (b *Builder) GetSoftDelete() bool { return b.SoftDelete }

// configPrefix returns the configured index prefix.
func (b *Builder) configPrefix() string {
	if b.Config == nil {
		return ""
	}
	return b.Config.Prefix()
}

// ApplyAfterRawSearchCallback invokes the raw result callback, if set.
func (b *Builder) ApplyAfterRawSearchCallback(results *Result) *Result {
	if b.AfterRawSearchCallback != nil {
		if r := b.AfterRawSearchCallback(results); r != nil {
			return r
		}
	}
	return results
}

// InvokeCallback runs the engine-specific callback with params, or nil.
func (b *Builder) InvokeCallback(ctx context.Context, params any) any {
	if b.Callback == nil {
		return nil
	}
	return b.Callback(ctx, b, params)
}

// SearchFields returns the fields the engine should search: the options
// "fields" override, else the model's full-text columns, else all indexed
// field names.
func (b *Builder) SearchFields() []string {
	if f, ok := b.Options["fields"]; ok && f != nil {
		if list, ok := f.([]string); ok {
			return list
		}
		if s, ok := f.(string); ok {
			return strings.Split(s, ",")
		}
	}
	if ft := FullTextColumnsOf(b.Model); len(ft) > 0 {
		return ft
	}
	return FieldNames(b.Model)
}

// Debug returns a dump of the builder state, mirroring Builder::debug().
func (b *Builder) Debug() map[string]any {
	return map[string]any{
		"model":          reflect.TypeOf(b.Model).String(),
		"query":          b.Query,
		"index":          b.GetIndex(),
		"wheres":         b.Wheres,
		"whereIns":       b.WhereIns,
		"whereNotIns":    b.WhereNotIns,
		"advancedWheres": b.advancedWheres,
		"vectorSearch":   b.vectorSearch,
		"sorts":          b.sorts,
		"aggregations":   b.aggregations,
		"facets":         b.facets,
		"options":        b.Options,
		"limit":          b.Limit,
		"offset":         b.Offset,
	}
}
