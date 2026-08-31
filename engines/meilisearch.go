package engines

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/erikwang2013/go-scout"
)

// MeilisearchEngine speaks the Meilisearch HTTP API: PUT /indexes/<i>/documents
// to upsert, POST /indexes/<i>/documents/delete-bys to remove, and
// POST /indexes/<i>/search to query. It mirrors MeilisearchEngine plus the
// AdvancedMeilisearchEngine filter/sort/vector/facet extensions.
type MeilisearchEngine struct {
	cfg    *scout.Config
	client *http.Client
	base   string
	key    string
}

// NewMeilisearch builds an engine from the "meilisearch" config section.
func NewMeilisearch(cfg *scout.Config) *MeilisearchEngine {
	if cfg == nil {
		cfg = scout.DefaultConfig()
	}
	return &MeilisearchEngine{
		cfg:    cfg,
		client: HTTPClient(30*time.Second, "", "", false),
		base:   strings.TrimRight(cfg.String("meilisearch.host", "http://127.0.0.1:7700"), "/"),
		key:    cfg.String("meilisearch.key", ""),
	}
}

// Name returns the driver name.
func (e *MeilisearchEngine) Name() string { return "meilisearch" }

func (e *MeilisearchEngine) headers() map[string]string {
	h := map[string]string{}
	if e.key != "" {
		h["X-Meili-API-Key"] = e.key
	}
	return h
}

// Update upserts models into their index.
func (e *MeilisearchEngine) Update(ctx context.Context, models []scout.ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	path := e.base + "/indexes/" + url.PathEscape(IndexName(models[0], e.cfg)) + "/documents"
	_, err := DoJSON(ctx, e.client, http.MethodPut, path, e.headers(), meiliDocuments(models))
	return meiliErr("update", err)
}

// Delete removes models from their index via a delete-bys filter.
func (e *MeilisearchEngine) Delete(ctx context.Context, models []scout.ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	key := scout.KeyNameOf(models[0])
	parts := make([]string, 0, len(models))
	for _, m := range models {
		parts = append(parts, key+" = "+meiliLit(m.ScoutKey()))
	}
	path := e.base + "/indexes/" + url.PathEscape(IndexName(models[0], e.cfg)) + "/documents/delete-bys"
	_, err := DoJSON(ctx, e.client, http.MethodPost, path, e.headers(), map[string]any{"filter": strings.Join(parts, " OR ")})
	return meiliErr("delete", err)
}

// Search runs the query described by b.
func (e *MeilisearchEngine) Search(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return e.run(ctx, b, b.GetLimit(), b.GetOffset())
}

// AdvancedSearch runs the full advanced query (filters, sorts, vector, facets).
func (e *MeilisearchEngine) AdvancedSearch(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return e.Search(ctx, b)
}

// Paginate runs the query for one page. limit/offset are derived from
// perPage/page so the builder is never mutated.
func (e *MeilisearchEngine) Paginate(ctx context.Context, b *scout.Builder, perPage, page int) (*scout.Result, error) {
	if perPage <= 0 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	return e.run(ctx, b, perPage, (page-1)*perPage)
}

// run issues one /search request and parses the response.
func (e *MeilisearchEngine) run(ctx context.Context, b *scout.Builder, limit, offset int) (*scout.Result, error) {
	body := meiliParams(b, limit, offset)
	if out := b.InvokeCallback(ctx, body); out != nil {
		if r, ok := out.(*scout.Result); ok {
			return r, nil
		}
		if raw, ok := out.(map[string]any); ok {
			return e.parse(raw, b), nil
		}
	}
	path := e.base + "/indexes/" + url.PathEscape(b.GetIndex()) + "/search"
	raw, err := DoJSON(ctx, e.client, http.MethodPost, path, e.headers(), body)
	if err != nil {
		return nil, fmt.Errorf("scout: meilisearch search: %w", err)
	}
	return e.parse(raw, b), nil
}

// MapIDs extracts the primary keys from a result.
func (e *MeilisearchEngine) MapIDs(results *scout.Result) []any {
	out := make([]any, 0, len(results.Hits))
	for _, h := range results.Hits {
		out = append(out, h.ID)
	}
	return out
}

// Map hydrates models for a result, preserving engine order.
func (e *MeilisearchEngine) Map(ctx context.Context, b *scout.Builder, results *scout.Result) ([]scout.ScoutModel, error) {
	ids := e.MapIDs(results)
	models, err := b.ModelsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return orderByIDPos(ids, models), nil
}

// GetTotalCount returns the total match count from a result.
func (e *MeilisearchEngine) GetTotalCount(results *scout.Result) int { return results.Total }

// Flush deletes the whole index for the model.
func (e *MeilisearchEngine) Flush(ctx context.Context, model scout.ScoutModel) error {
	path := e.base + "/indexes/" + url.PathEscape(IndexName(model, e.cfg))
	_, err := DoJSON(ctx, e.client, http.MethodDelete, path, e.headers(), nil)
	return meiliErr("flush", err)
}

// CreateIndex creates an index, seeding settings from
// meilisearch.index-settings[<name>] merged with options.
func (e *MeilisearchEngine) CreateIndex(ctx context.Context, name string, options map[string]any) (any, error) {
	settings := map[string]any{}
	if m, ok := e.cfg.Map("meilisearch.index-settings")[name].(map[string]any); ok {
		for k, v := range m {
			settings[k] = v
		}
	}
	for k, v := range options {
		settings[k] = v
	}
	body := map[string]any{"uid": name}
	if len(settings) > 0 {
		body["settings"] = settings
	}
	return DoJSON(ctx, e.client, http.MethodPost, e.base+"/indexes", e.headers(), body)
}

