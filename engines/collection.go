package engines

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/erikwang2013/go-scout"
)

// CollectionEngine searches the models themselves. There is no separate index,
// so indexing operations are no-ops and text queries are matched in memory with
// a case-insensitive substring scan over toSearchableArray().
type CollectionEngine struct{}

// NewCollection returns a CollectionEngine.
func NewCollection() *CollectionEngine { return &CollectionEngine{} }

// Name returns the driver name.
func (e *CollectionEngine) Name() string { return "collection" }

// Update is a no-op: the models are the index.
func (e *CollectionEngine) Update(context.Context, []scout.ScoutModel) error { return nil }

// Delete is a no-op: the models are the index.
func (e *CollectionEngine) Delete(context.Context, []scout.ScoutModel) error { return nil }

// Flush is a no-op: the models are the index.
func (e *CollectionEngine) Flush(context.Context, scout.ScoutModel) error { return nil }

// CreateIndex is a no-op.
func (e *CollectionEngine) CreateIndex(context.Context, string, map[string]any) (any, error) {
	return nil, nil
}

// DeleteIndex is a no-op.
func (e *CollectionEngine) DeleteIndex(context.Context, string) (any, error) { return nil, nil }

// Search returns the matching models capped at b.Limit. Total is the post-cap
// count, mirroring CollectionEngine::search().
func (e *CollectionEngine) Search(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	models, err := e.searchModels(ctx, b)
	if err != nil {
		return nil, err
	}
	if lim := b.GetLimit(); lim > 0 && len(models) > lim {
		models = models[:lim]
	}
	hits := modelHits(models)
	return &scout.Result{Hits: hits, Total: len(hits)}, nil
}

// Paginate returns the perPage/page slice; Total is the full match count.
func (e *CollectionEngine) Paginate(ctx context.Context, b *scout.Builder, perPage, page int) (*scout.Result, error) {
	models, err := e.searchModels(ctx, b)
	if err != nil {
		return nil, err
	}
	total := len(models)
	if perPage <= 0 {
		perPage = 15
	}
	if page < 1 {
		page = 1
	}
	start, end := (page-1)*perPage, (page-1)*perPage+perPage
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}
	return &scout.Result{Hits: modelHits(models[start:end]), Total: total}, nil
}

// MapIDs returns the document ids carried on the hits.
func (e *CollectionEngine) MapIDs(results *scout.Result) []any { return hitIDs(results) }

// Map reloads the models behind the hits by key, filtering to hit keys and
// restoring hit order.
func (e *CollectionEngine) Map(ctx context.Context, b *scout.Builder, results *scout.Result) ([]scout.ScoutModel, error) {
	return hydrateOrdered(ctx, b, hitIDs(results))
}

// GetTotalCount returns the total carried on the result.
func (e *CollectionEngine) GetTotalCount(results *scout.Result) int {
	if results == nil {
		return 0
	}
	return results.Total
}

// searchModels loads every model, applies the builder's constraints and
// ordering, then keeps only searchable records matching the text query.
func (e *CollectionEngine) searchModels(ctx context.Context, b *scout.Builder) ([]scout.ScoutModel, error) {
	if b.Source == nil {
		return nil, nil
	}
	models, err := b.Source.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("scout: collection engine: load: %w", err)
	}
	if cb := b.GetCallback(); cb != nil {
		// ponytail: a collection callback has no query builder to mutate, so it
		// runs with nil params; wheres/whereIns/whereNotIns are skipped,
		// mirroring PHP.
		cb(ctx, b, nil)
	} else {
		models = filterWheres(models, b)
	}
	models = sortModels(models, b)
	models = applySoftDelete(models, b)
	if len(models) == 0 {
		return models, nil
	}
	models = scout.MakeSearchableUsing(models[0], models)
	out := make([]scout.ScoutModel, 0, len(models))
	for _, m := range models {
		if m.ShouldBeSearchable() && (b.Query == "" || matchesQuery(m, b.Query)) {
			out = append(out, m)
		}
	}
	return out, nil
}

// filterWheres keeps models satisfying every Wheres/WhereIns/WhereNotIns pair,
// reading values from the searchable fields plus the key. __soft_deleted is
// handled by applySoftDelete.
func filterWheres(models []scout.ScoutModel, b *scout.Builder) []scout.ScoutModel {
	if len(b.Wheres) == 0 && len(b.WhereIns) == 0 && len(b.WhereNotIns) == 0 {
		return models
	}
	out := make([]scout.ScoutModel, 0, len(models))
	for _, m := range models {
		if wheresMatch(modelFields(m), b) {
			out = append(out, m)
		}
	}
	return out
}

func wheresMatch(fields map[string]any, b *scout.Builder) bool {
	for k, want := range b.Wheres {
		if k == "__soft_deleted" {
			continue
		}
		if !valueMatches(fields[k], want) {
			return false
		}
	}
	for k, vals := range b.WhereIns {
		if !containsAny(vals, fields[k]) {
			return false
		}
	}
	for k, vals := range b.WhereNotIns {
		if containsAny(vals, fields[k]) {
			return false
		}
	}
	return true
}

