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

// OpenSearchEngine speaks the OpenSearch REST API. The endpoints used here are
// wire-compatible with Elasticsearch's, so it shares the es* request/parse
// helpers from elasticsearch.go. It mirrors the PHP plugin's
// AdvancedOpenSearchEngine; the base driver is resolved to this engine too.
type OpenSearchEngine struct {
	cfg    *scout.Config
	client *http.Client
	base   string
}

// NewOpenSearch builds an engine from the "opensearch" config section. Basic
// auth comes from username/password; TLS verification is skipped unless
// opensearch.ssl_verification is set (the plugin ships self-signed by default).
func NewOpenSearch(cfg *scout.Config) *OpenSearchEngine {
	if cfg == nil {
		cfg = scout.DefaultConfig()
	}
	user := cfg.String("opensearch.username", "admin")
	pass := cfg.String("opensearch.password", "admin")
	skipTLS := !cfg.Bool("opensearch.ssl_verification", false)
	return &OpenSearchEngine{
		cfg:    cfg,
		client: HTTPClient(time.Duration(cfg.Int("opensearch.timeout", 30))*time.Second, user, pass, skipTLS),
		base:   strings.TrimRight(cfg.String("opensearch.host", "https://127.0.0.1:6205"), "/"),
	}
}

// Name returns the driver name.
func (e *OpenSearchEngine) Name() string { return "opensearch" }

// Update upserts models via the per-document _doc endpoint.
func (e *OpenSearchEngine) Update(ctx context.Context, models []scout.ScoutModel) error {
	return esUpdate(ctx, e.client, e.base, e.cfg, models)
}

// Delete removes models via the per-document _doc endpoint. Missing documents
// are ignored, matching the PHP engine's catch-all.
func (e *OpenSearchEngine) Delete(ctx context.Context, models []scout.ScoutModel) error {
	return esDelete(ctx, e.client, e.base, e.cfg, models)
}

// Search runs the query described by b.
func (e *OpenSearchEngine) Search(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return esRun(ctx, e.client, e.base, "opensearch", b, b.GetLimit(), b.GetOffset())
}

// AdvancedSearch runs the full advanced query (filters, sorts, aggs, facets).
func (e *OpenSearchEngine) AdvancedSearch(ctx context.Context, b *scout.Builder) (*scout.Result, error) {
	return e.Search(ctx, b)
}

// Paginate runs the query for one page. limit/offset are derived from
// perPage/page so the builder is never mutated.
func (e *OpenSearchEngine) Paginate(ctx context.Context, b *scout.Builder, perPage, page int) (*scout.Result, error) {
	if perPage <= 0 {
		perPage = 20
	}
	if page < 1 {
		page = 1
	}
	return esRun(ctx, e.client, e.base, "opensearch", b, perPage, (page-1)*perPage)
}

// MapIDs extracts the primary keys from a result.
func (e *OpenSearchEngine) MapIDs(results *scout.Result) []any { return esMapIDs(results) }

// Map hydrates models for a result, preserving engine order.
func (e *OpenSearchEngine) Map(ctx context.Context, b *scout.Builder, results *scout.Result) ([]scout.ScoutModel, error) {
	return esMap(ctx, b, results)
}

// GetTotalCount returns the total match count from a result.
func (e *OpenSearchEngine) GetTotalCount(results *scout.Result) int { return results.Total }

// Flush deletes the whole index for the model.
func (e *OpenSearchEngine) Flush(ctx context.Context, model scout.ScoutModel) error {
	path := e.base + "/" + esIndex(model, e.cfg)
	_, err := DoJSON(ctx, e.client, http.MethodDelete, path, nil, nil)
	return esErr("flush", err)
}

// CreateIndex creates an index; options become the PUT body (mappings, etc.).
func (e *OpenSearchEngine) CreateIndex(ctx context.Context, name string, options map[string]any) (any, error) {
	if options == nil {
		options = map[string]any{}
	}
	return DoJSON(ctx, e.client, http.MethodPut, e.base+"/"+url.PathEscape(name), nil, options)
}

// DeleteIndex drops an index.
func (e *OpenSearchEngine) DeleteIndex(ctx context.Context, name string) (any, error) {
	path := e.base + "/" + url.PathEscape(name)
	_, err := DoJSON(ctx, e.client, http.MethodDelete, path, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("scout: opensearch delete index: %w", err)
	}
	return nil, nil
}

// GetAggregations returns the aggregations payload for a query.
func (e *OpenSearchEngine) GetAggregations(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	res, err := e.Search(ctx, b)
	if err != nil {
		return nil, err
	}
	return res.Aggregations, nil
}

// GetFacets returns the facet payloads for a query.
func (e *OpenSearchEngine) GetFacets(ctx context.Context, b *scout.Builder) (map[string]any, error) {
	return e.GetAggregations(ctx, b)
}
