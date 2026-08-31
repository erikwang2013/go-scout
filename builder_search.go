package scout

import (
	"context"
)

// Raw runs the search and returns the unprocessed engine result.
func (b *Builder) Raw(ctx context.Context) (*Result, error) {
	if adv, ok := b.Engine.(AdvancedEngine); ok {
		return adv.AdvancedSearch(ctx, b)
	}
	return b.Engine.Search(ctx, b)
}

// Get runs the search and returns hydrated models with their engine metadata,
// mirroring Builder::get(). Advanced engines run the full advanced query; other
// engines fall back to their base search.
func (b *Builder) Get(ctx context.Context) ([]ResultItem, error) {
	raw, err := b.Raw(ctx)
	if err != nil {
		return nil, err
	}
	raw = b.ApplyAfterRawSearchCallback(raw)
	for _, p := range b.resultProcessors {
		if p != nil {
			raw = p(raw)
		}
	}
	models, err := b.Engine.Map(ctx, b, raw)
	if err != nil {
		return nil, err
	}
	return b.attachMeta(models, raw), nil
}

// GetAs casts the search results to a concrete model type, dropping metadata.
// It is a package-level function because Go methods cannot carry type parameters.
func GetAs[T any](ctx context.Context, b *Builder) ([]T, error) {
	items, err := b.Get(ctx)
	if err != nil {
		return nil, err
	}
	return As[T](items), nil
}

// First returns the first result, or nil when there is none.
func (b *Builder) First(ctx context.Context) (*ResultItem, error) {
	items, err := b.Get(ctx)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	it := items[0]
	return &it, nil
}

// Keys returns the primary keys of the search results.
func (b *Builder) Keys(ctx context.Context) ([]any, error) {
	raw, err := b.Raw(ctx)
	if err != nil {
		return nil, err
	}
	return b.Engine.MapIDs(raw), nil
}

// Paginate returns a length-aware page of results.
func (b *Builder) Paginate(ctx context.Context, perPage, page int, pageName string) (*PaginationResult, error) {
	if pageName == "" {
		pageName = "page"
	}
	if perPage <= 0 {
		perPage = PerPageOf(b.Model)
	}
	if page <= 0 {
		page = 1
	}
	if b.Offset == 0 {
		b.Offset = (page - 1) * perPage
	}

	raw, err := b.Engine.Paginate(ctx, b, perPage, page)
	if err != nil {
		return nil, err
	}
	raw = b.ApplyAfterRawSearchCallback(raw)
	models, err := b.Engine.Map(ctx, b, raw)
	if err != nil {
		return nil, err
	}
	items := b.attachMeta(models, raw)
	return b.pageResult(items, raw, perPage, page, pageName), nil
}

// pageResult assembles a PaginationResult and attaches the query to its links.
func (b *Builder) pageResult(items []ResultItem, raw *Result, perPage, page int, pageName string) *PaginationResult {
	pr := &PaginationResult{Items: items, Total: b.TotalCount(raw), PerPage: perPage, Page: page, PageName: pageName}
	return pr.AppendQueryFor(b.Query)
}

// simplePageResult assembles a page without a total count.
func (b *Builder) simplePageResult(items []ResultItem, perPage, page int, pageName string) *PaginationResult {
	pr := &PaginationResult{Items: items, Total: 0, PerPage: perPage, Page: page, PageName: pageName}
	return pr.AppendQueryFor(b.Query)
}

// PaginateRaw returns a page of raw engine hits without model hydration.
func (b *Builder) PaginateRaw(ctx context.Context, perPage, page int, pageName string) (*PaginationResult, error) {
	if pageName == "" {
		pageName = "page"
	}
	if perPage <= 0 {
		perPage = PerPageOf(b.Model)
	}
	if page <= 0 {
		page = 1
	}
	if b.Offset == 0 {
		b.Offset = (page - 1) * perPage
	}
	raw, err := b.Engine.Paginate(ctx, b, perPage, page)
	if err != nil {
		return nil, err
	}
	raw = b.ApplyAfterRawSearchCallback(raw)
	return b.pageResult(b.hitItems(raw), raw, perPage, page, pageName), nil
}

// SimplePaginate returns a simple (non-length-aware) page of results.
func (b *Builder) SimplePaginate(ctx context.Context, perPage, page int, pageName string) (*PaginationResult, error) {
	if pageName == "" {
		pageName = "page"
	}
	if perPage <= 0 {
		perPage = PerPageOf(b.Model)
	}
	if page <= 0 {
		page = 1
	}
	if b.Offset == 0 {
		b.Offset = (page - 1) * perPage
	}
	raw, err := b.Engine.Paginate(ctx, b, perPage, page)
	if err != nil {
		return nil, err
	}
	raw = b.ApplyAfterRawSearchCallback(raw)
	models, err := b.Engine.Map(ctx, b, raw)
	if err != nil {
		return nil, err
	}
	items := b.attachMeta(models, raw)
	// ponytail: simple pagination reports no total; HasMorePages infers from the
	// page being full. Upstream recount via a count query would need extra SQL.
	return b.simplePageResult(items, perPage, page, pageName), nil
}