// DeleteIndex drops an index.
func (e *MeilisearchEngine) DeleteIndex(ctx context.Context, name string) (any, error) {
	path := e.base + "/indexes/" + url.PathEscape(name)
	_, err := DoJSON(ctx, e.client, http.MethodDelete, path, e.headers(), nil)
	if err != nil {
		return nil, fmt.Errorf("scout: meilisearch delete index: %w", err)
	}
	return nil, nil
}

// --- request building ---

// meiliParams assembles the /search body. Builder options are merged first, so
// the computed keys win (matching the PHP base engine's array_merge order).
func meiliParams(b *scout.Builder, limit, offset int) map[string]any {
	if limit <= 0 {
		limit = 20
	}
	key := scout.KeyNameOf(b.Model)
	body := map[string]any{}
	for k, v := range b.GetOptions() {
		body[k] = v
	}
	retrieve := []any{key}
	if cur, ok := body["attributesToRetrieve"]; ok {
		retrieve = append(retrieve, meiliAnyList(cur)...)
	}
	body["q"] = b.Query
	body["limit"] = limit
	body["offset"] = offset
	body["attributesToRetrieve"] = retrieve
	body["attributesToHighlight"] = meiliHighlightFields(b)
	// Advanced parity: highlight/ranking params from options; the PHP engine
	// sends the same defaults unconditionally, and these match the server's own.
	if v, ok := b.GetOptions()["highlight_pre_tag"]; ok {
		body["highlightPreTag"] = v
	}
	if v, ok := b.GetOptions()["highlight_post_tag"]; ok {
		body["highlightPostTag"] = v
	}
	if v, ok := b.GetOptions()["show_matches_position"]; ok {
		body["showMatchesPosition"] = v
	}
	if v, ok := b.GetOptions()["show_ranking_score"]; ok {
		body["showRankingScore"] = v
	}
	if f := meiliFilters(b); f != "" {
		body["filter"] = f
	}
	if s := meiliSorts(b); len(s) > 0 {
		body["sort"] = s
	}
	if f := meiliFacetFields(b); len(f) > 0 {
		body["facetsBy"] = f
	}
	if v := meiliVector(b); v != nil {
		body["vector"] = v
		body["hybrid"] = meiliHybrid(b)
	}
	return body
}

// meiliFilters renders wheres, whereIns, whereNotIns and advancedWheres into a
// Meilisearch filter expression joined by AND.
func meiliFilters(b *scout.Builder) string {
	var parts []string
	for _, field := range meiliKeys(b.Wheres) {
		value := b.Wheres[field]
		if v, ok := value.([]any); ok && len(v) > 0 {
			parts = append(parts, fmt.Sprintf("%s IN [%s]", field, meiliList(v)))
			continue
		}
		if value == nil {
			parts = append(parts, field+" IS NULL")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s = %s", field, meiliLit(value)))
	}
	for _, field := range meiliKeys(b.GetWhereIns()) {
		if v := b.GetWhereIns()[field]; len(v) > 0 {
			parts = append(parts, fmt.Sprintf("%s IN [%s]", field, meiliList(v)))
		}
	}
	for _, field := range meiliKeys(b.GetWhereNotIns()) {
		if v := b.GetWhereNotIns()[field]; len(v) > 0 {
			parts = append(parts, fmt.Sprintf("%s NOT IN [%s]", field, meiliList(v)))
		}
	}
	for _, c := range b.GetAdvancedWheres() {
		f := meiliAdvanced(c)
		if f == "" {
			continue
		}
		if c.Boolean == "not" {
			f = "NOT (" + f + ")"
		}
		parts = append(parts, f)
	}
	return strings.Join(parts, " AND ")
}

// --- helpers ---

func meiliDocuments(models []scout.ScoutModel) []map[string]any {
	out := make([]map[string]any, 0, len(models))
	for _, m := range models {
		doc := m.ToSearchableArray()
		if doc == nil {
			doc = map[string]any{}
		}
		doc[scout.KeyNameOf(m)] = m.ScoutKey()
		out = append(out, doc)
	}
	return out
}

// meiliLit renders a value as a Meilisearch filter literal: numbers bare,
// strings double-quoted with " and \ escaped, booleans lowercase.
func meiliLit(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case string:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(t) + `"`
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		return meiliLit(fmt.Sprint(t))
	}
}

func meiliList(vals []any) string {
	lits := make([]string, 0, len(vals))
	for _, v := range vals {
		lits = append(lits, meiliLit(v))
	}
	return strings.Join(lits, ", ")
}

func meiliDir(column, dir string) string { return column + ":" + meiliD(dir) }

func meiliD(dir string) string {
	if dir == "desc" {
		return "desc"
	}
	return "asc"
}

func meiliAnyList(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

func meiliInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	}
	return 0
}

func meiliNum(m map[string]any, key string) float64 {
	switch t := m[key].(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	}
	return 0
}

// meiliTotal prefers estimatedTotalHits (v1.8+) and falls back to totalHits.
func meiliTotal(raw map[string]any) int {
	if n := meiliInt(raw["estimatedTotalHits"]); n > 0 {
		return n
	}
	return meiliInt(raw["totalHits"])
}

func meiliKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// orderByIDPos reorders hydrated models into engine hit order.
func orderByIDPos(ids []any, models []scout.ScoutModel) []scout.ScoutModel {
	pos := map[any]int{}
	for i, id := range ids {
		pos[scout.KeyString(id)] = i
	}
	sort.SliceStable(models, func(i, j int) bool {
		return pos[scout.KeyString(models[i].ScoutKey())] < pos[scout.KeyString(models[j].ScoutKey())]
	})
	return models
}

func meiliErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("scout: meilisearch %s: %w", op, err)
}