// applySoftDelete mirrors ensureSoftDeletesAreHandled: __soft_deleted selects
// live or trashed rows, the soft_delete config opts into withTrashed, and
// otherwise the base SoftDeletes behaviour hides trashed rows.
func applySoftDelete(models []scout.ScoutModel, b *scout.Builder) []scout.ScoutModel {
	want, ok := b.Wheres["__soft_deleted"].(int)
	if ok {
		return keepTrashed(models, want)
	}
	if b.Config != nil && b.Config.SoftDelete() {
		return models
	}
	if !scout.UsesSoftDelete(b.Model) {
		return models
	}
	return keepTrashed(models, 0)
}

func keepTrashed(models []scout.ScoutModel, want int) []scout.ScoutModel {
	out := make([]scout.ScoutModel, 0, len(models))
	for _, m := range models {
		if sd, ok := m.(scout.SoftDeleter); ok {
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

// sortModels applies b.Orders, or the key descending when none are set.
func sortModels(models []scout.ScoutModel, b *scout.Builder) []scout.ScoutModel {
	orders := b.GetOrders()
	if len(orders) == 0 {
		sort.SliceStable(models, func(i, j int) bool {
			return cmpAny(models[i].ScoutKey(), models[j].ScoutKey()) > 0
		})
		return models
	}
	fields := make([]map[string]any, len(models))
	for i, m := range models {
		fields[i] = modelFields(m)
	}
	sort.SliceStable(models, func(i, j int) bool {
		for _, o := range orders {
			c := cmpAny(fields[i][o.Column], fields[j][o.Column])
			if c != 0 {
				asc := strings.ToLower(strings.TrimSpace(o.Direction)) != "desc"
				return (c < 0) == asc
			}
		}
		return false
	})
	return models
}

// matchesQuery reports whether any searchable value contains the query,
// case-insensitively.
func matchesQuery(m scout.ScoutModel, q string) bool {
	lq := strings.ToLower(q)
	for _, v := range m.ToSearchableArray() {
		if strings.Contains(strings.ToLower(scalarString(v)), lq) {
			return true
		}
	}
	return false
}

// scalarString renders a searchable value: scalars stringify, everything else
// is JSON-encoded, as PHP does with is_scalar/json_encode.
func scalarString(v any) string {
	switch n := v.(type) {
	case nil:
		return ""
	case string:
		return n
	case bool:
		if n {
			return "true"
		}
		return "false"
	case int, int64, int32, float64, float32, json.Number:
		return fmt.Sprint(n)
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return fmt.Sprint(v)
}

// modelFields is the searchable payload plus the key, so constraints can name
// either.
func modelFields(m scout.ScoutModel) map[string]any {
	out := map[string]any{}
	for k, v := range m.ToSearchableArray() {
		out[k] = v
	}
	kn := scout.KeyNameOf(m)
	if _, ok := out[kn]; !ok {
		out[kn] = m.ScoutKey()
	}
	return out
}

func valueMatches(actual, want any) bool {
	if actual == nil || want == nil {
		return actual == want
	}
	if a, ok := actual.(string); ok {
		if w, ok := want.(string); ok {
			return a == w
		}
		return a == scout.KeyString(want)
	}
	if w, ok := want.(string); ok {
		return scout.KeyString(actual) == w
	}
	if af, ok := toFloat(actual); ok {
		if wf, ok := toFloat(want); ok {
			return af == wf
		}
	}
	return actual == want
}

func containsAny(vals []any, actual any) bool {
	for _, v := range vals {
		if valueMatches(actual, v) {
			return true
		}
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case string:
		if f, err := strconv.ParseFloat(n, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func cmpAny(a, b any) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	if af, ok := toFloat(a); ok {
		if bf, ok := toFloat(b); ok {
			switch {
			case af < bf:
				return -1
			case af > bf:
				return 1
			default:
				return 0
			}
		}
	}
	sa, sb := scout.KeyString(a), scout.KeyString(b)
	switch {
	case sa < sb:
		return -1
	case sa > sb:
		return 1
	default:
		return 0
	}
}

// modelHits converts models into hits carrying their searchable payload.
func modelHits(models []scout.ScoutModel) []scout.Hit {
	hits := make([]scout.Hit, 0, len(models))
	for _, m := range models {
		hits = append(hits, scout.Hit{ID: m.ScoutKey(), Source: m.ToSearchableArray()})
	}
	return hits
}

// hitIDs extracts the document ids from a result, skipping empty ones.
func hitIDs(results *scout.Result) []any {
	if results == nil {
		return nil
	}
	ids := make([]any, 0, len(results.Hits))
	for _, h := range results.Hits {
		if h.ID != nil {
			ids = append(ids, h.ID)
		}
	}
	return ids
}

// hydrateOrdered loads the models behind ids and restores their input order.
func hydrateOrdered(ctx context.Context, b *scout.Builder, ids []any) ([]scout.ScoutModel, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	models, err := b.ModelsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	pos := make(map[string]int, len(ids))
	for i, id := range ids {
		pos[scout.KeyString(id)] = i
	}
	out := make([]scout.ScoutModel, 0, len(models))
	for _, m := range models {
		if _, ok := pos[scout.KeyString(m.ScoutKey())]; ok {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return pos[scout.KeyString(out[i].ScoutKey())] < pos[scout.KeyString(out[j].ScoutKey())]
	})
	return out, nil
}