// SimplePaginateRaw returns a simple page of raw engine hits.
func (b *Builder) SimplePaginateRaw(ctx context.Context, perPage, page int, pageName string) (*PaginationResult, error) {
	if pageName == "" {
		pageName = "page"
	}
	if perPage <= 0 {
		perPage = PerPageOf(b.Model)
	}
	if page <= 0 {
		page = 1
	}
	if b.Offset == 0 {
		b.Offset = (page - 1) * perPage
	}
	raw, err := b.Engine.Paginate(ctx, b, perPage, page)
	if err != nil {
		return nil, err
	}
	raw = b.ApplyAfterRawSearchCallback(raw)
	return b.simplePageResult(b.hitItems(raw), perPage, page, pageName), nil
}

// Cursor streams results lazily, mirroring Builder::cursor().
func (b *Builder) Cursor(ctx context.Context) (<-chan ResultItem, error) {
	ch := make(chan ResultItem, 16)
	go func() {
		defer close(ch)
		items, err := b.Get(ctx)
		if err != nil {
			return
		}
		for _, it := range items {
			select {
			case ch <- it:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// GetAggregations returns the aggregation payloads, or an empty map when the
// engine does not support them.
func (b *Builder) GetAggregations(ctx context.Context) (map[string]any, error) {
	if adv, ok := b.Engine.(AdvancedEngine); ok {
		return adv.GetAggregations(ctx, b)
	}
	return map[string]any{}, nil
}

// GetFacets returns the facet payloads, or an empty map when unsupported.
func (b *Builder) GetFacets(ctx context.Context) (map[string]any, error) {
	if adv, ok := b.Engine.(AdvancedEngine); ok {
		return adv.GetFacets(ctx, b)
	}
	return map[string]any{}, nil
}

// TotalCount extracts the total match count from a raw result.
func (b *Builder) TotalCount(raw *Result) int {
	if raw == nil {
		return 0
	}
	return b.Engine.GetTotalCount(raw)
}

// ModelsByIDs loads models by their keys, honoring the active soft-delete
// constraint and any query callback. Mirrors ScoutModel::getScoutModelsByIds.
func (b *Builder) ModelsByIDs(ctx context.Context, ids []any) ([]ScoutModel, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if b.Source == nil {
		return nil, nil
	}
	models, err := b.Source.ByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	if cb := b.GetQueryCallback(); cb != nil {
		if err := cb(ctx, b); err != nil {
			return nil, err
		}
	}
	return filterSoftDeleted(models, b), nil
}

// filterSoftDeleted applies the __soft_deleted constraint to a model set.
func filterSoftDeleted(models []ScoutModel, b *Builder) []ScoutModel {
	const key = "__soft_deleted"
	v, ok := b.Wheres[key]
	if !ok {
		return models
	}
	want := 0
	if n, ok := v.(int); ok {
		want = n
	}
	out := make([]ScoutModel, 0, len(models))
	for _, m := range models {
		if sd, ok := m.(SoftDeleter); ok {
			got := 0
			if sd.Trashed() {
				got = 1
			}
			if got == want {
				out = append(out, m)
			}
			continue
		}
		if want == 0 {
			out = append(out, m)
		}
	}
	return out
}

// attachMeta pairs hydrated models with the metadata carried on their hits,
// keyed by document id so a filtered model set cannot misalign.
func (b *Builder) attachMeta(models []ScoutModel, raw *Result) []ResultItem {
	items := make([]ResultItem, 0, len(models))
	if raw == nil || len(raw.Hits) == 0 {
		for _, m := range models {
			items = append(items, ResultItem{Model: m})
		}
		return items
	}
	byID := map[string]Hit{}
	for _, h := range raw.Hits {
		byID[KeyString(h.ID)] = h
	}
	for _, m := range models {
		hit, ok := byID[KeyString(m.ScoutKey())]
		if !ok {
			continue
		}
		items = append(items, ResultItem{
			Model: m, Score: hit.Score, Highlight: hit.Highlight, VectorScore: hit.VectorScore,
		})
	}
	return items
}

// hitItems turns raw hits into ResultItems without model hydration, for the
// *Raw pagination variants.
func (b *Builder) hitItems(raw *Result) []ResultItem {
	items := make([]ResultItem, 0, len(raw.Hits))
	for _, h := range raw.Hits {
		src := h.Source
		if src == nil {
			src = map[string]any{}
		}
		src["_score"] = h.Score
		src["_id"] = h.ID
		if h.Highlight != nil {
			src["_highlight"] = h.Highlight
		}
		if h.VectorScore != nil {
			src["_vector_score"] = *h.VectorScore
		}
		items = append(items, ResultItem{
			Model: documentModel{src}, Score: h.Score, Highlight: h.Highlight, VectorScore: h.VectorScore,
		})
	}
	return items
}

// documentModel is a ScoutModel backed by a raw hit payload, used by the *Raw
// pagination variants where no hydration occurs.
type documentModel struct{ doc map[string]any }

func (d documentModel) ScoutKey() any { return d.doc["_id"] }
func (d documentModel) TableName() string { return "raw" }
func (d documentModel) ToSearchableArray() map[string]any {
	out := map[string]any{}
	for k, v := range d.doc {
		out[k] = v
	}
	return out
}
func (d documentModel) ShouldBeSearchable() bool { return true }
