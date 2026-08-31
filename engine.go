package scout

import "context"

// Result is the normalized shape returned by every engine's search/paginate.
// It mirrors the PHP array ['results' => ..., 'total' => ...] plus the extra
// fields the advanced engines surface (aggregations, suggestions, ...).
type Result struct {
	// Hits are the matched documents, in engine order.
	Hits []Hit
	// Total is the full match count (ignoring paging).
	Total int
	// MaxScore is the highest hit score, when the engine reports one.
	MaxScore *float64
	// Aggregations are the raw aggregation payloads.
	Aggregations map[string]any
	// Suggestions are the raw suggest payloads.
	Suggestions map[string]any
	// Took is the engine's reported execution time in ms.
	Took int
	// TimedOut reports whether the query timed out.
	TimedOut bool
	// Raw is the unprocessed engine response, for Raw() consumers.
	Raw any
}

// Hit is one matched document with its engine metadata.
type Hit struct {
	// ID is the document id as returned by the engine.
	ID any
	// Score is the relevance score.
	Score float64
	// Source is the stored document payload.
	Source map[string]any
	// Highlight holds highlight fragments when highlighting is enabled.
	Highlight map[string]any
	// VectorScore is the KNN similarity score when vector search ran.
	VectorScore *float64
	// Index is the index the hit came from (when the engine reports it).
	Index string
}

// ResultItem pairs a loaded model with the engine metadata attached to it.
// This is the Go equivalent of PHP's collection of models carrying _score,
// _highlight and _vector_score attributes.
type ResultItem struct {
	// Model is the hydrated domain record.
	Model ScoutModel
	// Score is the hit relevance score.
	Score float64
	// Highlight holds the highlight fragments for this model.
	Highlight map[string]any
	// VectorScore is the KNN similarity score for this model.
	VectorScore *float64
}

// Engine is the contract every search backend implements. It mirrors the
// abstract methods of scout's Engines\Engine.
//
// Engines return *Result for Search/Paginate. Search's Hits contain full
// documents; Paginate's Hits are the requested page slice while Total is the
// full match count.
type Engine interface {
	// Name returns the driver name (e.g. "database").
	Name() string

	// Update upserts the given models into the index.
	Update(ctx context.Context, models []ScoutModel) error

	// Delete removes the given models from the index.
	Delete(ctx context.Context, models []ScoutModel) error

	// Search runs the query described by b and returns raw hits.
	Search(ctx context.Context, b *Builder) (*Result, error)

	// Paginate runs the query and returns the perPage/page slice plus the
	// full total count.
	Paginate(ctx context.Context, b *Builder, perPage, page int) (*Result, error)

	// MapIDs extracts the primary keys from a raw result.
	MapIDs(results *Result) []any

	// Map hydrates the model instances for a raw result, preserving order.
	Map(ctx context.Context, b *Builder, results *Result) ([]ScoutModel, error)

	// GetTotalCount extracts the total match count from a raw result.
	GetTotalCount(results *Result) int

	// Flush removes all of the model's records from the index.
	Flush(ctx context.Context, model ScoutModel) error

	// CreateIndex creates a search index with the given options.
	CreateIndex(ctx context.Context, name string, options map[string]any) (any, error)

	// DeleteIndex drops a search index.
	DeleteIndex(ctx context.Context, name string) (any, error)
}

// AdvancedEngine is implemented by engines that understand the advanced builder
// API (vector/KNN search, geo, aggregations, facets, highlighting, suggest).
// It mirrors scout's Advanced*Engine classes.
type AdvancedEngine interface {
	Engine

	// AdvancedSearch runs the full advanced query and returns processed hits.
	AdvancedSearch(ctx context.Context, b *Builder) (*Result, error)

	// GetAggregations runs only the aggregations and returns their payload.
	GetAggregations(ctx context.Context, b *Builder) (map[string]any, error)

	// GetFacets runs only the facet aggregations and returns their payload.
	GetFacets(ctx context.Context, b *Builder) (map[string]any, error)
}

// Factory builds an engine from the configuration. Register with
// EngineManager.Extend; engines.Register wires the shipped drivers.
type Factory func(cfg *Config) Engine

// PaginationResult is returned by Builder.Paginate/SimplePaginate.
type PaginationResult struct {
	// Items are the hydrated models plus their metadata for this page.
	Items []ResultItem
	// Total is the full match count (0 for simple pagination).
	Total int
	// PerPage is the page size.
	PerPage int
	// Page is the 1-based current page.
	Page int
	// PageName is the query parameter name for the page.
	PageName string
	// Path is the pagination path.
	Path string
	// Appends are extra query parameters carried onto page links.
	Appends map[string]any
}

// LastPage returns the total number of pages.
func (p *PaginationResult) LastPage() int {
	if p.PerPage <= 0 {
		return 0
	}
	if p.Total > 0 {
		n := p.Total / p.PerPage
		if p.Total%p.PerPage != 0 {
			n++
		}
		return n
	}
	if len(p.Items) >= p.PerPage {
		return p.Page + 1
	}
	return p.Page
}

// HasMorePages reports whether a next page exists.
func (p *PaginationResult) HasMorePages() bool {
	if p.Total > 0 {
		return p.Page*p.PerPage < p.Total
	}
	return len(p.Items) >= p.PerPage && p.PerPage > 0
}

// HasPages reports whether there is more than one page.
func (p *PaginationResult) HasPages() bool { return p.LastPage() > 1 }

// WithAppends merges extra query parameters onto the pagination links.
func (p *PaginationResult) WithAppends(params map[string]any) *PaginationResult {
	if p.Appends == nil {
		p.Appends = map[string]any{}
	}
	for k, v := range params {
		p.Appends[k] = v
	}
	return p
}

// AppendQuery attaches an empty query parameter to the pagination links, as the
// PHP builder does with ->appends('query', $this->query).
func (p *PaginationResult) AppendQuery() *PaginationResult {
	return p.WithAppends(map[string]any{"query": ""})
}

// AppendQueryFor sets the query value carried on pagination links.
func (p *PaginationResult) AppendQueryFor(q string) *PaginationResult {
	return p.WithAppends(map[string]any{"query": q})
}

// First returns the first item on the page, or nil.
func (p *PaginationResult) First() *ResultItem {
	if len(p.Items) == 0 {
		return nil
	}
	it := p.Items[0]
	return &it
}

// Last returns the last item on the page, or nil.
func (p *PaginationResult) Last() *ResultItem {
	if len(p.Items) == 0 {
		return nil
	}
	it := p.Items[len(p.Items)-1]
	return &it
}

// Models returns just the hydrated models for this page, in order.
func (p *PaginationResult) Models() []ScoutModel {
	out := make([]ScoutModel, len(p.Items))
	for i, it := range p.Items {
		out[i] = it.Model
	}
	return out
}

// As casts hydrated results to a concrete model type, dropping metadata.
// It is a package-level function because Go methods cannot carry type parameters.
func As[T any](items []ResultItem) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if m, ok := it.Model.(T); ok {
			out = append(out, m)
		}
	}
	return out
}

// PageAs casts a whole page to a concrete model type.
func PageAs[T any](p *PaginationResult) []T { return As[T](p.Items) }
