package engines

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/erikwang2013/go-scout"
)

// ElasticsearchEngine speaks the Elasticsearch REST API: POST /<i>/_doc/<id>
// to upsert, DELETE /<i>/_doc/<id> to remove, POST /<i>/_search to query, and
// PUT/DELETE /<i> for index management. The JSON DSL comes from dsl.go, shared
// with the OpenSearch engine (the endpoints used here are wire-compatible).
type ElasticsearchEngine struct {
	cfg    *scout.Config
	client *http.Client
	base   string
}

// NewElasticSearch builds an engine from the "elasticsearch" config section.
// The base URL is the first entry of elasticsearch.hosts; optional basic auth
// comes from elasticsearch.auth.
func NewElasticSearch(cfg *scout.Config) *ElasticsearchEngine {
	if cfg == nil {
		cfg = scout.DefaultConfig()
	}
	base := "http://127.0.0.1:9200"
	if hosts := cfg.List("elasticsearch.hosts"); len(hosts) > 0 && hosts[0] != "" {
		base = hosts[0]
	}
	auth := cfg.Map("elasticsearch.auth")
	user, _ := auth["user"].(string)
	pass, _ := auth["password"].(string)
	return &ElasticsearchEngine{
		cfg:    cfg,
		client: HTTPClient(30*time.Second, user, pass, false),
		base:   strings.TrimRight(base, "/"),
	}
}

// Name returns the driver name.
func (e *ElasticsearchEngine) Name() string { return "elasticsearch" }

// Update upserts models via the per-document _doc endpoint.
func (e *ElasticsearchEngine) Update(ctx context.Context, models []scout.ScoutModel) error {
	return esUpdate(ctx, e.client, e.base, e.cfg, models)
}

// Delete removes models via the per-document _doc endpoint. Missing documents
// are ignored, matching the PHP engine's catch-all.
func (e *ElasticsearchEngine) Delete(ctx context.Context, models []scout.ScoutModel) error {
	return esDelete(ctx, e.client, e.base, e.cfg, models)
}

// Search runs the query described by b.
func (e *ElasticsearchEngine) Search(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return esRun(ctx, e.client, e.base, "elasticsearch", b, b.GetLimit(), b.GetOffset())
}

// AdvancedSearch runs the full advanced query (filters, sorts, aggs, facets).
func (e *ElasticsearchEngine) AdvancedSearch(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return e.Search(ctx, b)
}

// Paginate runs the query for one page. limit/offset are derived from
// perPage/page so the builder is never mutated.
func (e *ElasticsearchEngine) Paginate(ctx context.Context, b *scout.Builder, perPage, page int) (*scout.Result, error) {
	if perPage <= 0 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	return esRun(ctx, e.client, e.base, "elasticsearch", b, perPage, (page-1)*perPage)
}

// MapIDs extracts the primary keys from a result.
func (e *ElasticsearchEngine) MapIDs(results *scout.Result) []any { return esMapIDs(results) }

// Map hydrates models for a result, preserving engine order.
func (e *ElasticsearchEngine) Map(ctx context.Context, b *scout.Builder, results *scout.Result) ([]scout.ScoutModel, error) {
	return esMap(ctx, b, results)
}

// GetTotalCount returns the total match count from a result.
func (e *ElasticsearchEngine) GetTotalCount(results *scout.Result) int { return results.Total }

// Flush deletes the whole index for the model.
func (e *ElasticsearchEngine) Flush(ctx context.Context, model scout.ScoutModel) error {
	path := e.base + "/" + esIndex(model, e.cfg)
	_, err := DoJSON(ctx, e.client, http.MethodDelete, path, nil, nil)
	return esErr("flush", err)
}

// CreateIndex creates an index; options become the PUT body (mappings, etc.).
func (e *ElasticsearchEngine) CreateIndex(ctx context.Context, name string, options map[string]any) (any, error) {
	if options == nil {
		options = map[string]any{}
	}
	return DoJSON(ctx, e.client, http.MethodPut, e.base+"/"+url.PathEscape(name), nil, options)
}

// DeleteIndex drops an index.
func (e *ElasticsearchEngine) DeleteIndex(ctx context.Context, name string) (any, error) {
	path := e.base + "/" + url.PathEscape(name)
	_, err := DoJSON(ctx, e.client, http.MethodDelete, path, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("scout: elasticsearch delete index: %w", err)
	}
	return nil, nil
}

// GetAggregations returns the aggregations payload for a query.
func (e *ElasticsearchEngine) GetAggregations(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	res, err := e.Search(ctx, b)
	if err != nil {
		return nil, err
	}
	return res.Aggregations, nil
}

// GetFacets returns the facet payloads for a query.
func (e *ElasticsearchEngine) GetFacets(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	return e.GetAggregations(ctx, b)
}

// --- shared with the OpenSearch engine ---

func esIndex(m scout.ScoutModel, cfg *scout.Config) string {
	return url.PathEscape(IndexName(m, cfg))
}

func esUpdate(ctx context.Context, client *http.Client, base string, cfg *scout.Config, models []scout.ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	for _, m := range models {
		doc := m.ToSearchableArray()
		if doc == nil {
			doc = map[string]any{}
		}
		path := base + "/" + esIndex(m, cfg) + "/_doc/" + url.PathEscape(scout.KeyString(m.ScoutKey()))
		if _, err := DoJSON(ctx, client, http.MethodPost, path, nil, doc); err != nil {
			return esErr("update", err)
		}
	}
	return nil
}

func esDelete(ctx context.Context, client *http.Client, base string, cfg *scout.Config, models []scout.ScoutModel) error {
	if len(models) == 0 {
		return nil
	}
	for _, m := range models {
		path := base + "/" + esIndex(m, cfg) + "/_doc/" + url.PathEscape(scout.KeyString(m.ScoutKey()))
		// A 404 means the document is already gone; skip it. Anything else is
		// a real failure.
		if _, err := DoJSON(ctx, client, http.MethodDelete, path, nil, nil); err != nil && !strings.Contains(err.Error(), "returned 404") {
			return esErr("delete", err)
		}
	}
	return nil
}

// esRun issues one /_search request and parses the response.
func esRun(ctx context.Context, client *http.Client, base, name string, b *scout.Builder, limit, offset int) (*scout.Result, error) {
	body := esBody(b, limit, offset)
	if out := b.InvokeCallback(ctx, body); out != nil {
		if r, ok := out.(*scout.Result); ok {
			return r, nil
		}
		if raw, ok := out.(map[string]any); ok {
			return esParse(raw, b), nil
		}
	}
	path := base + "/" + url.PathEscape(b.GetIndex()) + "/_search"
	raw, err := DoJSON(ctx, client, http.MethodPost, path, nil, body)
	if err != nil {
		return nil, fmt.Errorf("scout: %s search: %w", name, err)
	}
	return esParse(raw, b), nil
}

// esBody assembles the /_search body from the shared DSL helpers. Builder
// options are merged first so the computed keys win.
func esBody(b *scout.Builder, limit, offset int) map[string]any {
	body := map[string]any{}
	for k, v := range b.GetOptions() {
		body[k] = v
	}
	body["query"] = BuildQuery(b)
	if limit > 0 {
		body["size"] = limit
	}
	if offset > 0 {
		body["from"] = offset
	}
	if s := buildSorts(b); len(s) > 0 {
		body["sort"] = s
	}
	aggs := buildAggregations(b)
	for k, v := range buildFacets(b) {
		aggs[k] = v
	}
	if len(aggs) > 0 {
		body["aggs"] = aggs
	}
	if hl := buildHighlight(b); hl != nil {
		body["highlight"] = hl
	}
	if s := buildSuggest(b); s != nil {
		body["suggest"] = s
	}
	return body
}

// esParse converts a raw _search response into a scout.Result.
func esParse(raw map[string]any, b *scout.Builder) *scout.Result {
	key := scout.KeyNameOf(b.Model)
	var hits []map[string]any
	// The hits object nests the array: {"hits": {"total": ..., "hits": [...]}}.
	for _, h := range esAnyList(asMap(raw["hits"])["hits"]) {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		doc, _ := hm["_source"].(map[string]any)
		if doc == nil {
			doc = map[string]any{}
		}
		id := hm["_id"]
		if id == nil {
			id = doc[key]
		}
		hits = append(hits, map[string]any{
			"_id":       id,
			"_score":    esNum(hm["_score"]),
			"_source":   doc,
			"highlight": hm["highlight"],
			"_index":    hm["_index"],
		})
	}
	total := 0
	if hs, ok := raw["hits"].(map[string]any); ok {
		if t, ok := hs["total"].(map[string]any); ok {
			total = esInt(t["value"])
		} else {
			total = esInt(hs["total"]) // ES6 reports total as a plain int
		}
	}
	res := Result(hits, total)
	if m, ok := raw["aggregations"].(map[string]any); ok {
		res.Aggregations = m
	}
	if t := esInt(raw["took"]); t > 0 {
		res.Took = t
	}
	if to, ok := raw["timed_out"].(bool); ok {
		res.TimedOut = to
	}
	res.Raw = raw
	for _, p := range b.GetResultProcessors() {
		res = p(res)
	}
	return res
}

func esMapIDs(results *scout.Result) []any {
	out := make([]any, 0, len(results.Hits))
	for _, h := range results.Hits {
		out = append(out, h.ID)
	}
	return out
}

func esMap(ctx context.Context, b *scout.Builder, results *scout.Result) ([]scout.ScoutModel, error) {
	ids := esMapIDs(results)
	models, err := b.ModelsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return orderByIDPos(ids, models), nil
}

func esAnyList(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

func esInt(v any) int {
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

func esNum(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	}
	return 0
}

func esErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("scout: elasticsearch %s: %w", op, err)
}
